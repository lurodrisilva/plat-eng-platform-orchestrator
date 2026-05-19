# Architecture

This document describes the internal architecture of the Platform Orchestrator: how its layers are organised, how data flows through a deployment, and which guarantees each layer is responsible for.

## Table of Contents

- [Goals & Non-Goals](#goals--non-goals)
- [Layered View](#layered-view)
- [Domain Layer](#domain-layer)
- [Application Layer](#application-layer)
- [Adapters](#adapters)
- [Infrastructure](#infrastructure)
- [Deployment Lifecycle](#deployment-lifecycle)
- [Error Model](#error-model)
- [Concurrency & Workflow Plan](#concurrency--workflow-plan)
- [Observability](#observability)
- [Known Gaps](#known-gaps)

## Goals & Non-Goals

**Goals**

- Centralize the deployment contract between application CI and Argo CD.
- Enforce environment / branch policy at a single boundary.
- Produce immutable, digest-pinned OCI artifacts for every deployment.
- Maintain a complete, queryable audit trail of deployment lifecycle events.
- Keep business rules portable: the domain layer must compile and run with no external services available.

**Non-Goals**

- Replacing Argo CD's reconciliation loop. The orchestrator submits an `Application` and observes it; Argo owns convergence.
- Hosting application source code or generating manifests from scratch. Charts are authored by application teams and consumed unchanged.
- Acting as a long-term event store. Lifecycle events are persisted, but the orchestrator is not a stream processor.

## Layered View

```
┌─────────────────────────────────────────────────────────────────┐
│                       cmd/server (composition root)             │
│  - Loads config                                                 │
│  - Inits telemetry, Mongo client                                │
│  - Wires concrete adapters into the application                 │
│  - Starts HTTP server, owns graceful shutdown                   │
└─────────────────────────────────────────────────────────────────┘
                                  │
        ┌─────────────────────────┼─────────────────────────┐
        ▼                                                   ▼
┌───────────────────────┐                       ┌────────────────────────┐
│  inbound/http         │                       │  outbound/*            │
│  - router             │                       │  - argocd.Client       │
│  - handler.Deployment │                       │  - github.ChartResolver│
│  - handler.Health     │                       │  - oci.Publisher       │
│  - middleware.{...}   │                       │  - persistence.Repo    │
└──────────┬────────────┘                       └────────────┬───────────┘
           │ depends on                                      │ implements
           ▼                                                 ▼
    ┌──────────────────────────────────────────────────────────────┐
    │                       application/                           │
    │  ┌────────────────────────┐    ┌──────────────────────────┐  │
    │  │  application/deployment│    │  application/port        │  │
    │  │  - Application{Cmds,Q} │◀───│  - ArgoCD                │  │
    │  │  - CreateDeploymentH   │    │  - ArtifactPublisher     │  │
    │  │  - GetDeploymentH      │    │  - ChartResolver/Composer│  │
    │  │  - DeploymentDTO       │    │  - PolicyEvaluator       │  │
    │  │                        │    │  - TokenValidator        │  │
    │  └───────────┬────────────┘    └──────────────────────────┘  │
    └──────────────┼───────────────────────────────────────────────┘
                   │ depends on
                   ▼
        ┌────────────────────────────────────────────────────┐
        │                    domain/deployment               │
        │  - Deployment (aggregate root)                     │
        │  - Image / Target / Source / ChartSource (VOs)     │
        │  - Status enum + transition map (14 states)        │
        │  - Repository (port — owned by the domain)         │
        │  - Sentinel errors                                 │
        └────────────────────────────────────────────────────┘
```

### Dependency rules (enforced by convention + code review)

| From                              | May import                                      | May **not** import |
|-----------------------------------|-------------------------------------------------|--------------------|
| `internal/domain/**`              | Go stdlib only                                  | anything else in the module |
| `internal/application/port`       | `context`                                       | domain mutators, adapters |
| `internal/application/deployment` | `internal/domain/**`, `internal/application/port` | adapters, infrastructure |
| `internal/adapter/**`             | `internal/domain/**`, `internal/application/port` | sibling adapters, `internal/application/deployment` (other than as a value bundle through `inbound`) |
| `internal/infrastructure/**`      | Go stdlib + third-party setup libs              | application, adapter, domain logic |
| `cmd/**`                          | anything                                        | (it is the only allowed wiring layer) |

Compile-time port assertions (`var _ port.X = (*Impl)(nil)`) live at the end of each outbound adapter.

## Domain Layer

Package: `internal/domain/deployment`. Pure Go, std-lib only.

### Aggregate

`Deployment` is the aggregate root. It owns:

- Identity (`DeploymentID`, deterministic from `(date, appID, env[:4], shortSHA)`)
- Required value objects: `Image`, `ChartSource`, `Target`, `Source`
- Optional generated artifacts: `Metadata`, `Artifact`, `ArgoApp`, `Health`
- A `Status` and the lifecycle timestamps (`startedAt`, `updatedAt`, `completedAt`)
- The caller-supplied `values` map (deep-copied on ingest **and** retrieval to prevent aliasing)

### Value objects

All constructors validate inputs and return zero values + errors on failure. `NewImage` requires a `sha256:` digest; `NewSource` requires a SHA of at least 7 characters; `NewTarget` requires all four fields; `NewChartSource` requires repo + name. There are no setters that bypass these checks.

### Status machine

```
RECEIVED ──▶ VALIDATING ──▶ REJECTED (terminal)
                       │
                       ├──▶ METADATA_GENERATED ──▶ CHART_RESOLVED ──▶ CHART_COMPOSED
                       │                                                  │
                       │                                                  ▼
                       │                                          ARTIFACT_PUBLISHED
                       │                                                  │
                       │                                                  ▼
                       │                                            ARGO_APP_CREATED
                       │                                                  │
                       │                              ┌───────────────────┼───────────────────┐
                       │                              ▼                   ▼                   ▼
                       │                          SYNCING            ROLLED_BACK           FAILED
                       │                              │                                       ▲
                       │                ┌─────────────┼─────────────┐                         │
                       │                ▼             ▼             ▼                         │
                       │             HEALTHY      DEGRADED        FAILED ────────────────────▶┘
                       │                │             │
                       │                ▼             ▼
                       │            COMPLETED    ROLLED_BACK (terminal)
                       │             (terminal)
                       └──▶ FAILED (terminal)
```

Allowed transitions live in `_transitions` (map) in `status.go`. Terminal states (`COMPLETED`, `FAILED`, `REJECTED`, `ROLLED_BACK`) accept no outgoing transitions. `CanTransitionTo` and `IsTerminal` are pure functions.

### Repository port (domain-owned)

```go
type Repository interface {
    Save(ctx context.Context, d *Deployment) error
    FindByID(ctx context.Context, id DeploymentID) (*Deployment, error)
    FindByApplication(ctx context.Context, appID, environment string, limit int) ([]*Deployment, error)
}
```

Defined in `domain/deployment/repository.go` so the domain controls its own persistence contract; implemented by `adapter/outbound/persistence`.

## Application Layer

Package: `internal/application/deployment` (aliased `deploymentapp` to disambiguate from the domain). The layer exposes a CQRS-light surface:

```go
type Application struct {
    Commands Commands
    Queries  Queries
}

type Commands struct { CreateDeployment *CreateDeploymentHandler }
type Queries  struct { GetDeployment    *GetDeploymentHandler    }
```

Every handler has `New<X>Handler(deps) *<X>Handler` and `Handle(ctx, cmd) (Result, error)`. Errors wrap with the use-case name: `fmt.Errorf("create deployment: %w", err)`.

### Outbound ports

| Port                   | File                            | Implementation directory                           |
|------------------------|---------------------------------|----------------------------------------------------|
| `ArgoCD`               | `application/port/argocd.go`    | `adapter/outbound/argocd`                          |
| `ArtifactPublisher`    | `application/port/artifact.go`  | `adapter/outbound/oci`                             |
| `ChartResolver`        | `application/port/chart.go`     | `adapter/outbound/github`                          |
| `ChartComposer`        | `application/port/chart.go`     | *(not yet implemented — Helm v3 in-process)*       |
| `PolicyEvaluator`      | `application/port/policy.go`    | *(not yet implemented — reads `policies/*.yaml`)*  |
| `TokenValidator`       | `application/port/auth.go`      | *(not yet implemented — GitHub Actions OIDC JWKS)* |

Ports speak in DTOs defined alongside the interface (e.g. `ArgoAppSpec`, `PublishedArtifact`, `ResolvedChart`). DTOs are flat primitives — they do **not** reference domain aggregates.

## Adapters

### Inbound: HTTP

`internal/adapter/inbound/http` uses `net/http.ServeMux` with Go 1.22+ method-prefixed patterns (`POST /api/v1/deployments`). The chain wraps mux outside-in:

```
Recovery(Logging(Tracing(mux)))
```

Recovery catches panics in any layer; Logging captures status + duration; Tracing opens an OTel span named `<METHOD> <path>` with `http.method` and `http.url` attributes.

Handlers translate JSON requests into application commands. Error mapping:

| Failure                              | HTTP status | `error.code`            |
|--------------------------------------|-------------|--------------------------|
| Missing/invalid `Authorization`      | 401         | `AUTHENTICATION_FAILED`  |
| Malformed JSON body                  | 400         | `VALIDATION_ERROR`       |
| Use-case error (validation, policy)  | 422         | `DEPLOYMENT_FAILED`      |
| Repository `ErrNotFound`             | 404         | `NOT_FOUND`              |
| Panic                                | 500         | `INTERNAL_ERROR`         |

> The 422 bucket currently swallows several distinct error classes — refine by `errors.Is(err, deployment.Err*)` mapping.

### Outbound

| Adapter        | Strategy                                                                                       |
|----------------|------------------------------------------------------------------------------------------------|
| `argocd`       | Direct REST calls (`/api/v1/projects`, `/api/v1/applications`) over `net/http` with bearer auth. `CreateOrUpdate` POSTs then falls back to PUT on conflict. Sync policy: `automated{prune:true, selfHeal:false}`, retry limit 2 (10s→60s exponential). |
| `github`       | `api.github.com` over `net/http`. Paginated tag listing, semver filtering via `golang.org/x/mod/semver`, tarball download capped at 100 MiB by `io.LimitReader`. |
| `oci`          | `helm.sh/helm/v3/pkg/registry` `client.Push`. Reference: `oci://{host}[/{prefix}]/{appID}/charts/{chartName}:{version}`. Returns manifest digest. |
| `persistence`  | `go.mongodb.org/mongo-driver` `UpdateOne` upsert with `$set`. `mongo.ErrNoDocuments` → `domain.ErrNotFound`. Default page size 50, sorted by `startedAt DESC`. |

## Infrastructure

- `infrastructure/config` — YAML loader with `os.ExpandEnv` (`${VAR:-default}`), defaults, and required-field validation.
- `infrastructure/telemetry` — OTel `TracerProvider` setup (OTLP gRPC, head-based sampling), `slog` JSON logger with service-name attr, `Enrich(ctx, logger)` to add `trace_id` + `span_id` when a span is active.

Both are leaves: they may **not** import application or adapter packages.

## Deployment Lifecycle

A single `POST /api/v1/deployments` triggers the following sequence. Today the HTTP path covers steps 1–4; the remaining steps will run in the Temporal worker (currently a stub).

```
1. OIDC token validated                  → claims extracted          [HTTP]
2. Request body decoded                  → CreateDeploymentCommand
3. Policy evaluated                      → allow / deny              [PolicyEvaluator]
4. Aggregate built + saved (RECEIVED)    → 202 returned to client    [Repository]
   ──── boundary: from here on, async ────
5. Status → VALIDATING                                               [Worker]
6. Status → METADATA_GENERATED, set Metadata
7. Chart resolved (GitHub tags + tarball) → CHART_RESOLVED          [ChartResolver]
8. Chart composed (Helm load + values merge + repackage) → CHART_COMPOSED [ChartComposer]
9. Artifact published to OCI registry    → ARTIFACT_PUBLISHED       [ArtifactPublisher]
10. Argo CD AppProject validated          → ARGO_APP_CREATED         [ArgoCD]
11. Argo CD sync triggered                → SYNCING                  [ArgoCD]
12. Poll Argo status until terminal       → HEALTHY | DEGRADED | FAILED [ArgoCD]
13. HEALTHY → COMPLETED ; DEGRADED → ROLLED_BACK or FAILED          [aggregate]
```

The deployment ID is deterministic from the start, so the client can `GET /api/v1/deployments/{id}` immediately after the 202 to poll status.

## Error Model

- Domain sentinels live in `internal/domain/deployment/errors.go`: `ErrNotFound`, `ErrPolicyViolation`, `ErrInvalidTransition`, `ErrChartNotFound`, `ErrChartValidation`, `ErrChartDependency`, `ErrAppProjectDenied`, `ErrAuthentication`.
- Use cases wrap with `fmt.Errorf("<use case>: %w", err)`; adapters wrap with the operation (`fmt.Errorf("save deployment %s: %w", id, err)`).
- HTTP handlers `errors.Is` against sentinels to map status codes (work in progress).
- `domain.NonRetryableTypes()` returns the list of error type names that the Temporal worker uses to short-circuit retry policy.

## Concurrency & Workflow Plan

The current single-shot HTTP create endpoint persists the initial aggregate and returns immediately. Long-running work (chart resolution, OCI publish, Argo CD reconciliation polling) belongs in a **Temporal workflow**, with activities calling the same `port.*` interfaces:

```
Activity boundary mirrors the port boundary one-to-one:
  ChartResolver.Resolve     → ResolveChartActivity
  ChartComposer.Compose     → ComposeChartActivity
  ArtifactPublisher.Publish → PublishArtifactActivity
  ArgoCD.{CreateOrUpdate,Sync,Status} → 3 activities, polled by workflow
```

The worker entrypoint (`cmd/worker/main.go`) is currently a stub. Wiring will land alongside concrete `PolicyEvaluator`, `TokenValidator`, and `ChartComposer` adapters.

## Observability

- **Traces** — head-sampled OTel traces with `service.name` resource attribute. Span hierarchy: HTTP request span → use-case span (TODO) → port-call span (TODO).
- **Logs** — structured JSON via `slog`, with `service` attribute set at process start; `trace_id` / `span_id` injected per request by `telemetry.Enrich`.
- **Metrics** — not yet emitted. Likely additions: `deployment_total{status}`, `deployment_duration_seconds`, `chart_resolution_failures_total`, `argo_app_unhealthy_total`.

## Known Gaps

The hexagonal scaffolding is complete; the missing pieces are concrete adapters and worker wiring:

| Gap                                                     | Where                                                  |
|---------------------------------------------------------|--------------------------------------------------------|
| `TokenValidator` adapter (OIDC JWKS, JWT verify)        | New: `internal/adapter/inbound/auth/`                  |
| `PolicyEvaluator` adapter (YAML loader + evaluator)     | New: `internal/adapter/outbound/policy/`               |
| `ChartComposer` adapter (Helm load + merge + repackage) | New: `internal/adapter/outbound/helm/`                 |
| Temporal worker wiring                                  | `cmd/worker/main.go` (stub)                            |
| Repository persistence of Metadata/Artifact/ArgoApp/Health | `internal/adapter/outbound/persistence/deployment.go` |
| Real semver constraint matching in chart resolution     | `internal/adapter/outbound/github/resolver.go` (`filterByConstraint`) |
| Use-case error → HTTP status fine-grained mapping       | `internal/adapter/inbound/http/handler/deployment.go`  |
| Idempotency on `POST /deployments` (header is accepted but ignored) | `internal/adapter/inbound/http/handler/deployment.go` |
| Helm chart for cluster deploy (raw manifests today)     | `deploy/`                                              |
| Prometheus / OTLP metrics                               | `internal/infrastructure/telemetry/`                   |

Each of these is documented in the corresponding directory's `AGENTS.md`.

# Platform Orchestrator

A Go-based deployment control plane that sits between **GitHub Actions** and **Argo CD**. It accepts OIDC-authenticated deployment requests from CI, validates them against organizational policy, resolves and packages Helm charts with per-deployment values, publishes immutable OCI artifacts, and drives Argo CD `Application` resources to sync + health convergence.

Built as a hexagonal (ports + adapters) Go service with a clean separation between domain logic, use cases, and external integrations.

## Table of Contents

- [Why](#why)
- [Architecture](#architecture)
- [API](#api)
- [Quick Start](#quick-start)
- [Configuration](#configuration)
- [Build & Test](#build--test)
- [Deployment](#deployment)
- [Project Layout](#project-layout)
- [Observability](#observability)
- [Policy](#policy)
- [Contributing](#contributing)
- [License](#license)

## Why

In a GitOps-style platform, every application team writes its own GitHub Actions workflow that pushes changes to an environment-specific Git repo of manifests; Argo CD then reconciles. This works but leaks platform concerns into every pipeline: chart resolution, values composition, OCI publishing, AppProject mapping, sync orchestration, and rollback semantics get re-implemented in every repo and drift over time.

The **Platform Orchestrator** centralises those concerns behind a single HTTP API. A workflow calls `POST /api/v1/deployments` with the image digest, target environment, and a values overlay; the orchestrator does the rest:

1. **Authenticate** the workflow via GitHub Actions OIDC (no shared secrets).
2. **Authorize** against environment + branch policy (e.g. `production` allows only `main` and `release/*`).
3. **Resolve** the Helm chart version from a GitHub repository using semver constraints.
4. **Compose** the chart with caller-provided values + platform defaults.
5. **Publish** the result as an immutable OCI artifact (digest-pinned).
6. **Reconcile** an Argo CD `Application` that points at the artifact and waits for sync + healthy.
7. **Persist** the full audit trail (request, decisions, artifact digests, Argo state) in DocumentDB.

The result: every team's CI shrinks to one HTTP call, and every deployment leaves a tamper-evident trail.

## Architecture

The codebase follows **hexagonal architecture** (ports & adapters) with strict layer rules:

```
                     ┌─────────────────────────────────────┐
                     │              cmd/server             │
                     │        (composition root)           │
                     └────────────────┬────────────────────┘
                                      │ wires
                                      ▼
   ┌─────────────────┐      ┌─────────────────┐      ┌──────────────────┐
   │  Inbound HTTP   │      │  Application    │      │ Outbound         │
   │  (handlers,     │─────▶│  (use cases)    │─────▶│ (Argo CD,        │
   │   middleware)   │      │                 │      │  GitHub, OCI,    │
   │                 │      │                 │      │  DocumentDB)     │
   └─────────────────┘      └─────────┬───────┘      └──────────────────┘
                                      │ depends on
                                      ▼
                            ┌─────────────────────┐
                            │       Domain        │
                            │ (aggregates, value  │
                            │  objects, status    │
                            │  machine)           │
                            └─────────────────────┘
```

**Dependency direction is one-way**:
- `internal/domain/**` — zero project imports, std-lib only
- `internal/application/**` — imports domain + own ports
- `internal/adapter/**` — implements ports, never imports other adapters
- `cmd/**` — the **only** place that wires concrete adapters into the application

See [ARCHITECTURE.md](ARCHITECTURE.md) for the full layer-by-layer breakdown, port catalog, and deployment lifecycle state machine.

## API

The orchestrator exposes a small HTTP surface. All endpoints return JSON.

| Method | Path                          | Auth          | Purpose                                  |
|--------|-------------------------------|---------------|------------------------------------------|
| GET    | `/healthz`                    | none          | Liveness probe                           |
| GET    | `/readyz`                     | none          | Readiness probe                          |
| POST   | `/api/v1/deployments`         | OIDC bearer   | Create a new deployment                  |
| GET    | `/api/v1/deployments/{id}`    | (none yet)    | Read deployment status by id             |

### Create a deployment

```http
POST /api/v1/deployments HTTP/1.1
Authorization: Bearer <github-actions-oidc-jwt>
Content-Type: application/json
Idempotency-Key: payment-service-staging-abc123-987-1

{
  "application": { "id": "payment-service", "team": "payments" },
  "image": {
    "repository": "myregistry.azurecr.io/payment-service",
    "tag": "v1.4.2",
    "digest": "sha256:abc123..."
  },
  "chart": {
    "repository": "myorg/helm-charts",
    "name": "payment-service",
    "versionConstraint": "~1.5.0",
    "allowPrerelease": false
  },
  "target": {
    "environment": "staging",
    "cluster": "aks-staging",
    "namespace": "payments",
    "appProject": "payments-staging"
  },
  "values": { "hex-scaffold": { "replicaCount": 3 } },
  "resources": [
    { "type": "postgres", "size": "small", "version": "16", "storageMb": 32768 }
  ],
  "source": {
    "gitSha": "abc123def456...",
    "gitRef": "refs/heads/main",
    "githubRunId": "987654321",
    "githubRunAttempt": 1,
    "workflowName": "deploy",
    "actor": "octocat"
  },
  "correlationId": "ci-2026-05-19-001"
}
```

**`values` is umbrella-relative, so app knobs are scoped to the `hex-scaffold` subchart alias.**
The deploy unit is the umbrella (ADR-0008) and the application is a subchart within it; the
umbrella has no root `replicaCount` or `resources` key, so a root-scoped override reaches no
template — it renders nothing and errors nowhere. The tunable allowlist names the same
alias-scoped paths, so validation and render finally agree.

`resources` declares the application dependencies to provision alongside the app, as part of
the same deploy unit (ADR-0023). Notes that matter:

- **`resources[].name` is not accepted.** The name is derived from `application.id`, so the
  `PostgresInstance` XR, the Azure server and the app's bind cannot disagree about which
  database the app uses. A `name` sent anyway is ignored and logged.
- **It bills.** Each entry is a real Azure Database for PostgreSQL Flexible Server, and
  `size: small` maps to `GP_Standard_D2s_v3` — a General Purpose SKU.
- **It is governed server-side**, per environment, by `resourcePolicy` in the policies file.
  Unlike the tunable allowlist, that policy is enforce-first: a refusal is `422
  RESOURCE_NOT_ALLOWED`, never logged-and-allowed. **Production denies `postgres`** (ADR-0010).
- **`values.sqldatabase.*` and `values['hex-scaffold'].postgres.bindBuildingBlock.*` are
  reserved** and refused outright — they are the chart shape the platform writes, and reaching
  them from the overlay would provision infrastructure with no policy evaluation.

The same field is accepted by `POST /api/v1/apps`, where it sets the scaffolded repository's
default database shape.

Response `202 Accepted`:

```json
{
  "deploymentId": "dep-20260519-payment-service-stag-abc123d",
  "status": "RECEIVED",
  "statusUrl": "/api/v1/deployments/dep-20260519-payment-service-stag-abc123d",
  "createdAt": "2026-05-19T12:34:56Z"
}
```

### Read deployment status

```http
GET /api/v1/deployments/dep-20260519-payment-service-stag-abc123d
```

Response `200 OK`:

```json
{
  "id": "dep-20260519-...",
  "applicationId": "payment-service",
  "environment": "staging",
  "cluster": "aks-staging",
  "namespace": "payments",
  "status": "HEALTHY",
  "imageRepository": "myregistry.azurecr.io/payment-service",
  "imageDigest": "sha256:abc123...",
  "chartVersion": "1.5.3-20260519123500-abc123d",
  "argoAppName": "payment-service-staging-payments",
  "argoSyncStatus": "Synced",
  "argoHealthStatus": "Healthy",
  "startedAt": "2026-05-19T12:34:56Z",
  "completedAt": "2026-05-19T12:38:21Z",
  "durationMs": 205000
}
```

### Error envelope

All non-2xx responses share this shape:

```json
{
  "error": { "code": "AUTHENTICATION_FAILED", "message": "missing authorization header" },
  "timestamp": "2026-05-19T12:34:56Z"
}
```

Codes in use: `AUTHENTICATION_FAILED` (401), `VALIDATION_ERROR` (400), `DEPLOYMENT_FAILED` (422), `NOT_FOUND` (404), `INTERNAL_ERROR` (500).

## Quick Start

### Prerequisites

- Go **1.25** or later (the module declares `go 1.25.0`)
- Docker (for container builds)
- Access to a MongoDB-compatible store (Azure DocumentDB or MongoDB) for state
- Argo CD reachable from the orchestrator
- An OCI registry the orchestrator can push to
- A GitHub App / PAT with read access to chart repos

### Run locally

```bash
make build
export CONFIG_PATH=./config.yaml
export DOCUMENTDB_ENDPOINT="mongodb://localhost:27017"
export GITHUB_TOKEN=ghp_...
./bin/server
```

The server listens on `:8080` by default. Hit `/healthz` to confirm it is up.

### Smoke test with the Bruno collection

```bash
cd api-tests
bru run --env local --env-var oidcToken=<test-jwt>
```

See `api-tests/AGENTS.md` for the full set of scenarios (happy paths + 22 negative tests).

## Configuration

Configuration is YAML with `${VAR:-default}` env-var expansion. Default file is `config.yaml` at the working directory; override with `CONFIG_PATH`.

| Section              | Purpose                                                                 |
|----------------------|-------------------------------------------------------------------------|
| `server`             | Listen addr, read/write timeouts                                        |
| `temporal`           | Temporal host:port, namespace, task queue (worker entrypoint, WIP)      |
| `auth.oidc`          | GitHub Actions OIDC issuer, audience, JWKS cache TTL                    |
| `argocd`             | Argo CD server URL; token from env (`ARGOCD_TOKEN` — wire in main)      |
| `oci`                | Registry host + optional repository prefix                              |
| `github`             | Token (env: `GITHUB_TOKEN`)                                             |
| `documentdb`         | Connection string (env: `DOCUMENTDB_ENDPOINT`), database, collections   |
| `otel`               | Service name, OTLP gRPC endpoint, trace sample rate, insecure flag      |
| `policies`           | Path to policy YAML (default `policies/default.yaml`)                   |
| `deploymentDefaults` | Sync / health / workflow / poll timeouts (seconds)                      |

Secret-bearing fields are tagged `yaml:"-"` in `internal/infrastructure/config/config.go` and must be populated via env-var substitution in `config.yaml` (or set explicitly in `cmd/server/main.go` after `Load`).

See `internal/infrastructure/config/AGENTS.md` for the full schema, defaults, and validation rules.

## Build & Test

```bash
make build         # builds bin/server and bin/worker
make test          # go test -race -count=1 ./...
make test-coverage # writes coverage.html
make lint          # golangci-lint run ./...
make fmt           # go fmt ./...
make vet           # go vet ./...
make vuln-check    # govulncheck ./...
make docker-build  # builds both container images
make mod-tidy      # go mod tidy
make clean
```

CI (`.github/workflows/ci.yaml`) runs `build`, `lint`, and `vulnerability-check` on every push/PR to `main`, then builds Docker images for `main`-branch pushes.

### Testing strategy

- **Domain** (`internal/domain/deployment`) — table-driven unit tests for the state machine + aggregate. Run with `go test ./internal/domain/...`.
- **Application** — handler tests against fake repository + fake ports (to be expanded).
- **Adapters** — integration tests against ephemeral services (Mongo testcontainer, fake HTTP server).
- **API contract** — Bruno collection in `api-tests/`.

## Deployment

Raw Kubernetes manifests for the API server and the Temporal worker live in `deploy/kubernetes/`. They expect three out-of-band resources in the same namespace:

- `ServiceAccount/platform-orchestrator`
- `ConfigMap/platform-orchestrator-config` (containing `config.yaml`)
- `ConfigMap/platform-orchestrator-policies` (containing the policy YAML)
- `Secret/platform-orchestrator-secrets` (env-injected: tokens, connection strings)

Apply with:

```bash
kubectl apply -n platform-orchestrator -f deploy/kubernetes/
```

Both Deployments run with a hardened pod security context: non-root, read-only root filesystem, all capabilities dropped. The image references default to `myregistry.azurecr.io/platform-orchestrator-{server,worker}:latest` — replace with immutable tags before production rollout.

> A Helm chart is **not** provided yet. See `deploy/kubernetes/AGENTS.md` for the manifest details.

## Project Layout

```
.
├── cmd/
│   ├── server/         # HTTP API binary (composition root)
│   └── worker/         # Temporal worker (stub — to be wired)
├── internal/
│   ├── domain/
│   │   └── deployment/ # Aggregate, value objects, status machine, repository port
│   ├── application/
│   │   ├── deployment/ # Use-case handlers (Create, Get)
│   │   └── port/       # Outbound port interfaces (ArgoCD, ChartResolver, etc.)
│   ├── adapter/
│   │   ├── inbound/
│   │   │   └── http/   # Router + handlers + middleware
│   │   └── outbound/
│   │       ├── argocd/
│   │       ├── github/
│   │       ├── oci/
│   │       └── persistence/
│   └── infrastructure/
│       ├── config/     # YAML + env-var config loader
│       └── telemetry/  # OTel + slog bootstrap
├── policies/           # Deployment policy YAML (env rules, freeze windows)
├── deploy/
│   └── kubernetes/     # Raw manifests for server + worker
├── api-tests/          # Bruno API test collection
├── .github/workflows/  # CI definitions
├── config.yaml         # Default runtime config
├── Dockerfile.server   # Distroless multi-stage build
├── Dockerfile.worker
├── Makefile
└── go.mod
```

Every directory has an `AGENTS.md` describing its purpose, key files, dependencies, and AI-agent conventions. Start at the root [`AGENTS.md`](AGENTS.md) and follow the `<!-- Parent: -->` links downward.

## Observability

- **Tracing**: OpenTelemetry, OTLP/gRPC exporter. Spans for every HTTP request (`<METHOD> <path>`) plus span context propagation across outbound calls. Configure endpoint via `otel.otlpEndpoint` (env: `OTEL_EXPORTER_OTLP_ENDPOINT`).
- **Logging**: Structured `slog` JSON to stdout. Every log entry inside a request scope is enriched with `trace_id` + `span_id` (see `telemetry.Enrich`).
- **Metrics**: not yet emitted. Adding Prometheus / OTLP metrics is the next planned infra addition.

## Policy

Deployment policy lives in `policies/default.yaml`. The default ruleset:

- `production` — allow refs matching `main` or `release/*`
- `staging` — additionally allow `hotfix/*`
- `development` — allow all branches, all repos
- `freezeWindows: []`

Policy is evaluated by `port.PolicyEvaluator.Evaluate(repo, gitRef, environment)` before any chart resolution. No concrete evaluator adapter is wired yet — see `policies/AGENTS.md`.

## Contributing

Contributions are welcome. Please read [CONTRIBUTING.md](CONTRIBUTING.md) for the local dev loop, code style, hex-architecture rules, and commit / PR conventions.

## License

(TBD — add LICENSE file before publishing externally.)

# Platform Orchestrator — Complete Planning & Architecture Design

**Version:** 1.0.0
**Date:** 2026-04-12
**Status:** Planning Phase Complete — Ready for Implementation

---

## Section A — Executive Summary

The Platform Orchestrator is a **deployment control plane** written in Go that sits between GitHub Actions CI pipelines and Argo CD GitOps delivery. It receives deployment requests from GitHub Actions workflows, enriches them with governance metadata, resolves and composes Helm charts using the Helm SDK, publishes immutable OCI artifacts, creates Argo CD Applications via API, monitors deployment health to terminal state, and persists the full deployment lifecycle to Azure DocumentDB.

The system uses **Temporal** as its workflow orchestration engine, providing durable execution, retry semantics, idempotency, compensation, and operational visibility. Every step is instrumented with **OpenTelemetry** traces and metrics, with structured logging via `slog` enriched with trace context.

**Key architectural decisions:**
- **OCI registry** as the artifact publication target (not Git or Helm repo) — immutable, content-addressable, natively supported by Argo CD 2.6+
- **GitHub OIDC federation** for zero-secret authentication from GitHub Actions
- **Temporal Search Attributes** for cross-system correlation (GitHub run ID ↔ Temporal workflow ID ↔ Argo CD app name ↔ deployment ID)
- **Saga-pattern compensation** via Temporal for rollback scenarios
- **Azure Managed Identity** for DocumentDB access with least-privilege RBAC

The system is designed for enterprise production: auditable, resilient to partial failures, deterministic in version selection, and explicit about what constitutes deployment success.

---

## Section B — Assumptions and Open Decisions

### Assumptions

| # | Assumption | Impact if Wrong |
|---|-----------|-----------------|
| A1 | Temporal Server is available (self-hosted or Temporal Cloud) | Need to provision Temporal infrastructure first |
| A2 | An OCI-compatible registry exists (ACR, GHCR, or Harbor) | Must provision before artifact publishing works |
| A3 | Argo CD 2.8+ is deployed with API access enabled | OCI Helm source support requires 2.6+; we target 2.8+ for stability |
| A4 | Azure DocumentDB (Cosmos DB with MongoDB API) is provisioned | State persistence requires existing infrastructure |
| A5 | GitHub Actions runners have network access to the orchestrator endpoint | Firewall/VPN rules must allow egress |
| A6 | Each application has exactly one Helm chart in a dedicated GitHub repository | Multi-chart repos require resolver changes |
| A7 | Helm charts follow SemVer tagging (e.g., `v1.2.3`, `1.2.3`) | Non-SemVer tags break deterministic selection |
| A8 | Argo CD AppProjects are pre-created and managed separately | Orchestrator validates against existing projects, does not create them |
| A9 | One deployment = one Argo CD Application = one Helm release | Fan-out deployments are out of scope for MVP |
| A10 | The orchestrator runs as a Kubernetes deployment within the same network as Argo CD | Cross-cluster API access adds latency and auth complexity |

### Open Decisions Requiring Stakeholder Input

| # | Decision | Recommendation | Alternative |
|---|----------|---------------|-------------|
| D1 | OCI registry choice | Azure Container Registry (ACR) — native Azure identity, geo-replication | GHCR if GitHub-centric |
| D2 | Temporal hosting | Temporal Cloud — managed, no operational burden | Self-hosted if data sovereignty required |
| D3 | API gateway in front of orchestrator | Yes — Azure API Management or Kong for rate limiting, JWT validation offload | Direct exposure with middleware |
| D4 | Multi-environment promotion model | Out of scope for v1 — each environment gets its own workflow invocation | Pipeline-based promotion in v2 |
| D5 | Chart signing (Cosign/Notation) | Defer to v2 — adds complexity without blocking deployment flow | v1 relies on OCI digest immutability |
| D6 | Notification system (Slack, Teams) | Defer to v2 — Temporal queries provide visibility for v1 | Add webhook notifications in v2 |

---

## Section C — Planning Phase

### C.1 Scope

**In scope:**
- HTTP API to receive deployment requests from GitHub Actions
- GitHub OIDC token validation
- Policy validation (environment gates, AppProject alignment, branch rules)
- Deterministic Helm chart resolution from GitHub repositories
- Helm chart composition with values injection using Helm SDK
- OCI artifact publication
- Argo CD Application creation and health monitoring
- Azure DocumentDB state persistence with audit trail
- Temporal workflow orchestration with compensation
- OpenTelemetry instrumentation (traces, metrics, structured logs)
- Deployment status query API

**Out of scope for v1:**
- UI / dashboard (use Temporal Web + Argo CD UI)
- Multi-chart / umbrella deployments
- Cross-environment promotion pipelines
- Chart signing and verification
- Notification integrations
- Canary / progressive delivery strategies
- Self-service AppProject management

### C.2 Milestones

| Milestone | Description | Target Week |
|-----------|-------------|-------------|
| M0 | Project scaffolding, CI, dev environment | Week 1-2 |
| M1 | API layer + OIDC auth + request validation | Week 3-4 |
| M2 | Temporal workflow skeleton + state machine | Week 5-6 |
| M3 | Chart resolver + Helm SDK composition | Week 7-8 |
| M4 | OCI artifact publisher | Week 9 |
| M5 | Argo CD integration (create + watch) | Week 10-11 |
| M6 | DocumentDB state persistence | Week 12 |
| M7 | Compensation / rollback flows | Week 13 |
| M8 | Observability hardening | Week 14 |
| M9 | Integration testing + load testing | Week 15-16 |
| M10 | Staging rollout + production readiness review | Week 17-18 |

### C.3 Workstreams

1. **API & Auth** — HTTP server, request validation, OIDC verification, policy engine
2. **Orchestration** — Temporal workflows, activities, state machine, compensation
3. **Helm & Artifacts** — Chart resolver, Helm SDK integration, OCI publisher
4. **Argo CD** — Application creation, sync monitoring, health evaluation, AppProject validation
5. **State & Audit** — DocumentDB client, schema, state transitions, audit trail
6. **Observability** — OTel setup, custom metrics, structured logging, dashboards
7. **Infrastructure** — Kubernetes manifests, Temporal deployment, CI/CD pipeline
8. **Testing** — Unit tests, integration tests, contract tests, chaos scenarios

### C.4 Risks

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Temporal operational complexity | Medium | High | Use Temporal Cloud; invest in runbooks early |
| Helm SDK API instability | Low | Medium | Pin Helm SDK version; wrap behind interface |
| Argo CD API breaking changes | Low | Medium | Pin Argo CD client version; integration tests |
| OCI registry unavailability | Low | High | Health checks; retry with backoff; alert on failure |
| DocumentDB throttling under load | Medium | Medium | Provision adequate RU/s; implement backpressure |
| GitHub OIDC token validation edge cases | Medium | High | Comprehensive claim validation; deny-by-default |
| Chart dependency resolution failures | Medium | Medium | Require pre-locked dependencies; fail fast |

### C.5 Team Roles

| Role | Count | Responsibility |
|------|-------|---------------|
| Tech Lead / Architect | 1 | Architecture decisions, code reviews, integration design |
| Backend Engineer (Go) | 2-3 | Core implementation across all workstreams |
| Platform Engineer | 1 | Temporal, Kubernetes, CI/CD infrastructure |
| SRE / Observability | 1 | OTel setup, dashboards, alerting, runbooks |

### C.6 Sequencing

```
Week 1-2:   [M0] Scaffolding ──────────────────────────────────────────►
Week 3-4:   [M1] API + Auth ───────────────────────►
Week 5-6:   [M2] Temporal Workflow ─────────────────► (depends on M0)
Week 7-9:   [M3+M4] Helm + OCI ────────────────────► (parallel with M2)
Week 10-11: [M5] Argo CD ──────────────────────────► (depends on M4)
Week 12:    [M6] DocumentDB ───────────────────────► (parallel with M5)
Week 13:    [M7] Compensation ─────────────────────► (depends on M5, M6)
Week 14:    [M8] Observability ────────────────────► (parallel)
Week 15-16: [M9] Testing ─────────────────────────► (depends on all)
Week 17-18: [M10] Staging + Prod ──────────────────► (depends on M9)
```

### C.7 MVP vs Production-Hardening

**MVP (Weeks 1-13):**
- Single environment, single cluster
- Basic policy validation (environment + namespace + branch)
- Happy-path deployment flow end-to-end
- Basic compensation (delete Argo app on failure)
- DocumentDB state tracking
- Structured logging

**Production-Hardening (Weeks 14-18):**
- Full OTel traces + metrics + dashboards
- Load testing and performance tuning
- Chaos testing (Temporal worker crash, OCI unavailable, Argo CD timeout)
- Comprehensive compensation scenarios
- Operational runbooks
- Rate limiting and circuit breakers
- Security audit

---

## Section D — Target Architecture

### D.1 Component Overview

```
┌─────────────────────────────────────────────────────────────────────┐
│                        GitHub Actions                               │
│  ┌─────────────────────────────────────────────────────────────┐    │
│  │  CI Workflow → Build → Test → Push Image → Call Orchestrator│    │
│  └──────────────────────────────┬──────────────────────────────┘    │
└─────────────────────────────────┼───────────────────────────────────┘
                                  │ HTTPS + OIDC JWT
                                  ▼
┌─────────────────────────────────────────────────────────────────────┐
│                   Platform Orchestrator                              │
│                                                                     │
│  ┌──────────────┐  ┌──────────────────┐  ┌───────────────────────┐ │
│  │  API Server   │  │  OIDC Validator   │  │  Policy Engine        │ │
│  │  (HTTP/gRPC)  │──│  (JWT claims)     │──│  (env/branch/ns)     │ │
│  └──────┬───────┘  └──────────────────┘  └───────────────────────┘ │
│         │                                                           │
│         ▼                                                           │
│  ┌──────────────┐                                                   │
│  │  Temporal     │                                                   │
│  │  Client       │──── Starts DeploymentWorkflow                    │
│  └──────────────┘                                                   │
│                                                                     │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │  Temporal Workers                                            │   │
│  │                                                              │   │
│  │  Activities:                                                 │   │
│  │  ┌─────────────┐ ┌──────────────┐ ┌───────────────────────┐ │   │
│  │  │ Validate     │ │ Generate     │ │ Resolve Chart         │ │   │
│  │  │ Deployment   │ │ Metadata     │ │ (GitHub API)          │ │   │
│  │  └─────────────┘ └──────────────┘ └───────────────────────┘ │   │
│  │  ┌─────────────┐ ┌──────────────┐ ┌───────────────────────┐ │   │
│  │  │ Compose      │ │ Publish      │ │ Create Argo App       │ │   │
│  │  │ Chart (Helm) │ │ OCI Artifact │ │ (Argo CD API)         │ │   │
│  │  └─────────────┘ └──────────────┘ └───────────────────────┘ │   │
│  │  ┌─────────────┐ ┌──────────────┐ ┌───────────────────────┐ │   │
│  │  │ Watch Sync   │ │ Update State │ │ Compensate            │ │   │
│  │  │ (Argo CD)    │ │ (DocumentDB) │ │ (Rollback)            │ │   │
│  │  └─────────────┘ └──────────────┘ └───────────────────────┘ │   │
│  └──────────────────────────────────────────────────────────────┘   │
│                                                                     │
│  ┌──────────────┐  ┌──────────────┐  ┌───────────────────────┐     │
│  │  OTel SDK     │  │  Config       │  │  Health / Readiness   │     │
│  │  (traces,     │  │  (env, YAML)  │  │  Probes               │     │
│  │   metrics)    │  │               │  │                       │     │
│  └──────────────┘  └──────────────┘  └───────────────────────┘     │
└─────────────────────────────────────────────────────────────────────┘
        │                    │                      │
        ▼                    ▼                      ▼
┌──────────────┐  ┌──────────────────┐  ┌───────────────────────┐
│  OCI Registry │  │  Argo CD Server   │  │  Azure DocumentDB     │
│  (ACR)        │  │  (API + GitOps)   │  │  (State + Audit)      │
└──────────────┘  └──────────────────┘  └───────────────────────┘
                          │
                          ▼
                  ┌──────────────────┐
                  │  Target K8s      │
                  │  Cluster         │
                  └──────────────────┘
```

### D.2 Component Descriptions

**API Server:** HTTP server (net/http + chi router) exposing `POST /api/v1/deployments` and `GET /api/v1/deployments/{id}`. Validates OIDC JWT, deserializes request, performs synchronous policy checks, starts Temporal workflow, returns workflow ID immediately.

**OIDC Validator:** Validates GitHub Actions OIDC tokens. Fetches JWKS from `https://token.actions.githubusercontent.com/.well-known/jwks`. Validates claims: `iss`, `aud`, `repository`, `ref`, `workflow`, `job_workflow_ref`. Caches JWKS with TTL.

**Policy Engine:** Evaluates deployment policies: allowed environments, allowed branches per environment, namespace ownership, AppProject alignment, deployment freeze windows. Configured via YAML policy files.

**Chart Resolver:** Queries GitHub API for tags on the chart repository. Applies deterministic SemVer selection rule. Downloads chart archive. Validates chart integrity.

**Chart Composer:** Loads chart via Helm SDK. Merges provided values.yaml with platform-injected metadata. Mutates Chart.yaml version and appVersion. Resolves and validates dependencies. Packages final chart.

**Artifact Publisher:** Pushes packaged chart to OCI registry with deterministic tag. Verifies push succeeded. Returns OCI reference with digest.

**Argo CD Integration:** Creates/updates Application resource via Argo CD REST API. Sets source to OCI reference. Configures sync policy. Polls Application status until terminal state.

**Deployment Watcher:** Polls Argo CD Application status with backoff. Evaluates health and sync status against explicit success criteria. Enforces per-step and global timeouts.

**State Repository:** Writes deployment state transitions to Azure DocumentDB. Maintains current-state document and event-history collection. Supports queries by deployment ID, application, environment, time range.

**Observability Subsystem:** OTel SDK configured with OTLP exporter. Trace spans for each activity. Custom metrics (deployment count, duration, failure rate). Structured logs via `slog` with trace context injection.

---

## Section E — End-to-End Deployment Sequence

### E.1 Happy Path

```
1. GitHub Actions workflow completes CI (build, test, push image)
2. Final step: HTTP POST to Platform Orchestrator /api/v1/deployments
   - Body: deployment request JSON with OIDC token in Authorization header
   - Idempotency-Key header with deployment correlation ID

3. API Server receives request
   3a. Validate OIDC JWT → extract claims (repo, ref, workflow, actor)
   3b. Deserialize and validate request body
   3c. Check idempotency: if workflow already exists for this key, return existing ID
   3d. Run synchronous policy checks:
       - Is this repo allowed to deploy to this environment?
       - Is this branch allowed for this environment?
       - Is this namespace within the AppProject scope?
       - Is there an active deployment freeze?
   3e. If any check fails → return 403/422 with explicit reason
   3f. Start Temporal workflow "DeploymentWorkflow" with deployment request
   3g. Return 202 Accepted with { deploymentId, temporalWorkflowId, statusUrl }

4. Temporal DeploymentWorkflow begins
   State → RECEIVED

5. Activity: ValidateDeployment (defensive re-validation within workflow)
   - Re-validate all policy rules (in case of race between API and workflow start)
   - Validate Argo CD AppProject exists and allows source/destination
   State → VALIDATING → (success) or REJECTED (terminal)

6. Activity: GenerateMetadata
   - Generate component ID: {app}-{env}-{cluster}-{sha7}
   - Generate deployment version: {chartVersion}-{timestamp}-{sha7}
   - Generate Argo app name: {app}-{env}-{namespace}
   - Compute all deployment tags and labels
   State → METADATA_GENERATED

7. Activity: ResolveChart
   - Query GitHub API: GET /repos/{owner}/{repo}/tags
   - Filter tags matching SemVer pattern
   - Select highest SemVer tag (deterministic)
   - Download chart archive from GitHub release asset or archive URL
   - Verify chart archive integrity (size, structure)
   State → CHART_RESOLVED

8. Activity: ComposeChart
   - Load chart with Helm SDK
   - Validate chart structure and required files
   - Resolve dependencies (must be pre-locked in Chart.lock)
   - Deep-merge provided values.yaml with platform metadata
   - Inject image, imageDigest, deployment labels into values
   - Mutate Chart.yaml: set version to deployment version, appVersion to git SHA
   - Package chart to .tgz
   State → CHART_COMPOSED

9. Activity: PublishArtifact
   - Push chart .tgz to OCI registry: oci://{registry}/{app}/charts/{chartName}:{deploymentVersion}
   - Verify push: pull manifest, confirm digest match
   - Record OCI reference with digest
   State → ARTIFACT_PUBLISHED

10. Activity: CreateArgoApplication
    - Build Application spec:
      source: OCI reference from step 9
      destination: cluster + namespace from request
      project: resolved AppProject name
      syncPolicy: automated with prune, selfHeal disabled
    - POST /api/v1/applications (or PUT if app exists)
    - Trigger sync: POST /api/v1/applications/{name}/sync
    State → ARGO_APP_CREATED

11. Activity: WatchDeployment
    - Poll GET /api/v1/applications/{name} every 10s
    - Evaluate:
      - sync.status == "Synced" AND health.status == "Healthy" → SUCCESS
      - health.status == "Degraded" after sync complete → TERMINAL FAILURE
      - health.status == "Progressing" → continue polling
      - health.status == "Missing" → TERMINAL FAILURE
      - health.status == "Unknown" after 2 min → TERMINAL FAILURE
    - Max poll duration: 10 minutes
    - If timeout reached → TERMINAL FAILURE
    State → SYNCING → HEALTHY or DEGRADED or FAILED

12. Activity: PersistState
    - Write final deployment document to DocumentDB
    - Write event-history entry for terminal state
    - Include full metadata, timestamps, Argo CD status snapshot
    State → COMPLETED (terminal)

13. Workflow completes successfully
    - Temporal records workflow as Completed
```

### E.2 Failure Branches and Compensation

```
FAILURE AT STEP 5 (Validation):
  → State → REJECTED
  → PersistState(REJECTED) — record rejection reason
  → No compensation needed
  → Workflow ends

FAILURE AT STEP 7 (Chart Resolution):
  → Retry 3 times with exponential backoff (GitHub API may be flaky)
  → If all retries fail → State → FAILED
  → PersistState(FAILED)
  → No compensation needed (no side effects yet)

FAILURE AT STEP 8 (Chart Composition):
  → No retry (deterministic operation — if it fails, it's a bug)
  → State → FAILED
  → PersistState(FAILED)

FAILURE AT STEP 9 (Artifact Publication):
  → Retry 3 times (registry may be temporarily unavailable)
  → If all retries fail → State → FAILED
  → PersistState(FAILED)
  → No compensation needed (partial push is not visible until complete)

FAILURE AT STEP 10 (Argo App Creation):
  → Retry 2 times
  → If all retries fail:
    → COMPENSATE: Delete OCI artifact tag (optional — immutable tags can stay)
    → State → FAILED
    → PersistState(FAILED)
  → Note: Artifact in OCI is immutable and harmless; compensation is optional

FAILURE AT STEP 11 (Deployment Watch — Degraded/Failed):
  → State → DEGRADED or FAILED
  → COMPENSATE:
    → If previous healthy version exists:
      → Record failed deployment
      → Mark Argo CD Application for manual intervention
      → DO NOT auto-rollback (dangerous without human judgment)
    → If no previous version:
      → Delete Argo CD Application
    → PersistState(FAILED) with compensation details
  → Emit alert via OTel metric + log

FAILURE AT STEP 12 (DocumentDB Write):
  → Retry 5 times with backoff (transient CosmosDB errors are common)
  → If all retries fail:
    → Log CRITICAL — deployment succeeded but state not persisted
    → Emit metric: deployment_state_persist_failure
    → DO NOT compensate the deployment itself (it's healthy!)
    → Temporal will show the workflow as failed, providing backup audit trail
```

---

## Section F — API Contract Design

### F.1 Create Deployment

**Endpoint:** `POST /api/v1/deployments`

**Headers:**
```
Authorization: Bearer <GitHub OIDC JWT>
Content-Type: application/json
Idempotency-Key: <deployment-correlation-id or generated UUID>
X-Request-ID: <optional trace correlation>
```

**Request Body:**
```json
{
  "application": {
    "id": "payment-service",
    "team": "payments"
  },
  "image": {
    "repository": "myregistry.azurecr.io/payment-service",
    "tag": "v1.5.2",
    "digest": "sha256:a1b2c3d4e5f6..."
  },
  "chart": {
    "repository": "github.com/myorg/helm-charts",
    "name": "payment-service",
    "versionConstraint": ""
  },
  "target": {
    "environment": "production",
    "cluster": "aks-prod-eastus2",
    "namespace": "payments"
  },
  "values": {
    "replicaCount": 3,
    "resources": {
      "limits": { "cpu": "500m", "memory": "512Mi" },
      "requests": { "cpu": "250m", "memory": "256Mi" }
    },
    "ingress": { "enabled": true, "host": "payments.example.com" }
  },
  "source": {
    "gitSha": "abc123def456",
    "gitRef": "refs/tags/v1.5.2",
    "githubRunId": "12345678",
    "githubRunAttempt": 1,
    "workflowName": "deploy-production",
    "actor": "johndoe",
    "repositoryFullName": "myorg/payment-service"
  },
  "correlationId": "deploy-abc123-1712880000"
}
```

**Response (202 Accepted):**
```json
{
  "deploymentId": "dep-20260412-payment-service-prod-abc123d",
  "temporalWorkflowId": "deploy:payment-service:production:abc123d",
  "temporalRunId": "run-uuid-here",
  "status": "RECEIVED",
  "statusUrl": "/api/v1/deployments/dep-20260412-payment-service-prod-abc123d",
  "createdAt": "2026-04-12T10:30:00Z"
}
```

**Response (409 Conflict — idempotent duplicate):**
```json
{
  "deploymentId": "dep-20260412-payment-service-prod-abc123d",
  "status": "SYNCING",
  "message": "Deployment already in progress for this idempotency key",
  "statusUrl": "/api/v1/deployments/dep-20260412-payment-service-prod-abc123d"
}
```

### F.2 Get Deployment Status

**Endpoint:** `GET /api/v1/deployments/{deploymentId}`

**Response (200 OK):**
```json
{
  "deploymentId": "dep-20260412-payment-service-prod-abc123d",
  "status": "HEALTHY",
  "application": "payment-service",
  "environment": "production",
  "cluster": "aks-prod-eastus2",
  "namespace": "payments",
  "image": {
    "repository": "myregistry.azurecr.io/payment-service",
    "digest": "sha256:a1b2c3d4e5f6..."
  },
  "chartVersion": "1.5.2-20260412103000-abc123d",
  "argoAppName": "payment-service-production-payments",
  "argoSyncStatus": "Synced",
  "argoHealthStatus": "Healthy",
  "temporalWorkflowId": "deploy:payment-service:production:abc123d",
  "startedAt": "2026-04-12T10:30:00Z",
  "completedAt": "2026-04-12T10:33:45Z",
  "stateHistory": [
    { "state": "RECEIVED", "at": "2026-04-12T10:30:00Z" },
    { "state": "VALIDATING", "at": "2026-04-12T10:30:01Z" },
    { "state": "METADATA_GENERATED", "at": "2026-04-12T10:30:02Z" },
    { "state": "CHART_RESOLVED", "at": "2026-04-12T10:30:05Z" },
    { "state": "CHART_COMPOSED", "at": "2026-04-12T10:30:07Z" },
    { "state": "ARTIFACT_PUBLISHED", "at": "2026-04-12T10:30:12Z" },
    { "state": "ARGO_APP_CREATED", "at": "2026-04-12T10:30:14Z" },
    { "state": "SYNCING", "at": "2026-04-12T10:30:15Z" },
    { "state": "HEALTHY", "at": "2026-04-12T10:33:45Z" },
    { "state": "COMPLETED", "at": "2026-04-12T10:33:46Z" }
  ]
}
```

### F.3 Error Model

```json
{
  "error": {
    "code": "POLICY_VIOLATION",
    "message": "Branch 'feature/xyz' is not allowed to deploy to environment 'production'",
    "details": {
      "policy": "branch-environment-gate",
      "branch": "feature/xyz",
      "environment": "production",
      "allowedBranches": ["main", "release/*"]
    }
  },
  "requestId": "req-uuid",
  "timestamp": "2026-04-12T10:30:00Z"
}
```

**Error codes:**
- `AUTHENTICATION_FAILED` — 401 — Invalid or expired OIDC token
- `AUTHORIZATION_DENIED` — 403 — Repository not authorized for target
- `POLICY_VIOLATION` — 422 — Deployment policy check failed
- `VALIDATION_ERROR` — 400 — Malformed request body
- `CONFLICT` — 409 — Duplicate idempotency key with different payload
- `INTERNAL_ERROR` — 500 — Unexpected server error
- `SERVICE_UNAVAILABLE` — 503 — Temporal unreachable

### F.4 Authentication Requirements

Every request **must** include a valid GitHub Actions OIDC JWT in the `Authorization: Bearer` header. The orchestrator validates:

1. **Signature** — against GitHub's JWKS endpoint
2. **Issuer** — must be `https://token.actions.githubusercontent.com`
3. **Audience** — must match configured audience (e.g., `platform-orchestrator.example.com`)
4. **Expiry** — token must not be expired
5. **Repository claim** — `repository` must be in the allow-list
6. **Ref claim** — `ref` must match environment-specific branch rules
7. **Workflow claim** — `job_workflow_ref` must match allowed workflow patterns

### F.5 Idempotency

The `Idempotency-Key` header is **required**. Recommended format: `{application}-{environment}-{gitSha}-{runId}-{runAttempt}`.

Behavior:
- First request with a key → start workflow, return 202
- Subsequent request with same key and same payload → return current status (200 or 202)
- Subsequent request with same key but different payload → return 409 Conflict

Implementation: Temporal's `WorkflowIDReusePolicy` with deterministic workflow ID derived from idempotency key.

---

## Section G — Temporal Workflow Design

### G.1 Workflow Definition

**Workflow name:** `DeploymentWorkflow`

**Workflow ID format:** `deploy:{application}:{environment}:{shortSha}`

This deterministic ID enables:
- Natural idempotency via Temporal's duplicate detection
- Easy lookup by operations teams
- Correlation with deployment ID

**Workflow input:** `DeploymentRequest` struct (full request payload + generated metadata)

**Workflow timeout:** 20 minutes (global — covers all activities + polling)

### G.2 Activities

| Activity | Responsibility | Timeout | Retry Policy | Idempotent? |
|----------|---------------|---------|--------------|-------------|
| `ValidateDeployment` | Policy checks + AppProject validation | 30s | 2 attempts, no backoff | Yes |
| `GenerateMetadata` | Component ID, version, labels | 5s | 1 attempt (pure computation) | Yes |
| `ResolveChart` | GitHub API tag query + download | 60s | 3 attempts, 5s initial, 2x backoff | Yes |
| `ComposeChart` | Helm load + merge + package | 30s | 1 attempt (deterministic) | Yes |
| `PublishArtifact` | OCI push + verify | 120s | 3 attempts, 10s initial, 2x backoff | Yes (OCI push is idempotent by digest) |
| `CreateArgoApp` | Argo CD API create/update + sync trigger | 60s | 2 attempts, 5s initial | Yes (upsert pattern) |
| `WatchDeployment` | Poll Argo CD status | 600s (10min) | 1 attempt (long-poll internally) | Yes |
| `PersistState` | DocumentDB write | 30s | 5 attempts, 2s initial, 2x backoff | Yes (upsert by deployment ID) |
| `CompensateArgoApp` | Delete or revert Argo app | 60s | 3 attempts | Yes |
| `CompensateArtifact` | Delete OCI tag (optional) | 30s | 1 attempt | Yes |

### G.3 Retry Policies

**Retryable errors:**
- Network timeouts (GitHub API, OCI registry, Argo CD API, DocumentDB)
- HTTP 429 (rate limited)
- HTTP 500/502/503/504 from external services
- DocumentDB throttling (HTTP 429 / RequestRateTooLarge)

**Non-retryable errors (fail immediately):**
- Policy violation (deployment not allowed)
- Authentication failure
- Invalid chart structure
- Argo CD AppProject mismatch
- Invalid values.yaml schema
- Chart dependency resolution failure
- HTTP 400/404 from external services (deterministic client errors)

```go
// Retryable external call
retryPolicy := &temporal.RetryPolicy{
    InitialInterval:    5 * time.Second,
    BackoffCoefficient: 2.0,
    MaximumInterval:    60 * time.Second,
    MaximumAttempts:    3,
    NonRetryableErrorTypes: []string{
        "PolicyViolationError",
        "AuthenticationError",
        "ChartValidationError",
        "AppProjectMismatchError",
    },
}

// Non-retryable computation
noRetry := &temporal.RetryPolicy{
    MaximumAttempts: 1,
}
```

### G.4 Compensation Strategy (Saga Pattern)

```go
func DeploymentWorkflow(ctx workflow.Context, req DeploymentRequest) (DeploymentResult, error) {
    var compensations []func(ctx workflow.Context) error

    // Step 1: Validate
    if err := workflow.ExecuteActivity(ctx, ValidateDeployment, req).Get(ctx, nil); err != nil {
        persistState(ctx, req.DeploymentID, StateFailed, err)
        return result, err
    }

    // Step 2: Generate metadata
    var meta DeploymentMetadata
    workflow.ExecuteActivity(ctx, GenerateMetadata, req).Get(ctx, &meta)

    // Step 3: Resolve chart
    var chart ResolvedChart
    if err := workflow.ExecuteActivity(ctx, ResolveChart, req).Get(ctx, &chart); err != nil {
        persistState(ctx, req.DeploymentID, StateFailed, err)
        return result, err
    }

    // Step 4: Compose chart
    var composed ComposedChart
    if err := workflow.ExecuteActivity(ctx, ComposeChart, req, chart, meta).Get(ctx, &composed); err != nil {
        persistState(ctx, req.DeploymentID, StateFailed, err)
        return result, err
    }

    // Step 5: Publish artifact — first side effect, add compensation
    var artifact PublishedArtifact
    if err := workflow.ExecuteActivity(ctx, PublishArtifact, composed).Get(ctx, &artifact); err != nil {
        persistState(ctx, req.DeploymentID, StateFailed, err)
        return result, err
    }
    compensations = append(compensations, func(ctx workflow.Context) error {
        return workflow.ExecuteActivity(ctx, CompensateArtifact, artifact).Get(ctx, nil)
    })

    // Step 6: Create Argo app — second side effect
    var argoApp ArgoApplication
    if err := workflow.ExecuteActivity(ctx, CreateArgoApp, req, artifact, meta).Get(ctx, &argoApp); err != nil {
        runCompensations(ctx, compensations)
        persistState(ctx, req.DeploymentID, StateFailed, err)
        return result, err
    }
    compensations = append(compensations, func(ctx workflow.Context) error {
        return workflow.ExecuteActivity(ctx, CompensateArgoApp, argoApp).Get(ctx, nil)
    })

    // Step 7: Watch deployment
    var health DeploymentHealth
    if err := workflow.ExecuteActivity(ctx, WatchDeployment, argoApp).Get(ctx, &health); err != nil {
        // DO NOT auto-rollback healthy-then-degraded — mark for manual intervention
        persistState(ctx, req.DeploymentID, StateFailed, err)
        return result, err
    }

    // Step 8: Persist state
    persistState(ctx, req.DeploymentID, StateCompleted, nil)
    return result, nil
}
```

### G.5 Search Attributes

| Attribute | Type | Purpose |
|-----------|------|---------|
| `DeploymentId` | Keyword | Primary lookup |
| `Application` | Keyword | Filter by app |
| `Environment` | Keyword | Filter by environment |
| `Cluster` | Keyword | Filter by cluster |
| `GitSha` | Keyword | Trace to source commit |
| `GitRef` | Keyword | Trace to branch/tag |
| `GithubRunId` | Keyword | Correlate with CI |
| `DeploymentStatus` | Keyword | Current state machine state |
| `Actor` | Keyword | Who triggered |
| `Team` | Keyword | Team ownership |
| `StartedAt` | Datetime | Time-range queries |
| `ChartVersion` | Keyword | Deployed chart version |

### G.6 Workflow Queries

| Query | Returns |
|-------|---------|
| `getCurrentState` | Current state machine state + timestamp |
| `getDeploymentDetails` | Full deployment metadata and progress |
| `getCompensationHistory` | Any compensations executed and their outcomes |

### G.7 Cancellation Handling

- Temporal's built-in cancellation propagates to running activities
- On cancellation: run compensations for completed side effects
- Activities use `workflow.Context` to detect cancellation and clean up
- Cancelled state persisted to DocumentDB

---

## Section H — Helm SDK Design

### H.1 Chart Retrieval

```
1. Resolve chart source repository: github.com/{owner}/{repo}
2. Query GitHub API: GET /repos/{owner}/{repo}/tags
3. Apply deterministic tag selection (see H.2)
4. Download chart archive: GET /repos/{owner}/{repo}/tarball/{tag}
   OR download from GitHub Release asset if charts are published as release assets
5. Extract chart directory from archive
6. Validate chart structure: Chart.yaml, templates/, values.yaml exist
```

### H.2 Deterministic Tag Selection Rule

**Rule: Highest SemVer tag matching `v{major}.{minor}.{patch}` pattern, with optional pre-release filter.**

Algorithm:
1. Fetch all tags from GitHub API
2. Filter tags matching regex `^v?\d+\.\d+\.\d+(-[a-zA-Z0-9.]+)?$`
3. Parse each as SemVer
4. If `chart.versionConstraint` is provided in request (e.g., `~1.5.0`, `^2.0.0`), apply constraint filter
5. Exclude pre-release tags unless explicitly requested
6. Sort descending by SemVer precedence
7. Select first (highest) — this is deterministic because SemVer defines total ordering

**Why this is deterministic:**
- SemVer has a well-defined comparison algorithm (semver.org §11)
- Given the same set of tags, the same version is always selected
- No reliance on creation time, commit date, or API ordering

**Edge cases:**
- No tags match → fail with `ChartNotFoundError` (non-retryable)
- Multiple tags resolve to same SemVer (e.g., `v1.0.0` and `1.0.0`) → normalize before comparison
- Version constraint excludes all tags → fail with `ChartVersionConstraintError`
- Pre-release tags (e.g., `v1.0.0-rc.1`) → excluded by default, included only if `chart.allowPrerelease: true`

**How to avoid deploying wrong chart version:**
- The resolved version is recorded in deployment metadata and persisted
- Chart.yaml version in the composed chart is mutated to deployment-specific version
- OCI artifact tag includes the source chart version
- Audit trail in DocumentDB records exact resolved tag

### H.3 Chart Loading with Helm SDK

```go
import (
    "helm.sh/helm/v3/pkg/chart/loader"
    "helm.sh/helm/v3/pkg/chart"
    "helm.sh/helm/v3/pkg/chartutil"
    "helm.sh/helm/v3/pkg/action"
)

// Load chart from extracted directory
chart, err := loader.Load(chartPath)

// Or load from .tgz archive
chart, err := loader.LoadArchive(reader)
```

### H.4 Dependency Handling

**Policy: Dependencies must be pre-locked. The orchestrator does NOT run `helm dependency update`.**

Rationale:
- Running `helm dependency update` during orchestration introduces non-determinism
- Dependencies could change between runs if upstream repos are updated
- Network failures during dependency download would fail the deployment
- Pre-locked dependencies guarantee reproducibility

Implementation:
1. Check for `Chart.lock` file — if missing, fail with `ChartDependencyNotLockedError` (non-retryable)
2. Check for `charts/` directory with all dependencies present
3. Validate each dependency against `Chart.lock` entries:
   - Name matches
   - Version matches
   - Repository matches
4. If any dependency is missing from `charts/` → fail with `ChartDependencyMissingError`
5. If `Chart.lock` and `Chart.yaml` dependencies diverge → fail with `ChartDependencyDriftError`

**Integrity validation:**
- Each dependency chart in `charts/` is loaded and its `Chart.yaml` version is compared against `Chart.lock`
- No integrity hash validation in v1 (Helm doesn't store hashes in Chart.lock for all sources)

### H.5 Values Merge Strategy

```go
// Priority order (highest wins):
// 1. Platform-injected metadata (non-overridable)
// 2. Request values.yaml (from GitHub Actions)
// 3. Chart default values.yaml

// Platform-injected values (always set, cannot be overridden by request):
platformValues := map[string]interface{}{
    "image": map[string]interface{}{
        "repository": req.Image.Repository,
        "tag":        req.Image.Tag,
        "digest":     req.Image.Digest,
    },
    "deploymentMetadata": map[string]interface{}{
        "deploymentId":   meta.DeploymentID,
        "componentId":    meta.ComponentID,
        "gitSha":         req.Source.GitSha,
        "environment":    req.Target.Environment,
        "deployedAt":     meta.Timestamp,
        "deployedBy":     req.Source.Actor,
        "chartVersion":   meta.ChartVersion,
        "workflowRunId":  req.Source.GithubRunId,
    },
}

// Merge: chart defaults ← request values ← platform values
finalValues := chartutil.CoalesceTables(
    chartutil.CoalesceTables(chart.Values, requestValues),
    platformValues,
)
```

### H.6 Chart Version Mutation

```go
// Original Chart.yaml might have version: 1.5.2
// We mutate to deployment-specific version:

chart.Metadata.Version = meta.DeploymentVersion
// e.g., "1.5.2-20260412103000-abc123d"
// Format: {sourceChartVersion}-{timestamp}-{shortSha}

chart.Metadata.AppVersion = req.Source.GitSha
// Full git SHA as appVersion — traces chart to exact source

// Add deployment annotations to Chart.yaml
chart.Metadata.Annotations["platform.example.com/deployment-id"] = meta.DeploymentID
chart.Metadata.Annotations["platform.example.com/source-chart-version"] = resolvedTag
chart.Metadata.Annotations["platform.example.com/environment"] = req.Target.Environment
```

**Collision avoidance:** The timestamp component (YYYYMMDDHHmmss) + short SHA makes versions unique even for rapid re-deploys of the same commit. If the same commit is deployed twice in the same second, the idempotency key prevents a second workflow.

### H.7 Packaging and Publishing

```go
// Package chart to .tgz
packagePath, err := chartutil.Save(chart, outputDir)

// Push to OCI registry
// Using oras-go or helm's internal registry client
ref := fmt.Sprintf("oci://%s/%s/charts/%s:%s",
    registry, req.Application.ID, chart.Name(), meta.DeploymentVersion)

client, _ := registry.NewClient()
client.Push(chartBytes, ref)

// Verify: pull manifest back and compare digest
manifest, _ := client.GetManifest(ref)
if manifest.Digest != expectedDigest {
    return errors.New("artifact verification failed: digest mismatch")
}
```

---

## Section I — Argo CD Integration Design

### I.1 Application Spec Strategy

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: payment-service-production-payments    # {app}-{env}-{namespace}
  namespace: argocd
  labels:
    platform.example.com/application: payment-service
    platform.example.com/environment: production
    platform.example.com/deployment-id: dep-20260412-payment-service-prod-abc123d
    platform.example.com/managed-by: platform-orchestrator
  annotations:
    platform.example.com/git-sha: abc123def456
    platform.example.com/chart-version: 1.5.2-20260412103000-abc123d
    platform.example.com/deployed-by: johndoe
  finalizers: []  # No finalizers — orchestrator manages lifecycle
spec:
  project: payments-production    # Must match pre-existing AppProject
  source:
    repoURL: oci://myregistry.azurecr.io/payment-service/charts
    chart: payment-service
    targetRevision: 1.5.2-20260412103000-abc123d
  destination:
    server: https://aks-prod-eastus2.example.com
    namespace: payments
  syncPolicy:
    automated:
      prune: true
      selfHeal: false    # Orchestrator manages sync, not Argo auto-heal
    syncOptions:
      - CreateNamespace=false
      - PruneLast=true
      - ApplyOutOfSyncOnly=true
    retry:
      limit: 2
      backoff:
        duration: 10s
        factor: 2
        maxDuration: 60s
```

**Key decisions:**
- `selfHeal: false` — the orchestrator triggers syncs explicitly; self-heal would mask drift
- `prune: true` — resources removed from chart are cleaned up
- `CreateNamespace=false` — namespaces must be pre-provisioned (security boundary)
- No finalizers — the orchestrator handles deletion, not Argo's garbage collector

### I.2 AppProject Validation

Before publishing the artifact or creating the Application, the orchestrator validates against the target AppProject:

```go
func validateAppProject(ctx context.Context, argoClient ArgoClient, req DeploymentRequest) error {
    project, err := argoClient.GetProject(ctx, req.Target.AppProject)
    if err != nil {
        return fmt.Errorf("AppProject %q not found: %w", req.Target.AppProject, err)
    }

    // 1. Validate source repository is allowed
    ociSource := fmt.Sprintf("oci://%s/%s/charts", registry, req.Application.ID)
    if !project.Spec.SourceReposAllowed(ociSource) {
        return &AppProjectMismatchError{
            Field: "sourceRepos",
            Value: ociSource,
            Allowed: project.Spec.SourceRepos,
        }
    }

    // 2. Validate destination cluster
    if !project.Spec.DestinationAllowed(req.Target.Cluster, req.Target.Namespace) {
        return &AppProjectMismatchError{
            Field: "destinations",
            Value: fmt.Sprintf("%s/%s", req.Target.Cluster, req.Target.Namespace),
        }
    }

    // 3. Validate namespace
    // Already covered by destination check above, but explicit for clarity
    for _, dest := range project.Spec.Destinations {
        if dest.Server == req.Target.Cluster {
            if dest.Namespace != "*" && dest.Namespace != req.Target.Namespace {
                return &AppProjectMismatchError{Field: "namespace"}
            }
        }
    }

    return nil
}
```

**Fail-fast principle:** AppProject validation happens in the `ValidateDeployment` activity (Step 5), before any chart resolution or artifact publication. This prevents wasted work.

### I.3 Health Evaluation — Precise Semantics

| Sync Status | Health Status | Interpretation | Action |
|-------------|--------------|----------------|--------|
| Synced | Healthy | **SUCCESS** | Complete workflow |
| Synced | Progressing | Resources starting up | Continue polling |
| Synced | Degraded | **TERMINAL FAILURE** — pods crashing, readiness failing | Stop polling, mark failed |
| Synced | Suspended | HPA or manual suspension | Continue polling up to timeout |
| Synced | Missing | Resources not found after sync | **TERMINAL FAILURE** |
| Synced | Unknown | Health check not configured or failing | Wait 2 min, then **TERMINAL FAILURE** |
| OutOfSync | Any | Sync not complete | Continue polling |
| Unknown | Any | Argo CD unable to determine | Wait 2 min, then **TERMINAL FAILURE** |

**Success definition:** `sync.status == "Synced" AND health.status == "Healthy"`

**Timeout rules:**
- **Sync initiation timeout:** 60s — if sync doesn't start within 60s of trigger, fail
- **Sync completion timeout:** 300s (5 min) — if OutOfSync persists for 5 min, fail
- **Health convergence timeout:** 600s (10 min) — total time from sync trigger to Healthy
- **Post-sync Progressing grace:** 300s — allow 5 min for pods to start after sync completes
- **Unknown state grace:** 120s — allow 2 min before treating Unknown as failure

**Polling strategy:**
- Poll interval: 10s
- No exponential backoff for polling (fixed interval for predictable convergence detection)
- Log each poll result at DEBUG level with full status snapshot

### I.4 Argo CD API Authentication

The orchestrator authenticates to Argo CD using a **dedicated service account token** (not user credentials):

1. Create an Argo CD local account `platform-orchestrator` with `apiKey` capability
2. Generate a long-lived API token for this account
3. Store token in Azure Key Vault
4. Orchestrator retrieves token at startup and caches it
5. Token has RBAC scoped to: create/update/delete Applications, read Projects, sync Applications
6. Token rotation: automated via Key Vault rotation policy, orchestrator re-fetches periodically

---

## Section J — State Machine Design

### J.1 States and Transitions

```
                    ┌─────────┐
                    │RECEIVED │
                    └────┬────┘
                         │
                    ┌────▼────┐
               ┌────│VALIDATING│────┐
               │    └────┬────┘    │
               │         │         │
          ┌────▼───┐     │    ┌────▼────┐
          │REJECTED│     │    │  FAILED │
          └────────┘     │    └─────────┘
               ▲         │
               │    ┌────▼──────────┐
               │    │METADATA_      │
               │    │GENERATED      │
               │    └────┬──────────┘
               │         │
               │    ┌────▼──────────┐
               │    │CHART_RESOLVED │
               │    └────┬──────────┘
               │         │
               │    ┌────▼──────────┐
               │    │CHART_COMPOSED │
               │    └────┬──────────┘
               │         │
               │    ┌────▼──────────────┐
               │    │ARTIFACT_PUBLISHED │
               │    └────┬──────────────┘
               │         │
               │    ┌────▼──────────────┐
               │    │ARGO_APP_CREATED   │
               │    └────┬──────────────┘
               │         │
               │    ┌────▼────┐
               │    │SYNCING  │
               │    └────┬────┘
               │         │
               │    ┌────▼────┐     ┌─────────┐
               │    │HEALTHY  │     │DEGRADED │
               │    └────┬────┘     └────┬────┘
               │         │              │
               │    ┌────▼────┐    ┌────▼──────┐
               │    │COMPLETED│    │ROLLED_BACK│
               │    └─────────┘    └───────────┘
```

### J.2 State Definitions

| State | Terminal? | Description |
|-------|-----------|-------------|
| RECEIVED | No | Request accepted by API, Temporal workflow started |
| VALIDATING | No | Policy and AppProject validation in progress |
| REJECTED | **Yes** | Validation failed — deployment not allowed |
| METADATA_GENERATED | No | Component ID, versions, labels generated |
| CHART_RESOLVED | No | Helm chart tag resolved and archive downloaded |
| CHART_COMPOSED | No | Final chart packaged with enriched values |
| ARTIFACT_PUBLISHED | No | OCI artifact pushed to registry |
| ARGO_APP_CREATED | No | Argo CD Application created and sync triggered |
| SYNCING | No | Argo CD sync in progress |
| HEALTHY | No | Argo CD reports Synced + Healthy |
| DEGRADED | **Yes** | Deployment degraded after sync |
| FAILED | **Yes** | Unrecoverable failure at any step |
| ROLLED_BACK | **Yes** | Compensations executed after failure |
| COMPLETED | **Yes** | Deployment succeeded and state persisted |

### J.3 Allowed Transitions

```
RECEIVED         → VALIDATING
VALIDATING       → REJECTED | METADATA_GENERATED | FAILED
METADATA_GENERATED → CHART_RESOLVED | FAILED
CHART_RESOLVED   → CHART_COMPOSED | FAILED
CHART_COMPOSED   → ARTIFACT_PUBLISHED | FAILED
ARTIFACT_PUBLISHED → ARGO_APP_CREATED | FAILED
ARGO_APP_CREATED → SYNCING | FAILED | ROLLED_BACK
SYNCING          → HEALTHY | DEGRADED | FAILED | ROLLED_BACK
HEALTHY          → COMPLETED | FAILED
DEGRADED         → ROLLED_BACK | FAILED
```

---

## Section K — Azure DocumentDB Data Model

### K.1 Collections

| Collection | Purpose | Partition Key |
|-----------|---------|---------------|
| `deployments` | Current deployment state | `/applicationId` |
| `deployment-events` | Event history / audit trail | `/deploymentId` |

**Partition key rationale:**
- `deployments` partitioned by `applicationId` — most queries are "show me deployments for app X"
- `deployment-events` partitioned by `deploymentId` — events for a single deployment are always queried together

### K.2 Deployment Document (Current State)

```json
{
  "id": "dep-20260412-payment-service-prod-abc123d",
  "applicationId": "payment-service",
  "environment": "production",
  "cluster": "aks-prod-eastus2",
  "namespace": "payments",
  "team": "payments",
  "status": "COMPLETED",
  "image": {
    "repository": "myregistry.azurecr.io/payment-service",
    "tag": "v1.5.2",
    "digest": "sha256:a1b2c3d4e5f6..."
  },
  "chart": {
    "sourceRepository": "github.com/myorg/helm-charts",
    "sourceName": "payment-service",
    "resolvedVersion": "1.5.2",
    "deploymentVersion": "1.5.2-20260412103000-abc123d",
    "ociReference": "oci://myregistry.azurecr.io/payment-service/charts/payment-service:1.5.2-20260412103000-abc123d",
    "ociDigest": "sha256:f1e2d3c4b5a6..."
  },
  "source": {
    "gitSha": "abc123def456789",
    "gitRef": "refs/tags/v1.5.2",
    "githubRunId": "12345678",
    "githubRunAttempt": 1,
    "workflowName": "deploy-production",
    "actor": "johndoe",
    "repositoryFullName": "myorg/payment-service"
  },
  "argocd": {
    "applicationName": "payment-service-production-payments",
    "syncStatus": "Synced",
    "healthStatus": "Healthy",
    "lastSyncedAt": "2026-04-12T10:33:40Z"
  },
  "temporal": {
    "workflowId": "deploy:payment-service:production:abc123d",
    "runId": "uuid-run-id",
    "taskQueue": "deployment-workers"
  },
  "correlationId": "deploy-abc123-1712880000",
  "startedAt": "2026-04-12T10:30:00Z",
  "completedAt": "2026-04-12T10:33:46Z",
  "durationMs": 226000,
  "createdAt": "2026-04-12T10:30:00Z",
  "updatedAt": "2026-04-12T10:33:46Z",
  "_etag": "\"00000000-0000-0000-0000-000000000000\""
}
```

### K.3 Event Document (Audit Trail)

```json
{
  "id": "evt-20260412-103000-RECEIVED",
  "deploymentId": "dep-20260412-payment-service-prod-abc123d",
  "applicationId": "payment-service",
  "state": "RECEIVED",
  "previousState": null,
  "timestamp": "2026-04-12T10:30:00Z",
  "details": {
    "message": "Deployment request received from GitHub Actions",
    "actor": "johndoe",
    "githubRunId": "12345678"
  },
  "traceId": "abc123def456",
  "spanId": "789ghi",
  "_ts": 1712914200
}
```

### K.4 Indexing Strategy

```json
{
  "indexingPolicy": {
    "automatic": true,
    "includedPaths": [
      { "path": "/applicationId/?" },
      { "path": "/environment/?" },
      { "path": "/status/?" },
      { "path": "/source/actor/?" },
      { "path": "/team/?" },
      { "path": "/startedAt/?" },
      { "path": "/completedAt/?" }
    ],
    "excludedPaths": [
      { "path": "/chart/*" },
      { "path": "/source/gitSha/?" },
      { "path": "/argocd/*" },
      { "path": "/temporal/*" },
      { "path": "/*" }
    ],
    "compositeIndexes": [
      [
        { "path": "/applicationId", "order": "ascending" },
        { "path": "/startedAt", "order": "descending" }
      ],
      [
        { "path": "/environment", "order": "ascending" },
        { "path": "/status", "order": "ascending" },
        { "path": "/startedAt", "order": "descending" }
      ]
    ]
  }
}
```

### K.5 Query Examples

```sql
-- Latest deployment for an application in an environment
SELECT TOP 1 * FROM c
WHERE c.applicationId = 'payment-service'
  AND c.environment = 'production'
ORDER BY c.startedAt DESC

-- All failed deployments in last 24 hours
SELECT * FROM c
WHERE c.status = 'FAILED'
  AND c.startedAt > '2026-04-11T10:30:00Z'
ORDER BY c.startedAt DESC

-- Deployment timeline for audit
SELECT * FROM c
WHERE c.deploymentId = 'dep-20260412-payment-service-prod-abc123d'
ORDER BY c.timestamp ASC
-- (run against deployment-events collection)

-- Deployments by actor
SELECT * FROM c
WHERE c.source.actor = 'johndoe'
  AND c.startedAt > '2026-04-01T00:00:00Z'
ORDER BY c.startedAt DESC
```

### K.6 Identity and RBAC

- **Access:** Azure Managed Identity (workload identity on AKS)
- **Role:** `Cosmos DB Built-in Data Contributor` scoped to the specific database
- **No connection strings or keys in config** — Managed Identity only
- **Least privilege:** The service identity can read/write only the `deployments` and `deployment-events` collections
- **No admin access** — schema changes managed via IaC (Terraform/Bicep)

---

## Section L — Security Model

### L.1 GitHub Actions → Orchestrator Authentication

**Mechanism: GitHub OIDC Token Federation**

```yaml
# GitHub Actions workflow step
- name: Deploy via Platform Orchestrator
  env:
    DEPLOYMENT_PAYLOAD: ${{ steps.prepare.outputs.payload }}
  run: |
    TOKEN=$(curl -s -H "Authorization: bearer $ACTIONS_ID_TOKEN_REQUEST_TOKEN" \
      "$ACTIONS_ID_TOKEN_REQUEST_URL&audience=platform-orchestrator.example.com" \
      | jq -r '.value')
    
    curl -X POST https://platform-orchestrator.example.com/api/v1/deployments \
      -H "Authorization: Bearer $TOKEN" \
      -H "Content-Type: application/json" \
      -H "Idempotency-Key: ${{ github.repository }}-${{ github.sha }}-${{ github.run_id }}-${{ github.run_attempt }}" \
      -d "$DEPLOYMENT_PAYLOAD"
```

**OIDC Token Claims Validated:**

| Claim | Validation |
|-------|-----------|
| `iss` | Must be `https://token.actions.githubusercontent.com` |
| `aud` | Must match configured audience |
| `exp` | Must not be expired |
| `repository` | Must be in the allowed repositories list |
| `ref` | Must match environment-specific branch rules |
| `job_workflow_ref` | Must match allowed workflow file patterns |
| `repository_owner` | Must match configured organization |
| `runner_environment` | Must be `github-hosted` or allowed self-hosted |

**Replay protection:**
- OIDC tokens have short TTL (< 15 minutes)
- `Idempotency-Key` prevents duplicate processing
- `jti` (JWT ID) claim can be tracked if stricter replay protection needed

**No secrets required:**
- No API keys, no shared secrets, no service account tokens stored in GitHub
- GitHub's OIDC provider handles identity — the orchestrator validates JWTs directly

### L.2 Orchestrator → Argo CD

- Argo CD API token stored in Azure Key Vault
- Retrieved at startup, cached in memory, refreshed periodically
- Token scoped to `platform-orchestrator` Argo CD account
- RBAC: create/update/delete Applications, read Projects, trigger sync
- mTLS between orchestrator and Argo CD if running in same cluster

### L.3 Orchestrator → GitHub API

- GitHub App installation token (not PAT)
- App scoped to: read repository content, read tags/releases
- Token refreshed automatically (GitHub App tokens expire in 1 hour)
- App installation ID stored in config, private key in Azure Key Vault

### L.4 Orchestrator → OCI Registry (ACR)

- Azure Managed Identity (workload identity)
- Role: `AcrPush` on the specific registry
- No docker config or credentials in pod

### L.5 Orchestrator → DocumentDB

- Azure Managed Identity (workload identity)
- Role: `Cosmos DB Built-in Data Contributor` scoped to database
- No connection strings

### L.6 Orchestrator → Temporal

- Temporal Cloud: mTLS with client certificates stored in Azure Key Vault
- Self-hosted: Kubernetes service account + network policy
- Namespace scoped: orchestrator can only interact with its own Temporal namespace

### L.7 Secret Management Summary

| Secret | Storage | Rotation |
|--------|---------|----------|
| Argo CD API token | Azure Key Vault | 90-day rotation |
| GitHub App private key | Azure Key Vault | Annual rotation |
| Temporal mTLS cert | Azure Key Vault | 90-day rotation |
| OCI registry credentials | None (Managed Identity) | N/A |
| DocumentDB credentials | None (Managed Identity) | N/A |
| GitHub OIDC validation | None (public JWKS) | N/A |

---

## Section M — Observability Model

### M.1 OpenTelemetry Traces

**Trace structure:** One trace per deployment, with spans for each activity.

```
Trace: deploy:payment-service:production:abc123d
├── Span: api.handleDeployment (API server)
│   ├── Span: auth.validateOIDC
│   └── Span: temporal.startWorkflow
├── Span: workflow.DeploymentWorkflow (Temporal)
│   ├── Span: activity.ValidateDeployment
│   │   ├── Span: policy.evaluateRules
│   │   └── Span: argocd.validateAppProject
│   ├── Span: activity.GenerateMetadata
│   ├── Span: activity.ResolveChart
│   │   ├── Span: github.listTags
│   │   └── Span: github.downloadArchive
│   ├── Span: activity.ComposeChart
│   │   ├── Span: helm.loadChart
│   │   ├── Span: helm.validateDependencies
│   │   ├── Span: helm.mergeValues
│   │   └── Span: helm.packageChart
│   ├── Span: activity.PublishArtifact
│   │   ├── Span: oci.pushChart
│   │   └── Span: oci.verifyDigest
│   ├── Span: activity.CreateArgoApp
│   │   ├── Span: argocd.createApplication
│   │   └── Span: argocd.triggerSync
│   ├── Span: activity.WatchDeployment
│   │   └── Span: argocd.pollStatus (repeated)
│   └── Span: activity.PersistState
│       └── Span: documentdb.upsertDeployment
```

**Span attributes (common):**
- `deployment.id`
- `deployment.application`
- `deployment.environment`
- `deployment.cluster`
- `deployment.git_sha`
- `deployment.github_run_id`
- `deployment.temporal_workflow_id`

### M.2 Metrics

| Metric | Type | Labels | Purpose |
|--------|------|--------|---------|
| `deployment_requests_total` | Counter | app, env, status_code | API request rate |
| `deployment_duration_seconds` | Histogram | app, env, final_status | End-to-end duration |
| `deployment_status_transitions_total` | Counter | app, env, from_state, to_state | State machine transitions |
| `deployment_active_count` | Gauge | env | Currently in-flight deployments |
| `chart_resolution_duration_seconds` | Histogram | chart_repo | GitHub API latency |
| `artifact_publish_duration_seconds` | Histogram | registry | OCI push latency |
| `argocd_sync_duration_seconds` | Histogram | app, env | Time from sync trigger to Healthy |
| `argocd_poll_count` | Histogram | app, env | Number of polls per deployment |
| `documentdb_operation_duration_seconds` | Histogram | collection, operation | Database latency |
| `oidc_validation_duration_seconds` | Histogram | result | Auth validation latency |
| `compensation_executions_total` | Counter | app, env, compensation_type | Rollback frequency |

### M.3 Structured Logging

**Library: `log/slog` (Go standard library)**

Rationale:
- Part of Go standard library since Go 1.21 — no external dependency
- Structured by default (JSON handler)
- OTel bridge available via `go.opentelemetry.io/contrib/bridges/otelslog` but production maturity is limited
- **Recommendation:** Use `slog` with JSON handler writing to stdout; correlate with traces via explicit trace/span ID fields rather than OTel log bridge

**Log enrichment middleware:**
```go
func enrichedLogger(ctx context.Context, base *slog.Logger) *slog.Logger {
    span := trace.SpanFromContext(ctx)
    sc := span.SpanContext()

    return base.With(
        slog.String("trace_id", sc.TraceID().String()),
        slog.String("span_id", sc.SpanID().String()),
        slog.String("deployment_id", deploymentIDFromContext(ctx)),
        slog.String("temporal_workflow_id", workflowIDFromContext(ctx)),
        slog.String("application", applicationFromContext(ctx)),
        slog.String("environment", environmentFromContext(ctx)),
    )
}
```

**OTel Logs stance (realistic):**
- OTel logs in Go are **not yet production-stable** for most organizations
- The `otelslog` bridge exists but adds complexity and dependency risk
- **v1 recommendation:** Use `slog` → stdout → collected by infrastructure (Fluent Bit, Vector, etc.) → correlated in observability backend via `trace_id` field
- **v2 consideration:** Evaluate OTel log bridge maturity for direct OTLP log export

### M.4 Correlation Strategy

All three signals (traces, metrics, logs) share these identifiers:

| Identifier | In Traces | In Metrics | In Logs |
|-----------|----------|-----------|---------|
| `trace_id` | Native | Exemplar | Field |
| `deployment_id` | Attribute | Label | Field |
| `temporal_workflow_id` | Attribute | — | Field |
| `github_run_id` | Attribute | — | Field |
| `application` | Attribute | Label | Field |
| `environment` | Attribute | Label | Field |

### M.5 Dashboards

1. **Deployment Overview** — success/failure rate, active deployments, average duration by environment
2. **Deployment Detail** — timeline view of a single deployment through state machine
3. **Error Analysis** — failure reasons by type, compensation frequency, stuck deployments
4. **External System Health** — GitHub API, OCI registry, Argo CD, DocumentDB latency and error rates
5. **Temporal Health** — workflow backlogs, activity durations, retry rates

### M.6 Alerting

| Alert | Condition | Severity |
|-------|-----------|----------|
| High deployment failure rate | >20% failures in 15min window | Critical |
| Deployment stuck | Any deployment in non-terminal state >20 min | Warning |
| State persistence failure | `deployment_state_persist_failure` metric > 0 | Critical |
| External service degradation | Error rate to GitHub/ACR/Argo >5% in 5 min | Warning |
| Temporal workflow backlog | Pending workflows > 50 for > 5 min | Warning |

---

## Section N — Failure Modes and Mitigations

| # | Failure Mode | Impact | Mitigation |
|---|-------------|--------|------------|
| F1 | GitHub OIDC provider outage | No new deployments can be authenticated | Cache JWKS with 24h fallback TTL; alert if JWKS refresh fails |
| F2 | Temporal server unavailable | No new workflows, in-flight activities stall | Temporal workers auto-reconnect; API returns 503; existing workflows resume when Temporal recovers |
| F3 | OCI registry unavailable | Cannot publish artifacts | Retry 3x with backoff; fail deployment cleanly; no side effects to compensate |
| F4 | Argo CD API unavailable | Cannot create apps or monitor health | Retry 2x; fail deployment; artifact already published is harmless |
| F5 | DocumentDB throttled | State persistence delayed | Retry 5x with exponential backoff; Temporal retry handles this; alert on sustained throttling |
| F6 | GitHub API rate limited | Cannot resolve chart tags | Retry with backoff respecting `X-RateLimit-Reset` header; use conditional requests (ETags) |
| F7 | Chart dependency missing | Cannot compose chart | Fail fast with clear error; non-retryable; chart owner must fix |
| F8 | Argo CD sync stuck in Progressing | Deployment hangs | 10-minute timeout; transition to FAILED; alert |
| F9 | Temporal worker crash mid-activity | Activity interrupted | Temporal retries activity on another worker; heartbeat timeout detects dead workers |
| F10 | Concurrent deployments for same app/env | Race condition | Temporal workflow ID includes app+env — second attempt gets `WorkflowExecutionAlreadyStarted` and returns existing deployment |
| F11 | OCI push succeeds but Argo CD cannot pull | Sync failure | Validate registry permissions pre-flight; Argo CD sync retry handles transient pull failures |
| F12 | Clock skew on OIDC token validation | Valid tokens rejected | Use NTP-synced infrastructure; allow 30s clock skew tolerance in JWT validation |
| F13 | Network partition between orchestrator and Argo CD | Health monitoring fails | WatchDeployment activity has heartbeat; Temporal detects timeout; deployment marked FAILED |
| F14 | Malformed values.yaml from caller | Chart rendering fails | Validate values schema before chart composition (use JSON Schema if chart provides it) |
| F15 | Helm SDK panic | Worker crashes | Recover panics in activity wrapper; Temporal retries on different worker; log stack trace |

---

## Section O — Build Phase

### O.1 Go Module and Package Layout

```
platform-orchestrator/
├── cmd/
│   ├── server/          # API server entrypoint
│   │   └── main.go
│   └── worker/          # Temporal worker entrypoint
│       └── main.go
├── internal/
│   ├── api/             # HTTP API layer
│   │   ├── handler.go       # Deployment handler
│   │   ├── middleware.go     # Auth, logging, tracing middleware
│   │   ├── request.go       # Request types and validation
│   │   ├── response.go      # Response types
│   │   └── router.go        # Route registration
│   ├── auth/            # Authentication
│   │   ├── oidc.go          # GitHub OIDC validator
│   │   └── jwks.go          # JWKS cache
│   ├── policy/          # Policy engine
│   │   ├── engine.go        # Policy evaluator
│   │   ├── rules.go         # Rule definitions
│   │   └── config.go        # Policy configuration
│   ├── workflow/        # Temporal workflows
│   │   ├── deployment.go    # DeploymentWorkflow
│   │   ├── compensation.go  # Compensation logic
│   │   └── queries.go       # Workflow query handlers
│   ├── activity/        # Temporal activities
│   │   ├── validate.go      # ValidateDeployment
│   │   ├── metadata.go      # GenerateMetadata
│   │   ├── chart.go         # ResolveChart, ComposeChart
│   │   ├── publish.go       # PublishArtifact
│   │   ├── argocd.go        # CreateArgoApp, WatchDeployment
│   │   ├── state.go         # PersistState
│   │   └── compensate.go    # Compensation activities
│   ├── helm/            # Helm SDK wrapper
│   │   ├── resolver.go      # Tag resolution and download
│   │   ├── composer.go      # Chart loading, values merge, packaging
│   │   ├── dependency.go    # Dependency validation
│   │   └── version.go       # Version computation
│   ├── oci/             # OCI registry client
│   │   ├── client.go        # Push, pull, verify
│   │   └── reference.go     # OCI reference parsing
│   ├── argocd/          # Argo CD API client
│   │   ├── client.go        # HTTP client
│   │   ├── application.go   # Application CRUD
│   │   ├── project.go       # AppProject queries
│   │   └── health.go        # Health evaluation
│   ├── github/          # GitHub API client
│   │   ├── client.go        # HTTP client with auth
│   │   ├── tags.go          # Tag listing and SemVer resolution
│   │   └── archive.go       # Archive download
│   ├── state/           # State management
│   │   ├── machine.go       # State machine definition
│   │   ├── repository.go    # DocumentDB repository
│   │   └── events.go        # Event history
│   ├── documentdb/      # Azure DocumentDB client
│   │   ├── client.go        # Connection and CRUD
│   │   └── query.go         # Query builders
│   ├── observability/   # Telemetry setup
│   │   ├── traces.go        # OTel trace provider
│   │   ├── metrics.go       # OTel meter provider
│   │   ├── logging.go       # slog setup and enrichment
│   │   └── middleware.go    # HTTP middleware for OTel
│   ├── config/          # Configuration
│   │   ├── config.go        # Config struct and loading
│   │   └── validate.go      # Config validation
│   └── domain/          # Domain types
│       ├── deployment.go    # DeploymentRequest, DeploymentResult, etc.
│       ├── chart.go         # ResolvedChart, ComposedChart, etc.
│       ├── metadata.go      # DeploymentMetadata
│       └── errors.go        # Domain error types
├── pkg/                 # Public packages (if any)
│   └── api/
│       └── v1/
│           └── types.go     # API types for client consumers
├── deploy/
│   ├── kubernetes/      # K8s manifests
│   │   ├── server.yaml
│   │   ├── worker.yaml
│   │   └── kustomization.yaml
│   └── terraform/       # Infrastructure as Code
│       ├── documentdb.tf
│       ├── acr.tf
│       └── identity.tf
├── policies/            # Deployment policy files
│   └── default.yaml
├── go.mod
├── go.sum
├── Dockerfile.server
├── Dockerfile.worker
├── Makefile
└── .github/
    └── workflows/
        └── ci.yaml
```

### O.2 Key Interfaces

```go
// internal/domain/deployment.go

type DeploymentRequest struct {
    Application ApplicationInfo `json:"application"`
    Image       ImageInfo       `json:"image"`
    Chart       ChartInfo       `json:"chart"`
    Target      TargetInfo      `json:"target"`
    Values      map[string]any  `json:"values"`
    Source      SourceInfo      `json:"source"`
    CorrelationID string        `json:"correlationId"`
}

type DeploymentResult struct {
    DeploymentID string         `json:"deploymentId"`
    Status       DeploymentState `json:"status"`
    ArgoAppName  string         `json:"argoAppName,omitempty"`
    OCIReference string         `json:"ociReference,omitempty"`
    Error        string         `json:"error,omitempty"`
}
```

```go
// internal/helm/resolver.go

type ChartResolver interface {
    ResolveVersion(ctx context.Context, repo, chartName, constraint string) (ResolvedChart, error)
    DownloadChart(ctx context.Context, repo, tag string) ([]byte, error)
}
```

```go
// internal/helm/composer.go

type ChartComposer interface {
    LoadChart(ctx context.Context, archive []byte) (*chart.Chart, error)
    ValidateDependencies(ctx context.Context, ch *chart.Chart) error
    MergeValues(ch *chart.Chart, requestValues, platformValues map[string]any) (map[string]any, error)
    MutateVersion(ch *chart.Chart, meta DeploymentMetadata) error
    Package(ch *chart.Chart, values map[string]any) ([]byte, error)
}
```

```go
// internal/oci/client.go

type ArtifactPublisher interface {
    Push(ctx context.Context, chartBytes []byte, reference string) (digest string, err error)
    Verify(ctx context.Context, reference, expectedDigest string) error
}
```

```go
// internal/argocd/client.go

type ArgoClient interface {
    GetProject(ctx context.Context, name string) (*AppProject, error)
    CreateApplication(ctx context.Context, app *Application) (*Application, error)
    UpdateApplication(ctx context.Context, app *Application) (*Application, error)
    GetApplication(ctx context.Context, name string) (*Application, error)
    DeleteApplication(ctx context.Context, name string) error
    SyncApplication(ctx context.Context, name string) error
}
```

```go
// internal/state/repository.go

type StateRepository interface {
    GetDeployment(ctx context.Context, id string) (*DeploymentDocument, error)
    UpsertDeployment(ctx context.Context, doc *DeploymentDocument) error
    AppendEvent(ctx context.Context, event *DeploymentEvent) error
    ListDeployments(ctx context.Context, filter DeploymentFilter) ([]DeploymentDocument, error)
}
```

```go
// internal/policy/engine.go

type PolicyEngine interface {
    Evaluate(ctx context.Context, req DeploymentRequest) (PolicyDecision, error)
}

type PolicyDecision struct {
    Allowed bool
    Reason  string
    Rules   []RuleResult
}
```

### O.3 Temporal Worker Structure

```go
// cmd/worker/main.go

func main() {
    cfg := config.Load()
    tp := observability.InitTraces(cfg.OTel)
    mp := observability.InitMetrics(cfg.OTel)
    defer tp.Shutdown(context.Background())
    defer mp.Shutdown(context.Background())

    temporalClient, err := client.Dial(client.Options{
        HostPort:  cfg.Temporal.HostPort,
        Namespace: cfg.Temporal.Namespace,
    })
    defer temporalClient.Close()

    // Initialize dependencies
    githubClient := github.NewClient(cfg.GitHub)
    helmResolver := helm.NewResolver(githubClient)
    helmComposer := helm.NewComposer()
    ociPublisher := oci.NewClient(cfg.OCI)
    argoClient := argocd.NewClient(cfg.ArgoCD)
    stateRepo := state.NewRepository(documentdb.NewClient(cfg.DocumentDB))
    policyEngine := policy.NewEngine(cfg.Policies)

    // Create activity struct with dependencies
    activities := &activity.Activities{
        ChartResolver: helmResolver,
        ChartComposer: helmComposer,
        Publisher:     ociPublisher,
        ArgoClient:    argoClient,
        StateRepo:     stateRepo,
        PolicyEngine:  policyEngine,
    }

    // Register worker
    w := worker.New(temporalClient, cfg.Temporal.TaskQueue, worker.Options{})
    w.RegisterWorkflow(workflow.DeploymentWorkflow)
    w.RegisterActivity(activities)

    if err := w.Run(worker.InterruptCh()); err != nil {
        log.Fatal("Worker failed", "error", err)
    }
}
```

### O.4 API Server Structure

```go
// cmd/server/main.go

func main() {
    cfg := config.Load()
    tp := observability.InitTraces(cfg.OTel)
    mp := observability.InitMetrics(cfg.OTel)
    defer tp.Shutdown(context.Background())
    defer mp.Shutdown(context.Background())

    temporalClient, err := client.Dial(client.Options{...})
    defer temporalClient.Close()

    oidcValidator := auth.NewOIDCValidator(cfg.Auth)
    policyEngine := policy.NewEngine(cfg.Policies)
    stateRepo := state.NewRepository(documentdb.NewClient(cfg.DocumentDB))

    handler := api.NewHandler(temporalClient, oidcValidator, policyEngine, stateRepo)
    router := api.NewRouter(handler)

    srv := &http.Server{
        Addr:    cfg.Server.Addr,
        Handler: router,
    }
    // Graceful shutdown handling...
    srv.ListenAndServe()
}
```

### O.5 Configuration Model

```yaml
# config.yaml
server:
  addr: ":8080"
  readTimeout: 30s
  writeTimeout: 60s

temporal:
  hostPort: "temporal.example.com:7233"
  namespace: "platform-orchestrator"
  taskQueue: "deployment-workers"

auth:
  oidc:
    issuer: "https://token.actions.githubusercontent.com"
    audience: "platform-orchestrator.example.com"
    jwksCacheTTL: 24h
  allowedRepositories:
    - "myorg/payment-service"
    - "myorg/order-service"

argocd:
  serverURL: "https://argocd.example.com"
  tokenSecretName: "argocd-api-token"  # Azure Key Vault reference

oci:
  registry: "myregistry.azurecr.io"
  repositoryPrefix: ""

github:
  appID: 12345
  installationID: 67890
  privateKeySecretName: "github-app-key"  # Azure Key Vault reference

documentdb:
  endpoint: "https://myaccount.documents.azure.com"
  database: "platform-orchestrator"
  deploymentsCollection: "deployments"
  eventsCollection: "deployment-events"

otel:
  serviceName: "platform-orchestrator"
  otlpEndpoint: "otel-collector.observability:4317"
  tracesSampleRate: 1.0

policies:
  configPath: "/etc/policies/default.yaml"

deploymentDefaults:
  syncTimeoutSeconds: 600
  healthConvergenceTimeoutSeconds: 300
  workflowTimeoutMinutes: 20
```

### O.6 Go Version Pinning Policy

**Recommended version:** Go 1.24.2 (latest stable patch as of April 2026 — adjust to actual latest)

**Pinning strategy:**
- `go.mod`: `go 1.24` (minor version — allows any 1.24.x)
- `Dockerfile`: `FROM golang:1.24.2-alpine` (exact patch)
- `CI`: `go-version: '1.24.2'` in GitHub Actions

**Upgrade cadence:**
- **Patch releases (1.24.x → 1.24.y):** Upgrade within 2 weeks of release; run full test suite first
- **Minor releases (1.24 → 1.25):** Upgrade within 1 month; verify all SDK compatibility first
- **SDK compatibility matrix:** Before any Go upgrade, verify:
  - `go.temporal.io/sdk` supports the Go version
  - `helm.sh/helm/v3` builds without issues
  - `go.opentelemetry.io/otel` is compatible
  - `github.com/argoproj/argo-cd/v2` pkg dependencies work

**CI verification pipeline for Go upgrades:**
1. Create branch with Go version bump
2. Run `go build ./...` — verify compilation
3. Run `go vet ./...` — verify static analysis
4. Run full test suite
5. Run `govulncheck ./...` — check for known vulnerabilities
6. Build Docker images
7. Run integration tests against staging Temporal + Argo CD
8. Merge only if all green

### O.7 Testing Strategy

**Unit tests (70% coverage target):**
- Policy engine rules
- State machine transitions
- Values merge logic
- SemVer tag selection
- Chart version computation
- Request validation
- Error classification (retryable vs non-retryable)

**Integration tests:**
- Temporal workflow end-to-end with test environment
- Helm SDK chart loading and packaging with real charts
- OCI push/pull with test registry (use `zot` or `distribution` in CI)
- DocumentDB CRUD with CosmosDB emulator

**Contract tests:**
- Argo CD API client against recorded responses (use `go-vcr` or `httpmock`)
- GitHub API client against recorded responses
- OIDC token validation against test JWTs

**End-to-end tests (staging):**
- Full deployment flow with staging Argo CD and staging cluster
- Failure injection: unavailable registry, stuck sync, invalid chart
- Compensation flow verification

**Temporal-specific tests:**
```go
// Use Temporal test framework
func TestDeploymentWorkflow(t *testing.T) {
    testSuite := &testsuite.WorkflowTestSuite{}
    env := testSuite.NewTestWorkflowEnvironment()

    env.RegisterActivity(activities)
    env.OnActivity(ValidateDeployment, mock.Anything, mock.Anything).Return(nil)
    env.OnActivity(GenerateMetadata, mock.Anything, mock.Anything).Return(metadata, nil)
    // ... mock all activities

    env.ExecuteWorkflow(DeploymentWorkflow, request)

    require.True(t, env.IsWorkflowCompleted())
    require.NoError(t, env.GetWorkflowError())
}
```

### O.8 Deployment Model

**Two deployments:**
1. **API Server** — Kubernetes Deployment with HPA, exposed via Service + Ingress
2. **Temporal Worker** — Kubernetes Deployment with fixed replicas (scale based on Temporal metrics)

**Both share:**
- Same Go module, different entrypoints (`cmd/server`, `cmd/worker`)
- Same Docker base image, different CMD
- Same config mounted via ConfigMap + Secret references
- Workload Identity for Azure services

```yaml
# Simplified server deployment
apiVersion: apps/v1
kind: Deployment
metadata:
  name: platform-orchestrator-server
spec:
  replicas: 2
  template:
    spec:
      serviceAccountName: platform-orchestrator  # Workload Identity
      containers:
      - name: server
        image: myregistry.azurecr.io/platform-orchestrator:v1.0.0
        command: ["/server"]
        ports:
        - containerPort: 8080
        - containerPort: 9090  # metrics
        env:
        - name: OTEL_SERVICE_NAME
          value: "platform-orchestrator-server"
        livenessProbe:
          httpGet: { path: /healthz, port: 8080 }
        readinessProbe:
          httpGet: { path: /readyz, port: 8080 }
```

### O.9 CI/CD Considerations

```yaml
# .github/workflows/ci.yaml
name: CI
on: [push, pull_request]
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
    - uses: actions/checkout@v4
    - uses: actions/setup-go@v5
      with:
        go-version: '1.24.2'
    - run: go build ./...
    - run: go vet ./...
    - run: go test -race -coverprofile=coverage.out ./...
    - run: govulncheck ./...
    - run: golangci-lint run

  integration:
    needs: build
    runs-on: ubuntu-latest
    services:
      cosmosdb-emulator: ...
      temporal: ...
    steps:
    - run: go test -tags=integration ./...

  docker:
    needs: [build, integration]
    runs-on: ubuntu-latest
    steps:
    - run: docker build -f Dockerfile.server -t platform-orchestrator-server .
    - run: docker build -f Dockerfile.worker -t platform-orchestrator-worker .
    - run: docker push ...  # to ACR
```

---

## Section P — Recommended First Increment

### MVP-0: Validate Architecture with Minimum Surface Area

**Goal:** Deploy one real application through the full pipeline to prove the architecture works end-to-end.

**Scope:**
1. API server with hardcoded policy (single allowed repo, single environment)
2. OIDC validation (full implementation — this is security-critical and cannot be faked)
3. Temporal workflow with all activities (happy path only — no compensation yet)
4. Chart resolution from a test chart repository with known tags
5. Chart composition with values injection (no dependency handling — use a chart with no deps)
6. OCI push to ACR
7. Argo CD Application creation and health polling
8. DocumentDB state write (current state only — no event history yet)
9. Basic OTel traces (no custom metrics yet)

**Explicitly deferred:**
- Compensation / rollback (manual intervention for failures)
- Event history collection
- Complex policy rules
- Deployment status API endpoint
- Custom metrics and dashboards
- Rate limiting
- Multiple environments

**Validation criteria:**
- A GitHub Actions workflow can trigger a deployment end-to-end
- The correct chart version is resolved, composed, and published to OCI
- Argo CD creates the application and reaches Healthy
- DocumentDB contains the deployment record
- Traces show the full deployment path in the observability backend
- A second trigger with the same idempotency key returns the existing deployment

**Estimated effort:** 2 engineers, 3-4 weeks

**Risk reduction:**
- Proves Temporal ↔ Helm SDK ↔ OCI ↔ Argo CD integration works
- Validates OIDC auth model in real GitHub Actions
- Confirms DocumentDB schema and partition strategy under real data
- Uncovers any Helm SDK limitations early
- Establishes CI/CD pipeline and deployment model

---

## Appendix: Mandatory Topic Cross-Reference

| Topic | Section(s) |
|-------|-----------|
| 4.1 Artifact publication target | D.2, H.7, I.1 — OCI registry recommended |
| 4.2 Deterministic tag selection | H.2 — Highest SemVer tag |
| 4.3 Final chart versioning | H.6 — `{sourceVersion}-{timestamp}-{shortSha}` |
| 4.4 Dependency handling | H.4 — Pre-locked, no updates during orchestration |
| 4.5 AppProject validation | I.2 — Fail-fast before artifact publication |
| 4.6 GitHub Actions auth | L.1 — OIDC federation, zero secrets |
| 4.7 Temporal design | G — Full workflow, activity, retry, search attribute design |
| 4.8 Deployment success semantics | I.3 — Synced+Healthy with explicit timeout rules |
| 4.9 Rollback/compensation | E.2, G.4 — Saga pattern, no auto-rollback for degraded |
| 4.10 DocumentDB design | K — Collections, partition keys, schema, RBAC |
| 4.11 OTel logging stance | M.3 — slog + trace_id correlation, OTel log bridge deferred |
| 4.12 Go patch pinning | O.6 — Exact patch in Docker/CI, minor in go.mod |

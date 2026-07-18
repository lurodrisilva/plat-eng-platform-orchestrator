<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-19 | Updated: 2026-05-19 -->

# kubernetes

## Purpose
Raw Kubernetes manifests for the server binary. The Deployment uses `serviceAccountName: platform-orchestrator`, image from `myregistry.azurecr.io/...:latest`, env from `Secret/platform-orchestrator-secrets`, and mounts `ConfigMap`s for `config.yaml` (`/etc/config`) + policies (`/etc/policies`). Hardened pod security context: `runAsNonRoot`, `readOnlyRootFilesystem`, `allowPrivilegeEscalation:false`, `capabilities.drop:[ALL]`. The deploy pipeline runs in-process inside the server (async executor, ADR-0016) — there is no separate worker.

## Key Files
| File | Description |
|------|-------------|
| `server.yaml` | `Deployment platform-orchestrator-server` (2 replicas, port 8080, liveness `/healthz`, readiness `/readyz`, 250m/256Mi → 500m/512Mi) + `Service` (ClusterIP, port 8080) |

## For AI Agents

### Working In This Directory
- `image: ...:latest` — replace with immutable tag (sha or version) before production rollout.
- `readOnlyRootFilesystem: true` requires writable tmpfs — if the server starts needing scratch space, add an `emptyDir` volume at `/tmp`.
- ConfigMap `platform-orchestrator-config` must contain `config.yaml` (mirrors repo's `config.yaml`); ConfigMap `platform-orchestrator-policies` must contain `default.yaml`.
- `OTEL_SERVICE_NAME` env vars override `cfg.OTel.ServiceName` only if your config sources it (currently it does not — config wins). Keep them aligned with `config.yaml` to avoid surprise.
- The async executor's concurrency is bounded by pod replicas × in-flight goroutines, not an external queue; a mid-flight restart is re-driven from persisted state via `POST /api/v1/deployments/{id}/deploy`.

## Dependencies

### External (cluster-side)
- `Namespace`, `ServiceAccount platform-orchestrator`, `ConfigMap platform-orchestrator-config`, `ConfigMap platform-orchestrator-policies`, `Secret platform-orchestrator-secrets`
- An image registry reachable at `myregistry.azurecr.io`

<!-- MANUAL: -->

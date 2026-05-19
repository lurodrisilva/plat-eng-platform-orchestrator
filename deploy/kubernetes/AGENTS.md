<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-19 | Updated: 2026-05-19 -->

# kubernetes

## Purpose
Raw Kubernetes manifests for the two binaries. Both Deployments use `serviceAccountName: platform-orchestrator`, image from `myregistry.azurecr.io/...:latest`, env from `Secret/platform-orchestrator-secrets`, and mount `ConfigMap`s for `config.yaml` (`/etc/config`) + policies (`/etc/policies`). Hardened pod security context: `runAsNonRoot`, `readOnlyRootFilesystem`, `allowPrivilegeEscalation:false`, `capabilities.drop:[ALL]`.

## Key Files
| File | Description |
|------|-------------|
| `server.yaml` | `Deployment platform-orchestrator-server` (2 replicas, port 8080, liveness `/healthz`, readiness `/readyz`, 250m/256Mi → 500m/512Mi) + `Service` (ClusterIP, port 8080) |
| `worker.yaml` | `Deployment platform-orchestrator-worker` (2 replicas, no port, mounts `tmp` emptyDir, 500m/512Mi → 1 CPU/1Gi). No probes (Temporal worker has no HTTP surface) |

## For AI Agents

### Working In This Directory
- `image: ...:latest` — replace with immutable tag (sha or version) before production rollout.
- `readOnlyRootFilesystem: true` requires writable tmpfs. Worker mounts an `emptyDir` at `/tmp`; server doesn't — if server starts needing scratch space, add the same volume.
- ConfigMap `platform-orchestrator-config` must contain `config.yaml` (mirrors repo's `config.yaml`); ConfigMap `platform-orchestrator-policies` must contain `default.yaml`.
- `OTEL_SERVICE_NAME` env vars override `cfg.OTel.ServiceName` only if your config sources it (currently it does not — config wins). Keep them aligned with `config.yaml` to avoid surprise.
- Replicas=2 for worker is independent of Temporal task queue partitioning — Temporal handles work distribution.

## Dependencies

### External (cluster-side)
- `Namespace`, `ServiceAccount platform-orchestrator`, `ConfigMap platform-orchestrator-config`, `ConfigMap platform-orchestrator-policies`, `Secret platform-orchestrator-secrets`
- An image registry reachable at `myregistry.azurecr.io`

<!-- MANUAL: -->

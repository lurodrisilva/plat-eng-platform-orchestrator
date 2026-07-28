<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-19 | Updated: 2026-05-19 -->

# kubernetes

## Purpose
Raw Kubernetes manifests for the server binary. The Deployment uses `serviceAccountName: platform-orchestrator`, image from `myregistry.azurecr.io/...:latest`, env from `Secret/platform-orchestrator-secrets`, and mounts `ConfigMap`s for `config.yaml` (`/etc/config`) + policies (`/etc/policies`). Hardened pod security context: `runAsNonRoot`, `readOnlyRootFilesystem`, `allowPrivilegeEscalation:false`, `capabilities.drop:[ALL]`. The deploy pipeline runs in-process inside the server (async executor, ADR-0016) — there is no separate worker.

## Key Files
| File | Description |
|------|-------------|
| `namespace.yaml` | `Namespace platform-orchestrator`, labelled `azure.workload.identity/use: "true"` |
| `serviceaccount.yaml` | `ServiceAccount platform-orchestrator`, annotated `azure.workload.identity/client-id` = gate-3 AcrPush UAMI (ADR-0021 §3) |
| `server.yaml` | `Deployment platform-orchestrator-server` (2 replicas, port 8080, liveness `/healthz`, readiness `/readyz`, 250m/256Mi → 500m/512Mi; WI pod label; ACR image; ArgoCD/OCI env) + `Service` (ClusterIP, port 8080) |
| `externalsecret.yaml` | Secretless target: ESO syncs `DOCUMENTDB_CONNECTION_STRING` from the Key Vault into `platform-orchestrator-secrets`. Not yet applied — the cluster's `azure-keyvault` ClusterSecretStore is `InvalidProviderConfig`; the Secret is bootstrapped at deploy until that store is fixed. |
| `kustomization.yaml` | Aggregates namespace + SA + server. ConfigMaps (`config`/`policies`) and the Secret are created at deploy (DRY + secretless), not generated here. |

## For AI Agents

### Working In This Directory
- Push to ACR is secretless via gate-3 workload identity: the pod carries `azure.workload.identity/use: "true"` and the SA is annotated with the UAMI client-id; the webhook injects `AZURE_*` for azidentity. Rotating the identity = re-annotate the SA with the new `acr_orchestrator_push_client_id`.
- ArgoCD lives in `devops-system` (not `argocd`): `ARGOCD_APP_NAMESPACE=devops-system`, `ARGOCD_SERVER_URL=https://argocd-server.devops-system.svc`, `ARGOCD_INSECURE=true` (self-signed in-cluster cert; hardening follow-up: trust the argocd CA).
- `image: ...:latest` — replace with immutable tag (sha or version) before production rollout.
- `readOnlyRootFilesystem: true` requires writable tmpfs — if the server starts needing scratch space, add an `emptyDir` volume at `/tmp`.
- Deploy with `kubectl apply -k .` **from the repo root**, not from this directory. The root kustomization generates the two ConfigMaps from `config.yaml` and `policies/default.yaml`; applying this directory alone yields pods stuck in `ContainerCreating` on missing ConfigMaps.
- Do not create those ConfigMaps by hand. They were `kubectl create cm --from-file=...` until 2026-07-28, and had frozen at their 2026-07-18 content: the live allowlist was still root-scoped `replicaCount` after PR #22 re-keyed it to `app.replicaCount` on 2026-07-26, and `oci.appChartRepository` had never arrived at all. `mode: audit` hid the first; the second silently degraded every `GET /api/v1/apps/{name}` to `chartStatus: "unavailable"`. The generator's name-suffix hash is what forces the rollout — the policy is read once at startup, so an in-place ConfigMap edit changes nothing until the pods restart.
- `OTEL_SERVICE_NAME` env vars override `cfg.OTel.ServiceName` only if your config sources it (currently it does not — config wins). Keep them aligned with `config.yaml` to avoid surprise.
- The async executor's concurrency is bounded by pod replicas × in-flight goroutines, not an external queue; a mid-flight restart is re-driven from persisted state via `POST /api/v1/deployments/{id}/deploy`.

## Dependencies

### External (cluster-side)
- `Namespace`, `ServiceAccount platform-orchestrator`, `Secret platform-orchestrator-secrets`. The two ConfigMaps are no longer external — the root kustomization generates them.
- An image registry reachable at `myregistry.azurecr.io`

<!-- MANUAL: -->

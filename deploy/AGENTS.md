<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-19 | Updated: 2026-05-19 -->

# deploy

## Purpose
Kubernetes deployment manifests for the platform-orchestrator server.

## Subdirectories
| Directory | Purpose |
|-----------|---------|
| `kubernetes/` | `Deployment` + `Service` manifests for `server` (see `kubernetes/AGENTS.md`) |

## For AI Agents

### Working In This Directory
- Not yet packaged as a Helm chart — these are raw manifests.
- Two referenced `ConfigMap`s (`platform-orchestrator-config`, `platform-orchestrator-policies`) and one `Secret` (`platform-orchestrator-secrets`) must exist out-of-band before applying.
- The `ServiceAccount` `platform-orchestrator` is referenced but not defined here.

<!-- MANUAL: -->

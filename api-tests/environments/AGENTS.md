<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-19 | Updated: 2026-05-19 -->

# environments

## Purpose
Bruno environment definitions. Each `.bru` here defines `vars` (test data fixtures: appId, team, env, cluster, namespace, project, chart/image refs, gitSha, runId, workflow, actor, repo) plus `vars:secret [oidcToken]` that must be populated externally.

## Key Files
| File | Description |
|------|-------------|
| `local.bru` | `baseUrl: http://localhost:8080`, dev fixtures (`appId: payment-service`, `environment: development`, `cluster: minikube`, ...) |
| `staging.bru` | Staging endpoint + staging fixtures |

## For AI Agents

### Working In This Directory
- Switch env on CLI: `bru run --env local|staging`.
- Secrets: `bru run --env local --env-var oidcToken=...` or set via desktop UI; never commit JWTs.
- New environment → copy `local.bru`, update `baseUrl` + targeting fields, keep variable **names** identical to keep tests portable.

<!-- MANUAL: -->

<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-19 | Updated: 2026-05-19 -->

# create

## Purpose
Happy-path tests for `POST /api/v1/deployments`. Each `.bru` posts a deployment request with bearer OIDC auth + idempotency key, then captures `deploymentId` + `statusUrl` into post-response vars for chaining into `status/` tests.

## Key Files
| File | Description |
|------|-------------|
| `folder.bru` | Folder metadata |
| `create-deployment.bru` | Full payload (image, chart, target, values, source); asserts 202, `deploymentId` matches `/^dep-/`, `status: RECEIVED`, `<2s` |
| `create-with-chart-constraint.bru` | Tilde range `~1.5.0` in `chart.versionConstraint` |
| `create-minimal-values.bru` | Null `values`, no `correlationId` — smoke test for optional fields |

## For AI Agents

### Working In This Directory
- Tests rely on the server actually running with a wired `TokenValidator` + `PolicyEvaluator` — both are `nil` in current `cmd/server/main.go`, so live runs will currently fault on auth.
- `Idempotency-Key` header is sent but the server does not implement idempotency yet — coverage is forward-looking.

<!-- MANUAL: -->

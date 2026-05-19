<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-19 | Updated: 2026-05-19 -->

# handler

## Purpose
HTTP request handlers for deployment + health endpoints. Decodes JSON bodies into command DTOs, dispatches to `deploymentapp.Application`, and encodes responses as JSON with structured error envelopes.

## Key Files
| File | Description |
|------|-------------|
| `deployment.go` | `Deployment` handler — `Create` (POST: OIDC bearer validate → decode `createRequest` → build `CreateDeploymentCommand` → `app.Commands.CreateDeployment.Handle` → 202 with `{deploymentId, status, statusUrl, createdAt}`); `Status` (GET: `app.Queries.GetDeployment.Handle` → 200 with DTO, 404 on `ErrNotFound`) |
| `health.go` | `Health` handler — `Liveness` (`GET /healthz` → `{"status":"ok"}`), `Readiness` (`GET /readyz` → `{"status":"ready"}`) |
| `response.go` | `writeJSON`, `writeError` (envelope `{error: {code, message}, timestamp}`), `extractBearer` (case-insensitive `Bearer <token>`) |

## For AI Agents

### Working In This Directory
- All errors flow through `writeError(status, code, message)` — keep code names SCREAMING_SNAKE_CASE (`AUTHENTICATION_FAILED`, `VALIDATION_ERROR`, `DEPLOYMENT_FAILED`, `NOT_FOUND`, `INTERNAL_ERROR`).
- `Deployment.Create` currently calls `h.validator.Validate(...)` — `validator` is nil-injected in `cmd/server/main.go`; first authed request will panic.
- 404 vs 422 vs 401 mapping: missing auth → 401; OIDC fail → 401; JSON decode error → 400; use-case error → 422; not-found → 404.
- `Readiness` currently always returns ready — wire actual dependency checks (Mongo ping, ArgoCD reachability) before relying on it.

### Testing Requirements
- HTTP handler tests via `net/http/httptest.NewRecorder` + injected fake `deploymentapp.Application` (currently absent).

### Common Patterns
- Use `telemetry.Enrich(ctx, logger)` for trace-correlated logs inside a handler.
- `r.PathValue("id")` for Go 1.22 path variables.

## Dependencies

### Internal
- `internal/application/deployment` (use cases)
- `internal/application/port` (token validator)
- `internal/infrastructure/telemetry` (log enrich)

### External
- `encoding/json`, `net/http`, `log/slog`, `time`

<!-- MANUAL: -->

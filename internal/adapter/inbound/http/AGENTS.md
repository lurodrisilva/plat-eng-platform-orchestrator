<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-19 | Updated: 2026-05-19 -->

# http

## Purpose
HTTP inbound adapter. Mounts routes on a `net/http.ServeMux` (Go 1.22+ pattern-matching syntax `"METHOD /path/{var}"`), wraps with tracing/logging/recovery middleware, and dispatches to deployment + health handlers.

## Key Files
| File | Description |
|------|-------------|
| `router.go` | `NewRouter(deploymentHandler, logger)` — registers `GET /healthz`, `GET /readyz`, `POST /api/v1/deployments`, `GET /api/v1/deployments/{id}`; wraps in `Tracing → Logging → Recovery` (outer-first) |

## Subdirectories
| Directory | Purpose |
|-----------|---------|
| `handler/` | Endpoint handlers (deployment, health) + JSON/bearer helpers (see `handler/AGENTS.md`) |
| `middleware/` | Tracing, request logging, panic recovery (see `middleware/AGENTS.md`) |

## For AI Agents

### Working In This Directory
- New route → add line in `NewRouter` and corresponding method on a handler struct in `handler/`.
- Middleware order in `NewRouter` wraps outside-in: `Recovery(Logging(Tracing(mux)))` — recovery is outermost so panics in any layer are caught.
- Server timeouts (read/write) come from `cfg.Server` and are set in `cmd/server/main.go`.

### Common Patterns
- HTTP method bound to route via Go 1.22 syntax: `mux.HandleFunc("POST /api/v1/deployments", ...)`.
- Path vars via `r.PathValue("id")`.

## Dependencies

### Internal
- `internal/adapter/inbound/http/handler`
- `internal/adapter/inbound/http/middleware`

### External
- `net/http`, `log/slog`

<!-- MANUAL: -->

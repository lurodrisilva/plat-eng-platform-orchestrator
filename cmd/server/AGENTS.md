<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-19 | Updated: 2026-05-19 -->

# server

## Purpose
HTTP API server entrypoint. Wires DocumentDB persistence, application use cases, HTTP handlers, and router; serves on `cfg.Server.Addr` with read/write timeouts; handles graceful shutdown via SIGINT/SIGTERM.

## Key Files
| File | Description |
|------|-------------|
| `main.go` | Composition root — loads config, inits OTel + logger + Mongo, builds `deploymentapp.Application`, mounts HTTP router, runs server |

## For AI Agents

### Working In This Directory
- Token validator is currently `nil` in `handler.NewDeployment(app, nil, logger)` — wire a real `port.TokenValidator` before any production use; the OIDC validator adapter does not yet exist.
- Policy evaluator passed to `CreateDeploymentHandler` is also `nil` — `Handle` will nil-panic when policy evaluation runs.
- Config path defaults to `config.yaml`, overridable via `CONFIG_PATH` env var.
- Shutdown timeout = 15s; trace shutdown timeout = 5s.

### Testing Requirements
- No tests at this layer — integration tested via `api-tests/` Bruno collection.

### Common Patterns
- `run() error` pattern; errors bubble to `main` for non-zero exit.

## Dependencies

### Internal
- `internal/infrastructure/config`
- `internal/infrastructure/telemetry`
- `internal/adapter/inbound/http`
- `internal/adapter/inbound/http/handler`
- `internal/adapter/outbound/persistence`
- `internal/application/deployment`

### External
- `go.mongodb.org/mongo-driver/mongo`

<!-- MANUAL: -->

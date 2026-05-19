<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-19 | Updated: 2026-05-19 -->

# cmd

## Purpose
Binary entrypoints. Each subdirectory builds to one executable in `bin/`. This is the **composition root**: only place where concrete adapters get wired into application ports.

## Subdirectories
| Directory | Purpose |
|-----------|---------|
| `server/` | HTTP API server entrypoint (see `server/AGENTS.md`) |
| `worker/` | Temporal worker entrypoint — stub, not yet wired (see `worker/AGENTS.md`) |

## For AI Agents

### Working In This Directory
- Keep `main.go` files thin — config load, telemetry init, adapter wiring, server start, graceful shutdown.
- No business logic here. If you write logic in `cmd/**`, move it to `internal/application` or `internal/domain`.
- Build via `make build-server` / `make build-worker`.

### Common Patterns
- `run()` returns error; `main()` exits non-zero on error.
- `signal.NotifyContext` for SIGINT/SIGTERM-driven shutdown.
- Defer `mongoClient.Disconnect`, tracer `Shutdown` with timeout context.

## Dependencies

### Internal
- `internal/infrastructure/config` — config loader
- `internal/infrastructure/telemetry` — OTel + slog init
- `internal/adapter/**` — concrete port implementations
- `internal/application/deployment` — use-case handlers

<!-- MANUAL: -->

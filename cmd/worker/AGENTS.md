<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-19 | Updated: 2026-05-19 -->

# worker

## Purpose
Temporal worker entrypoint. **Currently a stub** — prints "worker: not yet wired to new hexagonal structure" and exits. Will host Temporal workflow + activity registration once workflow/activity packages are refactored against the port-based architecture.

## Key Files
| File | Description |
|------|-------------|
| `main.go` | Stub entrypoint — no Temporal client wired yet |

## For AI Agents

### Working In This Directory
- When wiring the worker: import `go.temporal.io/sdk/{client,worker}`, register workflow + activities, block on `worker.Run(worker.InterruptCh())`.
- Activity dependencies must come from `internal/application/port` interfaces, never from concrete adapters directly.
- Config namespace: `cfg.Temporal.HostPort`, `cfg.Temporal.Namespace`, `cfg.Temporal.TaskQueue` ("deployment-workers").

### Testing Requirements
- Currently none. Add Temporal `testsuite` based activity tests once wired.

## Dependencies

### Internal (planned)
- `internal/infrastructure/config`
- `internal/application/deployment` (workflow logic)
- `internal/adapter/outbound/{argocd,github,oci,persistence}`

<!-- MANUAL: -->

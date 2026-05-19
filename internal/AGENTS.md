<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-19 | Updated: 2026-05-19 -->

# internal

## Purpose
All library code. Organized as hexagonal architecture with strict dependency direction: `domain` → nothing, `application` → `domain` + `application/port`, `adapter/*` → `application/port` + `domain`, `infrastructure` → nothing application-specific. Composition happens in `cmd/`.

## Subdirectories
| Directory | Purpose |
|-----------|---------|
| `domain/` | Pure business model — aggregates, value objects, status machine, repository interface (see `domain/AGENTS.md`) |
| `application/` | Use-case handlers + outbound port interfaces (see `application/AGENTS.md`) |
| `adapter/` | Concrete inbound (HTTP) and outbound (Argo CD, GitHub, OCI, DocumentDB) adapters (see `adapter/AGENTS.md`) |
| `infrastructure/` | Config loader + OTel/logging setup (see `infrastructure/AGENTS.md`) |

## For AI Agents

### Working In This Directory
- Strict dependency rule:
  - `domain` imports nothing from this project
  - `application` imports `domain` and `application/port` only
  - `adapter/*` imports `application/port` and `domain` (never another adapter, never `application/deployment`)
  - `cmd` is the only allowed caller of concrete adapters
- New external integration → add port in `application/port/` first, then adapter in `adapter/outbound/<name>/`.
- New inbound channel (gRPC, queue) → put under `adapter/inbound/<name>/`.

### Testing Requirements
- Domain logic: unit tests next to the code (e.g., `entity_test.go`, `status_test.go`).
- Adapters: integration tests against ephemeral services (Mongo testcontainer, fake HTTP server).

### Common Patterns
- Compile-time interface assertions: `var _ port.X = (*Impl)(nil)` at end of adapter files.
- Repository interface lives in `domain/deployment/repository.go` (DDD: dependency inversion at aggregate boundary).
- Outbound ports live in `application/port/` (not domain) when they exist purely to serve use cases.

## Dependencies

### Internal
- This package is internal to the module; not importable by external code (Go `internal/` convention).

<!-- MANUAL: -->

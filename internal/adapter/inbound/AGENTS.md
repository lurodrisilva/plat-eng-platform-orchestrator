<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-19 | Updated: 2026-05-19 -->

# inbound

## Purpose
Driving adapters — entry points that translate external requests (currently only HTTP) into application commands/queries.

## Subdirectories
| Directory | Purpose |
|-----------|---------|
| `http/` | HTTP router, handlers, and middleware (see `http/AGENTS.md`) |

## For AI Agents

### Working In This Directory
- Add a new transport (gRPC, NATS, Temporal signal listener) as a sibling under `inbound/`.
- Inbound handlers depend only on `internal/application/deployment` + `internal/application/port` — never on adapters or domain mutators directly.

<!-- MANUAL: -->

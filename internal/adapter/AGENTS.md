<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-19 | Updated: 2026-05-19 -->

# adapter

## Purpose
Adapter layer for hexagonal architecture. Inbound adapters translate external protocols into application commands/queries; outbound adapters implement `application/port.*` interfaces against real systems (Argo CD, GitHub, OCI registries, MongoDB/DocumentDB).

## Subdirectories
| Directory | Purpose |
|-----------|---------|
| `inbound/` | Driving adapters — HTTP API (see `inbound/AGENTS.md`) |
| `outbound/` | Driven adapters — Argo CD, GitHub, OCI, persistence (see `outbound/AGENTS.md`) |

## For AI Agents

### Working In This Directory
- Adapters never import other adapters. Cross-cutting helpers go in `internal/infrastructure`.
- Each outbound adapter exposes a constructor (`NewClient`, `NewPublisher`, ...) returning a concrete struct, and asserts the port: `var _ port.X = (*Impl)(nil)`.
- Inbound adapter handlers receive `deploymentapp.Application` (the use-case bundle) — not individual repositories.

<!-- MANUAL: -->

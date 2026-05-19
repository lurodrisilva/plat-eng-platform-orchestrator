<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-19 | Updated: 2026-05-19 -->

# domain

## Purpose
Pure domain model. Zero dependencies on external packages outside the Go standard library. Encodes deployment business rules, invariants, and lifecycle.

## Subdirectories
| Directory | Purpose |
|-----------|---------|
| `deployment/` | The `Deployment` aggregate, value objects, status machine, repository contract, sentinel errors (see `deployment/AGENTS.md`) |

## For AI Agents

### Working In This Directory
- **No imports** from `internal/application`, `internal/adapter`, or `internal/infrastructure`.
- **No imports** from third-party packages (only `std`).
- Validation lives in constructors; getters are pure; mutators enforce invariants.

## Dependencies

### External
- Go standard library only

<!-- MANUAL: -->

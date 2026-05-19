<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-19 | Updated: 2026-05-19 -->

# deployment (application)

## Purpose
Deployment use-case handlers. Package name `deploymentapp` (aliased to avoid clash with `internal/domain/deployment`). Wraps validation + persistence + policy into transaction-scoped commands/queries.

## Key Files
| File | Description |
|------|-------------|
| `dto.go` | `Application` struct grouping `Commands` (`CreateDeployment`) and `Queries` (`GetDeployment`) — single injection point for adapters |
| `create.go` | `CreateDeploymentHandler` — runs `PolicyEvaluator.Evaluate`, builds value objects (`NewImage`/`NewChartSource`/`NewTarget`/`NewSource`), constructs `Deployment` aggregate, persists initial state via `Repository.Save`; returns `{DeploymentID, Status}` |
| `get.go` | `GetDeploymentHandler` + `DeploymentDTO` (JSON-tagged read model) — maps aggregate to flat DTO including metadata, health, completed/duration |

## For AI Agents

### Working In This Directory
- Handlers expose `New<Foo>Handler(deps) *<Foo>Handler` + `Handle(ctx, cmd|query) (Result, error)` pattern.
- **Bug to watch**: `CreateDeploymentHandler.Handle` dereferences `h.policy.Evaluate(...)` without nil-check; `cmd/server/main.go` currently passes `nil` policy — first POST will panic.
- **DTO note**: `CompletedAt` is `*time.Time` (omitempty); `Get` handler sets it from `d.CompletedAt()` only when non-zero.
- New use case → new file `<verb>.go`, expose `<Verb>Handler` + `<Verb>{Command|Query}` + `<Verb>Result`, register in `Application` struct.

### Testing Requirements
- Use fake `deployment.Repository` + fake `port.PolicyEvaluator` for unit tests.

### Common Patterns
- Command DTO carries flat primitives; conversion to value objects happens inside `Handle`.
- Error wrap prefix is the use-case name (`"create deployment: %w"`, `"get deployment: %w"`).
- Repository call wrap: `fmt.Errorf("create deployment: save: %w", err)`.

## Dependencies

### Internal
- `internal/domain/deployment`
- `internal/application/port`

<!-- MANUAL: -->

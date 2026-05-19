<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-19 | Updated: 2026-05-19 -->

# deployment (domain)

## Purpose
The deployment bounded context. Contains the `Deployment` aggregate root, immutable value objects (`Image`, `Target`, `Source`, `ChartSource`, `DeploymentID`, `ComponentID`, `DeploymentVersion`, `ArgoAppName`), the 14-state lifecycle machine, sentinel errors, and the `Repository` port (defined here to keep domain in control of its persistence contract).

## Key Files
| File | Description |
|------|-------------|
| `entity.go` | `Deployment` aggregate root + `Metadata`/`Artifact`/`ArgoApp`/`Health` value objects, `New`/`Reconstitute`, state transition methods (`TransitionTo`, `Complete`, `Fail`, `Reject`), deep-copied `values` map |
| `valueobject.go` | `DeploymentID`, `ComponentID`, `DeploymentVersion`, `ArgoAppName`, `Image`, `Target`, `Source`, `ChartSource` — all validated in constructors |
| `status.go` | `Status` enum (1..14: RECEIVED..COMPLETED), `_transitions` allow-map, `_terminalStatuses`, `CanTransitionTo`, `IsTerminal`, `IsSuccess`, `ParseStatus` |
| `errors.go` | Sentinel errors (`ErrNotFound`, `ErrPolicyViolation`, `ErrInvalidTransition`, chart errors, auth errors) + `NonRetryableTypes()` for Temporal |
| `repository.go` | `Repository` interface: `Save`, `FindByID`, `FindByApplication` (implemented by `adapter/outbound/persistence`) |
| `entity_test.go` | Aggregate tests — construction, validation, transitions, values copy protection |
| `status_test.go` | State machine tests — allowed/disallowed transitions, terminal status, String/Parse roundtrip |

## For AI Agents

### Working In This Directory
- **Domain rule**: `New(...)` validates inputs (required appID, team, image digest `sha256:` prefix, gitSHA ≥7 chars, run attempt ≥1).
- **Identity rule**: `DeploymentID` is deterministic from `(date, appID, env[:4], shortSHA)`; recomputed in `SetMetadata`.
- **State machine**: every transition must go through `TransitionTo`; check `_transitions` map in `status.go` when adding/changing states.
- **Values copy**: `values` map is deep-copied on both ingest (`New`) and read (`Values()`) — never expose internal map.
- Add new lifecycle states → also extend `String()`, `ParseStatus()`, `_transitions`, `_terminalStatuses` (if terminal).
- Sentinel errors are wrapped with `fmt.Errorf("...: %w", err)` by callers — use `errors.Is`.

### Testing Requirements
- Table-driven tests in `status_test.go` cover every allowed/disallowed transition.
- `entity_test.go` covers happy path, missing-field errors, transition validation, and values aliasing protection.
- Run `go test -race ./internal/domain/...`.

### Common Patterns
- Constructor returns `(T, error)` — no zero values escape unvalidated.
- Setters update `updatedAt = time.Now().UTC()`.
- `Source.ShortSHA()` returns first 7 chars — assumes `NewSource` validated length.

## Dependencies

### External
- `fmt`, `errors`, `strings`, `time`, `testing` — Go stdlib only

<!-- MANUAL: -->

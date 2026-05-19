<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-19 | Updated: 2026-05-19 -->

# application

## Purpose
Use-case layer. Orchestrates domain objects to fulfill commands and queries. Declares outbound port interfaces consumed by use cases (implemented by `adapter/outbound/*`).

## Subdirectories
| Directory | Purpose |
|-----------|---------|
| `deployment/` | Deployment commands + queries (`CreateDeploymentHandler`, `GetDeploymentHandler`, `Application` aggregator, DTO) (see `deployment/AGENTS.md`) |
| `port/` | Outbound port interfaces — `ArgoCD`, `ArtifactPublisher`, `ChartResolver`/`ChartComposer`, `PolicyEvaluator`, `TokenValidator` (see `port/AGENTS.md`) |

## For AI Agents

### Working In This Directory
- Use cases depend on `domain` types and `application/port` interfaces — never concrete adapters.
- One handler per command/query (CQRS-light).
- Wrap errors with `fmt.Errorf("create deployment: %w", err)` for traceability.
- DTOs (`DeploymentDTO`) live with their query handler, not in domain.

### Testing Requirements
- Mock ports for unit tests (no real Mongo/HTTP).

## Dependencies

### Internal
- `internal/domain/deployment`

<!-- MANUAL: -->

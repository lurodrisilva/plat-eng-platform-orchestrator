<!-- Generated: 2026-05-19 | Updated: 2026-05-19 -->

# platform-orchestrator

## Purpose
Go-based deployment control plane that sits between GitHub Actions and Argo CD. Accepts OIDC-authenticated deployment requests, validates policy, resolves Helm charts from GitHub, packages them with per-deployment values, publishes immutable OCI artifacts, and drives Argo CD Applications to sync + health convergence. Structured per hexagonal (ports + adapters) architecture.

## Key Files
| File | Description |
|------|-------------|
| `go.mod` | Go module manifest (Go 1.25, Helm v3, Mongo driver, OTel, x/mod/semver) |
| `Makefile` | Build/test/lint/vet/docker-build/govulncheck targets |
| `Dockerfile.server` | Distroless multi-stage build for API server |
| `Dockerfile.worker` | Distroless multi-stage build for worker (Temporal — not yet wired) |
| `config.yaml` | Runtime config (server, temporal, OIDC, argocd, OCI, github, documentdb, otel, policies) — supports `${ENV_VAR:-default}` expansion |
| `README.md` | Project name placeholder |
| `.gitignore` | Standard Go ignores |

## Subdirectories
| Directory | Purpose |
|-----------|---------|
| `cmd/` | Binary entrypoints (`server`, `worker`) (see `cmd/AGENTS.md`) |
| `internal/` | Library code — domain, application, adapters, infrastructure (see `internal/AGENTS.md`) |
| `policies/` | Deployment policy YAML (environment rules, freeze windows) (see `policies/AGENTS.md`) |
| `deploy/` | Kubernetes manifests for server + worker (see `deploy/AGENTS.md`) |
| `api-tests/` | Bruno API test collection (see `api-tests/AGENTS.md`) |
| `.github/` | CI workflows (see `.github/AGENTS.md`) |

## For AI Agents

### Working In This Directory
- Architecture: strict hexagonal — `domain` has zero external deps, `application` depends only on `domain` + `port`, `adapter/*` implements ports, `cmd` wires concretes.
- Module path: `github.com/myorg/platform-orchestrator`.
- Go 1.25 (go.mod). Dockerfiles still pin `golang:1.24.2-alpine` — mismatch.
- Use `make build` / `make test` / `make lint`. Tests run with `-race -count=1`.
- Never put adapter imports inside `internal/domain/**` or `internal/application/**`.

### Testing Requirements
- `make test` runs `go test -race -count=1 ./...`.
- Domain layer (`internal/domain/deployment`) has table-driven unit tests — extend rather than mock.
- API contract tests in `api-tests/` (Bruno collection).

### Common Patterns
- Sentinel errors in domain (`ErrNotFound`, `ErrPolicyViolation`, `ErrInvalidTransition`).
- Value objects validated in constructors (`NewImage`, `NewTarget`, `NewSource`, `NewChartSource`).
- Aggregate root `Deployment` enforces status transitions via `_transitions` map.
- Compile-time port checks: `var _ port.X = (*Impl)(nil)` in each adapter.
- Config via YAML + `os.ExpandEnv`; secrets passed through env vars only.
- Structured `slog` JSON logging with trace_id/span_id enrichment.

## Dependencies

### External
- `go.mongodb.org/mongo-driver` — Azure DocumentDB / MongoDB persistence
- `helm.sh/helm/v3` — chart packaging + OCI push
- `go.opentelemetry.io/otel/*` — tracing (OTLP gRPC exporter)
- `golang.org/x/mod/semver` — chart version resolution
- `gopkg.in/yaml.v3` — config parsing

<!-- MANUAL: -->

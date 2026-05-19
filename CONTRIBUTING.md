# Contributing

Thanks for your interest in the Platform Orchestrator. This guide covers everything you need to develop, test, and submit changes.

## Table of Contents

- [Local Development](#local-development)
- [Code Style](#code-style)
- [Architecture Rules](#architecture-rules)
- [Testing](#testing)
- [Commit Messages](#commit-messages)
- [Pull Requests](#pull-requests)
- [Adding a New Adapter](#adding-a-new-adapter)
- [Adding a New Use Case](#adding-a-new-use-case)
- [Adding a New HTTP Endpoint](#adding-a-new-http-endpoint)

## Local Development

### Prerequisites

- **Go 1.25+** — `go.mod` declares `go 1.25.0`. Older toolchains will refuse to build.
- **golangci-lint** — install via `brew install golangci-lint` or `go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest`.
- **govulncheck** — `go install golang.org/x/vuln/cmd/govulncheck@latest`.
- **Bruno** *(optional, for API tests)* — https://www.usebruno.com/

### Common commands

```bash
make fmt           # gofmt all files
make vet           # go vet ./...
make lint          # golangci-lint
make test          # go test -race -count=1 ./...
make test-coverage # writes coverage.html
make vuln-check    # govulncheck ./...
make build         # produces bin/server, bin/worker
make docker-build  # both container images
make mod-tidy      # go mod tidy
```

Run `make fmt vet test` locally before pushing.

### Running the server locally

```bash
export CONFIG_PATH=./config.yaml
export DOCUMENTDB_ENDPOINT="mongodb://localhost:27017"
export GITHUB_TOKEN=ghp_...
./bin/server
```

Hit `http://localhost:8080/healthz` to confirm liveness.

## Code Style

- Standard `gofmt` formatting. `make fmt` enforces.
- `golangci-lint` configuration is the project's source of truth; no per-file `//nolint` without justification in a trailing comment.
- Prefer **structured error wrapping**: `fmt.Errorf("<context>: %w", err)`. Never wrap with `%v` — it loses the chain.
- **Sentinel errors** live in the domain package (`internal/domain/deployment/errors.go`). Use `errors.Is` to compare; never `==`.
- **No package-level mutable state** outside `var _<lowercase>` private caches. Inject configuration through constructors.
- Logging: `slog` only. Use `telemetry.Enrich(ctx, logger)` inside request scopes to attach trace fields.
- Comments: only when the **why** is non-obvious. Don't restate the code. No comments referencing past PRs / tasks.
- Don't add helpers, fallbacks, or feature flags for hypothetical future requirements.

### Naming

- Use-case handlers: `<Verb><Noun>Handler`, e.g. `CreateDeploymentHandler`, `RolloutDeploymentHandler`.
- Commands and queries: `<Verb><Noun>Command` / `<Verb><Noun>Query` (e.g. `CreateDeploymentCommand`, `GetDeploymentQuery`).
- Results: `<Verb><Noun>Result` (or `<Noun>DTO` for read models).
- Ports: present-tense capability noun (`ArgoCD`, `ChartResolver`, `PolicyEvaluator`).
- Adapter constructors: `New<Concept>(deps...) *<Concept>` returning a concrete struct (not the interface) — the compile-time `var _ port.X = (*Concept)(nil)` covers conformance.

## Architecture Rules

The codebase is **hexagonal** with strict layer rules. Violations are blocking review comments.

| From                              | May import                                       | May **not** import                                       |
|-----------------------------------|--------------------------------------------------|----------------------------------------------------------|
| `internal/domain/**`              | Go stdlib only                                   | Anything else in the module                              |
| `internal/application/port`       | `context` + DTOs in the same package             | Domain mutators, adapters                                |
| `internal/application/deployment` | `internal/domain/**`, `internal/application/port`| Adapters, infrastructure                                 |
| `internal/adapter/**`             | `internal/domain/**`, `internal/application/port`| Sibling adapters, `internal/application/deployment` (except via the bundle passed to inbound) |
| `internal/infrastructure/**`      | Stdlib + third-party setup libs                  | Application, adapter, domain logic                        |
| `cmd/**`                          | Anything                                         | (it is the **only** allowed wiring layer)                |

When in doubt, read the closest `AGENTS.md` — every directory has one.

## Testing

- **Domain**: table-driven unit tests next to the code. See `status_test.go` for the canonical style.
- **Application**: handler tests against fake `domain.Repository` and fake ports.
- **Adapters**: integration tests against ephemeral services (Mongo testcontainer, fake HTTP server). Skip with `-short` when unavailable; CI runs the full suite.
- **API contract**: Bruno collection in `api-tests/`. Run with `bru run --env local`.

Race detector is mandatory: `go test -race -count=1 ./...` (this is what `make test` does).

### Adding a test

- Put the test file next to the code it covers (`foo_test.go` for `foo.go`).
- Prefer table-driven tests (`tests := []struct{...}; for _, tt := range tests { t.Run(tt.name, ...) }`).
- Use `t.Helper()` inside helper constructors so failures point at the caller line.
- Don't mock the SUT's collaborators if their behaviour is part of what's under test — use fakes for ports, not mocks for domain types.

## Commit Messages

Imperative mood, short subject (≤72 chars), blank line, then a body explaining **why** (not what — the diff covers that). Reference issues with `Fixes #N` or `Refs #N`.

```
Add OIDC token validator adapter

Wires a real `port.TokenValidator` against GitHub Actions JWKS, replacing
the `nil` placeholder in cmd/server/main.go. JWKS responses are cached
for the duration in config.auth.oidc.jwksCacheTTL (default 24h).
```

Squash trivial fixup commits before opening / merging.

## Pull Requests

1. Branch from `master`. Use a descriptive name: `feat/<short>`, `fix/<short>`, `docs/<short>`, `refactor/<short>`.
2. Keep each PR focused on one logical change. Cross-cutting cleanups belong in their own PR.
3. Run `make fmt vet lint test vuln-check` locally. CI runs the same checks.
4. PR description should include:
   - **Summary** — 1–3 bullets on what changed.
   - **Why** — motivation, linked issue.
   - **Test plan** — bullet list of how to verify (commands + expected output, or screenshots for UI).
5. Mark draft until self-review is complete. Re-request review after addressing comments rather than push-and-forget.
6. Squash-merge by default; merge commits are acceptable for cross-team PRs with multiple authors.

## Adding a New Adapter

When you need to integrate a new external system:

1. **Define the port first.** Add `internal/application/port/<capability>.go` with the interface and any DTOs. Keep DTOs flat — no domain imports.
2. **Implement the adapter.** Create `internal/adapter/outbound/<name>/` with one file per concrete type. End the file with `var _ port.<Capability> = (*<Impl>)(nil)`.
3. **Wire it in `cmd/server/main.go`.** Construct the adapter after config load and inject it into the use-case handler that needs it.
4. **Test.** Add a thin integration test (`go test -short` skippable) that hits a fake/testcontainer.
5. **Document.** Update or create `AGENTS.md` in the adapter directory.

## Adding a New Use Case

1. Decide whether it is a command (mutating) or a query (read-only). Both live in `internal/application/<bounded-context>/`.
2. Create `<verb>.go` containing:
   - The command/query struct (flat primitives).
   - The result struct (or `<Noun>DTO` for read models).
   - The handler: `New<Verb>Handler(deps) *<Verb>Handler` + `Handle(ctx, cmd) (Result, error)`.
3. Add the handler to the `Commands` / `Queries` struct in `dto.go`.
4. Wire it in `cmd/server/main.go`.
5. Add HTTP handler glue if the use case is exposed via the API (see below).
6. Add unit tests using fake repositories and ports.

## Adding a New HTTP Endpoint

1. Add the route in `internal/adapter/inbound/http/router.go`:

   ```go
   mux.HandleFunc("METHOD /api/v1/...", handler.Method)
   ```

2. Add the handler method to the relevant struct in `internal/adapter/inbound/http/handler/`. Use `r.PathValue("name")` for Go 1.22 path variables.
3. Decode the request body into a private `*Request` struct local to the handler file. Translate to the use-case command in-place.
4. Use `writeJSON(w, status, payload)` for success and `writeError(w, status, code, message)` for failure. Keep error codes in SCREAMING_SNAKE_CASE.
5. Add at least one happy-path and one error-path test in the Bruno collection under `api-tests/`.

---

Questions? Open an issue, or start a discussion in the PR.

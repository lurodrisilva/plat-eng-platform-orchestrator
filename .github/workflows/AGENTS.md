<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-19 | Updated: 2026-05-19 -->

# workflows

## Purpose
GitHub Actions CI definitions for build, lint, vulnerability scan, and Docker image build.

## Key Files
| File | Description |
|------|-------------|
| `ci.yaml` | Triggers on push/PR to `main`/`master`. Jobs: `build` (Go 1.25 — setup, mod download, build, vet, `go test -race -coverprofile=coverage.out -count=1`, upload coverage artifact); `lint` (golangci-lint installed from source); `vulnerability-check` (`govulncheck ./...`, advisory); `docker` (only on `refs/heads/main`, after build+lint; builds the server image tagged `${{ github.sha }}`) |

## For AI Agents

### Working In This Directory
- **Go version mismatch**: `go.mod` declares `go 1.25.0`; CI installs `1.24.2`. Bump `setup-go.with.go-version` to match or downgrade the module directive.
- `docker` job builds images but does **not** push — add a registry login + `docker push` step to ship images.
- No release/tag workflow exists.
- Coverage uploaded as an artifact only; no Codecov / threshold gate.

<!-- MANUAL: -->

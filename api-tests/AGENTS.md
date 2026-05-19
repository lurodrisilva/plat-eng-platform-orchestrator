<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-19 | Updated: 2026-05-19 -->

# api-tests

## Purpose
Bruno API test collection covering all HTTP endpoints exposed by the platform orchestrator. Used for manual regression + dev-loop verification before promoting changes. Runs with the Bruno CLI or the Bruno desktop app.

## Key Files
| File | Description |
|------|-------------|
| `bruno.json` | Collection manifest — name "Platform Orchestrator API", ignores `node_modules`/`.git` |
| `collection.bru` | Collection-level headers (`Accept: application/json`, `X-Client: bruno`), `apiVersion=v1` var, post-response 5xx logging, docs |

## Subdirectories
| Directory | Purpose |
|-----------|---------|
| `health/` | Liveness + readiness probe tests (see `health/AGENTS.md`) |
| `deployments/` | Deployment endpoint tests — create, status, errors (see `deployments/AGENTS.md`) |
| `environments/` | Bruno environment files for `local` + `staging` (see `environments/AGENTS.md`) |

## For AI Agents

### Working In This Directory
- Run a single env: `bru run --env local`.
- Tests live alongside requests in `.bru` files (see `tests { ... }` block).
- Auth: `vars:secret [oidcToken]` — must be set per-env via Bruno secrets UI / `--env-var oidcToken=...`.
- New endpoint → mirror folder structure under `deployments/...` with one `.bru` per scenario + a `folder.bru` for grouping.

### Common Patterns
- Each request file has `meta {name, type, seq}`, `headers/auth`, `body:json`, `vars:post-response` (chaining), `assert {...}`, `tests {...}`.
- Variable substitution: `{{varName}}` for collection/env vars, `{{$timestamp}}` for runtime.

## Dependencies

### External
- Bruno CLI / app (https://www.usebruno.com/)

<!-- MANUAL: -->

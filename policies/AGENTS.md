<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-19 | Updated: 2026-05-19 -->

# policies

## Purpose
Deployment policy definitions consumed by `port.PolicyEvaluator`. Encodes which branches/repos may deploy to which environment, and freeze windows blocking deployments entirely.

## Key Files
| File | Description |
|------|-------------|
| `default.yaml` | `environmentRules` — `production` (allow `main` + `release/*`), `staging` (allow `main` + `release/*` + `hotfix/*`), `development` (allow all branches, all repos). `freezeWindows: []` |

## For AI Agents

### Working In This Directory
- File path is referenced by `config.policies.configPath` (default `policies/default.yaml`).
- `allowedRepos: []` semantics: empty = allow all (per current schema).
- No `PolicyEvaluator` adapter exists yet — the file is read into structure-shaped configuration but there is no loader/evaluator code in `internal/adapter/outbound/`. When implementing, place under `internal/adapter/outbound/policy/`.
- Glob/regex used in `allowedBranches`: `release/*`, `hotfix/*` — interpret as shell-glob (single segment); reuse `path.Match` in the evaluator implementation.
- Freeze windows shape (when added): expect `{from: <RFC3339>, to: <RFC3339>, environments: [...], reason: <string>}`.

## Dependencies

### External
- YAML format; consumed by future `policy` adapter

<!-- MANUAL: -->

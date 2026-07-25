<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-19 | Updated: 2026-05-19 -->

# policies

## Purpose
Deployment policy definitions consumed by `port.PolicyEvaluator`. Encodes which branches/repos may deploy to which environment, and freeze windows blocking deployments entirely.

## Key Files
| File | Description |
|------|-------------|
| `default.yaml` | Three sections: `environmentRules` (branch/repo gates per environment) + `freezeWindows: []`; `tunableAllowlist` (J3 values-overlay knobs); `resourcePolicy` (J3 application dependencies) |

## For AI Agents

### Working In This Directory
- File path is referenced by `config.policies.configPath` (default `policies/default.yaml`).
- `allowedRepos: []` semantics: empty = allow all (per current schema).
- `environmentRules`/`freezeWindows` still have **no** `PolicyEvaluator` adapter — that section is structure-shaped configuration nothing reads. `tunableAllowlist` and `resourcePolicy` DO have loaders, both in `internal/adapter/outbound/policy/`.
- `tunableAllowlist` keys are **umbrella-relative** and every one is prefixed `hex-scaffold.` — the deploy unit is the umbrella and the app is a subchart aliased within it. A root-scoped key validates a path the chart never reads. Never add a bare `hex-scaffold` entry: matching is prefix-based, so one blanket entry makes the whole subchart tunable.
- The two J3 sections resolve per environment the same way: an environment listed under `environments` uses its own rules **in full**, and one that is absent falls back to `default`. Key presence decides, not content — `production.allowedTypes: []` under `resourcePolicy` is a deliberate deny-everything, and removing the key would silently grant the default.
- The two J3 sections default to **opposite** modes on purpose: `tunableAllowlist` is audit-first (a bad tuning value mistunes a workload), `resourcePolicy` is enforce-first (a bad resource request spends money on real Azure infrastructure). An empty `resourcePolicy.mode` means enforce; a missing `resourcePolicy` section refuses every resource request.
- Glob/regex used in `allowedBranches`: `release/*`, `hotfix/*` — interpret as shell-glob (single segment); reuse `path.Match` in the evaluator implementation.
- Freeze windows shape (when added): expect `{from: <RFC3339>, to: <RFC3339>, environments: [...], reason: <string>}`.

## Dependencies

### External
- YAML format; consumed by future `policy` adapter

<!-- MANUAL: -->

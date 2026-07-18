<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-19 | Updated: 2026-05-19 -->

# config

## Purpose
Strongly-typed config struct + YAML loader. `os.ExpandEnv` runs over the file contents before unmarshal — supports `${VAR}` and `${VAR:-default}` substitution. Applies defaults then validates required fields.

## Key Files
| File | Description |
|------|-------------|
| `config.go` | `Config{Server, Auth(OIDC), ArgoCD, OCI, GitHub, DocDB, OTel, Policies, Deploy}` with YAML tags. `Load(path)`: read → expand env → unmarshal → `applyDefaults` → `validate`. Secrets (`ArgoCD.Token`, `GitHub.Token`, `DocDB.ConnectionString`) carry `yaml:"-"` to keep them out of file serialization and must be set via env. `validate` requires: `auth.oidc.audience`, `argocd.serverURL`, `oci.registry`, `otel.serviceName` |

## For AI Agents

### Working In This Directory
- **Secret fields** (`yaml:"-"`): currently not auto-populated from env by the loader. `os.ExpandEnv` works on yaml *value* placeholders (`${VAR}` patterns inside the file), so secret fields are populated **only if** the config file contains placeholders like `token: "${ARGOCD_TOKEN}"`. The repo's `config.yaml` does this for `GitHub.token` and `DocDB.endpoint` but **not** `ArgoCD.token` — wire one explicitly or set the field in `cmd/server/main.go` after `Load`.
- `os.ExpandEnv` syntax for default values is `${VAR:-default}` (not `${VAR-default}` or `${VAR:default}`). The file uses this form.
- Defaults applied in `applyDefaults`: server `:8080` / 30s read / 60s write, argocd appNamespace `argocd`, OIDC issuer `https://token.actions.githubusercontent.com` / JWKS TTL 24h, OTel sample 1.0, deploy timeouts 600s/300s/20m/10s, DocDB collection `deployments`.
- Add a new field → add YAML tag → add default in `applyDefaults` → add required-check in `validate` if mandatory.

### Common Patterns
- Time fields are `time.Duration` (YAML accepts `30s`, `24h`).

## Dependencies

### External
- `gopkg.in/yaml.v3`, `os`, `fmt`, `time`

<!-- MANUAL: -->

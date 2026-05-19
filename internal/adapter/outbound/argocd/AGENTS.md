<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-19 | Updated: 2026-05-19 -->

# argocd

## Purpose
Implements `port.ArgoCD` against the Argo CD REST API. Validates AppProject allow-lists, creates/updates `Application` CRs via POST/PUT, triggers sync, polls health/sync status, deletes apps.

## Key Files
| File | Description |
|------|-------------|
| `client.go` | `Client{serverURL, token, httpClient(30s timeout), logger}`. `ValidateProject` reads `/api/v1/projects/{p}` and checks `sourceRepos` + `destinations` allow-lists (`*` wildcard). `CreateOrUpdate` POSTs to `/api/v1/applications`; on failure PUTs to `/api/v1/applications/{name}` with sync policy `automated{prune:true, selfHeal:false}`, syncOptions `[CreateNamespace=false, PruneLast=true, ApplyOutOfSyncOnly=true]`, retry limit 2 (10s→60s exp). `Sync` POSTs `/sync` with `{prune:true}`. `Status` returns `{Name, SyncStatus, HealthStatus, Message}`. `Delete` issues DELETE. `doRequest` handles bearer auth + 4xx/5xx → wrapped error |

## For AI Agents

### Working In This Directory
- Auth: static bearer token at construction (`NewClient(serverURL, token, logger)`). Token rotation requires re-instantiation.
- Timeout is 30s per request, not per-operation. Sync waits for HTTP only, not for Argo to converge — caller must poll `Status`.
- `selfHeal: false` is intentional — platform owns lifecycle, no drift correction without explicit re-sync.
- `CreateOrUpdate` swallows create error and tries update; if both fail, both errors are returned.
- The Argo REST API requires the Application namespace (typically `argocd`) in `metadata.namespace` for upsert routing.

### Common Patterns
- All HTTP methods funnel through `doRequest(ctx, method, url, body)` — central error wrapping with `<METHOD> <URL> returned <code>`.

## Dependencies

### Internal
- `internal/application/port`

### External
- `net/http`, `encoding/json`, `time`, `log/slog`

<!-- MANUAL: -->

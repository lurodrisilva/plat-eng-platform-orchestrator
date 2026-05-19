<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-19 | Updated: 2026-05-19 -->

# errors

## Purpose
Negative-path API tests. Each `.bru` exercises one expected error class and asserts both the HTTP status and the `error.code` field of the JSON envelope (`{error: {code, message}, timestamp}`).

## Key Files
| File | Description |
|------|-------------|
| `folder.bru` | Folder metadata |
| `missing-auth.bru` | No `Authorization` → 401 `AUTHENTICATION_FAILED` |
| `invalid-token.bru` | Garbage OIDC JWT → 401 `AUTHENTICATION_FAILED` |
| `missing-required-fields.bru` | Strip required body fields → 422 `VALIDATION_ERROR` / `DEPLOYMENT_FAILED` |
| `invalid-json.bru` | Malformed body → 400 `VALIDATION_ERROR` |
| `invalid-image-digest.bru` | Digest missing `sha256:` prefix → 422 |
| `short-git-sha.bru` | `gitSha` <7 chars → 422 |
| `method-not-allowed.bru` | `DELETE /api/v1/deployments` → 405 |

## For AI Agents

### Working In This Directory
- Server currently returns 422 for any use-case error via `writeError(http.StatusUnprocessableEntity, "DEPLOYMENT_FAILED", ...)` — distinguish validation vs policy vs persistence by mapping `errors.Is(err, deployment.Err*)` to specific status codes before relying on fine-grained assertions.
- 405 test depends on `net/http.ServeMux` Go-1.22 method routing returning 405 (not 404) for matched paths with wrong methods — keep route patterns method-prefixed in `router.go`.

<!-- MANUAL: -->

<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-19 | Updated: 2026-05-19 -->

# github

## Purpose
Implements `port.ChartResolver` using the GitHub REST API. Lists repository tags, filters by semver validity and (optional) prerelease policy, selects highest matching version, downloads the tarball archive at that tag.

## Key Files
| File | Description |
|------|-------------|
| `resolver.go` | `ChartResolver{httpClient(30s), baseURL("https://api.github.com"), token, logger}`. `Resolve` parses `owner/repo` (strips `https://`, `github.com/`, `.git`), paginates `/repos/{o}/{r}/tags` 100-per-page, calls `selectHighestSemVer` (canonicalizes by adding `v` prefix, validates via `golang.org/x/mod/semver`, skips prereleases unless allowed, sorts descending), downloads `/repos/{o}/{r}/tarball/{tag}` capped at 100 MiB via `io.LimitReader`, returns `ResolvedChart{SourceRepository, ChartName, ResolvedVersion, ResolvedTag, ArchiveBytes}` |

## For AI Agents

### Working In This Directory
- **Constraint matching is a stub**: `filterByConstraint` returns all tags unchanged. Add real semver-range parsing (e.g., `Masterminds/semver/v3`) before relying on version constraints from `ChartSource.VersionConstraint()`.
- Tag pagination break condition: empty batch OR `len(batch) < 100` — protects against infinite loops on truncated APIs.
- Auth: optional bearer token (`NewChartResolver(token, logger)`). Token empty → unauthenticated rate limit (60 req/h per IP).
- Headers always include `Accept: application/vnd.github+json` + `X-GitHub-Api-Version: 2022-11-28`.
- Tarball size cap is 100 MiB — bump if charts grow.
- `defer resp.Body.Close()` inside the pagination loop will only close on function exit — convert to per-iteration close if memory pressure shows up.

### Common Patterns
- Helper `parseRepoRef` accepts `https://github.com/owner/repo.git`, `github.com/owner/repo`, or `owner/repo`.

## Dependencies

### Internal
- `internal/application/port`

### External
- `golang.org/x/mod/semver`, `net/http`, `encoding/json`, `io`, `sort`, `strings`, `time`

<!-- MANUAL: -->

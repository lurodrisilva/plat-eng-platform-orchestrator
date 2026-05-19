<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-19 | Updated: 2026-05-19 -->

# oci

## Purpose
Implements `port.ArtifactPublisher` using `helm.sh/helm/v3/pkg/registry`. Pushes packaged Helm chart bytes to an OCI registry under a deterministic reference `oci://{registry}/{prefix}/{appID}/charts/{chartName}:{version}`.

## Key Files
| File | Description |
|------|-------------|
| `publisher.go` | `Publisher{registryHost, repositoryPrefix, logger}`. `Publish(chartBytes, appID, chartName, version)` creates a Helm `registry.Client`, calls `Push(chartBytes, ref)`, returns `PublishedArtifact{OCIReference, Digest (manifest), Registry, Repository ("{appID}/charts/{chartName}"), Tag (version)}`. `buildRef` formats `oci://{host}[/{prefix}]/{appID}/charts/{chartName}:{version}`. Exposes `RegistryHost()` for callers that need the host string |

## For AI Agents

### Working In This Directory
- Helm `registry.NewClient()` is created **per Publish call** — for high-throughput, refactor to construct once in `NewPublisher` and reuse.
- Auth is **not wired**: `registry.NewClient()` uses default Docker credstore lookup. To inject explicit creds, build the client with `registry.ClientOptCredentialsFile(...)` or login via `client.Login(...)` before Push.
- Returned `Digest` comes from `result.Manifest.Digest` (manifest digest, not chart blob digest). Persist this with the deployment for integrity audit.
- Empty `repositoryPrefix` → omit prefix segment entirely (no double slash).

## Dependencies

### Internal
- `internal/application/port`

### External
- `helm.sh/helm/v3/pkg/registry`, `context`, `fmt`, `log/slog`

<!-- MANUAL: -->

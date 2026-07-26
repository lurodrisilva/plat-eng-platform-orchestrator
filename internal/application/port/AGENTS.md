<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-19 | Updated: 2026-05-19 -->

# port

## Purpose
Outbound port interfaces consumed by application use cases. Each file defines one capability boundary that the application requires. Concrete implementations live in `internal/adapter/outbound/<name>/` and `internal/adapter/inbound/**` (for `TokenValidator`).

## Key Files
| File | Description |
|------|-------------|
| `argocd.go` | `ArgoCD` interface — `ValidateProject`, `CreateOrUpdate`, `Sync`, `Status`, `Delete` + `ArgoAppSpec`/`ArgoAppStatus` DTOs |
| `artifact.go` | `ArtifactPublisher` — `Publish(chartBytes, appID, chartName, version) → PublishedArtifact` (OCI ref, digest) |
| `chart.go` | `ChartResolver.Resolve` (semver constraint match + tarball download) and `ChartComposer.Compose` (load + values merge + repackage) |
| `auth.go` | `TokenValidator.Validate(token) → OIDCClaims` for GitHub Actions OIDC |
| `policy.go` | `PolicyEvaluator.Evaluate(repo, gitRef, env) → PolicyDecision{Allowed, Reason}` |
| `allowlist.go` | `TunableAllowlist` — J3 values-overlay knob validation (audit/enforce) |
| `resource.go` | `ResourcePolicyEvaluator` — J3 application dependencies (ADR-0023), enforce-first |
| `scaffold.go` | `Scaffolder.Dispatch`/`RepoStatus` — the scaffold-new-app workflow_dispatch (ADR-0009) |
| `appartifacts.go` | `AppRepoReader.ReadUmbrellaChart` (chart name + app subchart values key, read from the app's repo) and `AppArtifactResolver.ResolveLatest` (published version + the image that version runs) |

## For AI Agents

### Working In This Directory
- Add new outbound capability → new file here first, then adapter in `internal/adapter/outbound/<name>/` with `var _ port.X = (*Impl)(nil)`.
- DTOs (`OIDCClaims`, `PublishedArtifact`, `ArgoAppSpec`, `ResolvedChart`, `ComposedChart`, `PolicyDecision`) live with their port — keep them flat, no methods.
- Don't reference `internal/domain` from port DTOs; ports speak in primitives + their own types, not aggregates.
- **Never hardcode `hex-scaffold` as an application's values key.** The scaffolder substitutes that token for the app's own name when it renders, so a scaffolded app's subchart is keyed `<app>`. `UmbrellaChart.AppValuesKey` is the value to thread through; a hardcoded alias addresses a key Helm discards in silence.
- A port whose absence would let the platform SPEND MONEY fails closed (see `resource.go`); a port whose absence only costs information degrades to an explicit "unavailable" (see `appartifacts.go`). Neither reports a reassuring default.

### Common Patterns
- Interface verbs: present-tense (`Resolve`, `Publish`, `Validate`).
- Status/Result types prefixed by their port noun (`ArgoAppStatus`, `PolicyDecision`).

## Dependencies

### External
- `context` only

<!-- MANUAL: -->

<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-19 | Updated: 2026-05-19 -->

# outbound

## Purpose
Driven adapters — concrete implementations of `application/port.*` interfaces. Each subdirectory is one capability talking to one external system.

## Subdirectories
| Directory | Purpose |
|-----------|---------|
| `argocd/` | `port.ArgoCD` over Argo CD REST API (`/api/v1/projects`, `/api/v1/applications`) (see `argocd/AGENTS.md`) |
| `github/` | `port.ChartResolver` over GitHub API (list tags, download tarball) (see `github/AGENTS.md`) |
| `oci/` | `port.ArtifactPublisher` via Helm's OCI registry client (see `oci/AGENTS.md`) |
| `persistence/` | `domain/deployment.Repository` over MongoDB / Azure DocumentDB (see `persistence/AGENTS.md`) |

## For AI Agents

### Working In This Directory
- Each adapter file ends with `var _ port.X = (*Impl)(nil)` (or `var _ deployment.Repository = ...`) for compile-time conformance.
- Adapters never import each other.
- Logger always passed in via constructor — no package-level loggers.

<!-- MANUAL: -->

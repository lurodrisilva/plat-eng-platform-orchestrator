<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-19 | Updated: 2026-05-19 -->

# deployments

## Purpose
Bruno tests for the deployment API surface (`POST /api/v1/deployments`, `GET /api/v1/deployments/{id}`). Organized by scenario: happy paths in `create/`, retrieval in `status/`, expected 4xx in `errors/`.

## Subdirectories
| Directory | Purpose |
|-----------|---------|
| `create/` | POST happy paths (full payload, chart constraint, minimal values) (see `create/AGENTS.md`) |
| `status/` | GET by id + 404 (see `status/AGENTS.md`) |
| `errors/` | 400/401/405/422 negative tests (see `errors/AGENTS.md`) |

## Key Files
| File | Description |
|------|-------------|
| `folder.bru` | Folder metadata |

<!-- MANUAL: -->

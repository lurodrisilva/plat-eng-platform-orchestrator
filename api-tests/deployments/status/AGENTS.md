<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-19 | Updated: 2026-05-19 -->

# status

## Purpose
Tests for `GET /api/v1/deployments/{id}`.

## Key Files
| File | Description |
|------|-------------|
| `folder.bru` | Folder metadata |
| `get-deployment.bru` | Retrieve a known deployment (uses `deploymentId` chained from `create/`); asserts DTO fields (id, applicationId, environment, status, startedAt) |
| `get-nonexistent.bru` | Bogus id → asserts 404 + `error.code: NOT_FOUND` |

## For AI Agents

### Working In This Directory
- Run order matters: `create/*` must run first to populate `deploymentId` (Bruno preserves vars across run). Use `bru run --env local` over the whole `deployments/` folder.

<!-- MANUAL: -->

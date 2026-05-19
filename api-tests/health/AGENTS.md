<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-19 | Updated: 2026-05-19 -->

# health

## Purpose
Tests for liveness (`/healthz`) and readiness (`/readyz`) probes.

## Key Files
| File | Description |
|------|-------------|
| `folder.bru` | Folder metadata for Bruno |
| `liveness.bru` | `GET /healthz` — asserts 200, body `{status: "ok"}`, `<500ms`, content-type JSON |
| `readiness.bru` | `GET /readyz` — analogous readiness assertions |

## For AI Agents

### Working In This Directory
- Both probes currently return static JSON — when readiness gets real dependency checks (Mongo ping, ArgoCD reachable), extend `readiness.bru` to cover degraded paths (503).

<!-- MANUAL: -->

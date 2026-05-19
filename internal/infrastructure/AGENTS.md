<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-19 | Updated: 2026-05-19 -->

# infrastructure

## Purpose
Cross-cutting setup that isn't a domain port: configuration loading + OpenTelemetry tracing + structured logging bootstrap.

## Subdirectories
| Directory | Purpose |
|-----------|---------|
| `config/` | YAML + env-var config loader with defaults + validation (see `config/AGENTS.md`) |
| `telemetry/` | OTel tracer provider, slog JSON logger, trace-id log enrichment (see `telemetry/AGENTS.md`) |

## For AI Agents

### Working In This Directory
- Don't import `application` or `adapter` from here — infrastructure is a leaf.
- Add a new infrastructure concern (metrics, secrets cache, feature flags) as a sibling subdirectory.

<!-- MANUAL: -->

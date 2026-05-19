<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-19 | Updated: 2026-05-19 -->

# middleware

## Purpose
HTTP middleware: OpenTelemetry trace span per request, structured access logging with trace correlation, and panic recovery.

## Key Files
| File | Description |
|------|-------------|
| `tracing.go` | `Tracing` (starts OTel span `<METHOD> <path>` with `http.method`/`http.url` attrs); `Logging(logger)` (wraps `ResponseWriter` to capture status, logs method/path/status/duration with trace fields via `telemetry.Enrich`); `Recovery(logger)` (recovers panics → logs + 500 JSON error); `statusWriter` (captures response code) |

## For AI Agents

### Working In This Directory
- Tracer is package-level `_tracer = otel.Tracer("platform-orchestrator/http")` — change tracer name with care; backend dashboards may key on it.
- Wrap order in `router.go` is `Recovery(Logging(Tracing(mux)))` (outer-first). If a panic occurs inside `Logging`, recovery still catches it because Recovery is outermost.
- `statusWriter` captures only the first `WriteHeader` call (Go default). Don't call it twice.
- Recovery response is hardcoded JSON literal — keep in sync with the error envelope in `handler/response.go` if that shape changes.

### Common Patterns
- All three middlewares return `http.Handler` and operate as closures `func(http.Handler) http.Handler`.

## Dependencies

### Internal
- `internal/infrastructure/telemetry`

### External
- `go.opentelemetry.io/otel`, `go.opentelemetry.io/otel/attribute`, `go.opentelemetry.io/otel/trace`

<!-- MANUAL: -->

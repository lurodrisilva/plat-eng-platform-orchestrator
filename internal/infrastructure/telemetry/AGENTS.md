<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-19 | Updated: 2026-05-19 -->

# telemetry

## Purpose
OTel tracer provider bootstrap + structured `slog` JSON logger + helper to enrich a logger with `trace_id` / `span_id` from the current span context.

## Key Files
| File | Description |
|------|-------------|
| `otel.go` | `Init(ctx, cfg.OTel) (*sdktrace.TracerProvider, error)` — builds OTLP gRPC exporter (Insecure optional), merges default resource with `service.name`, picks `AlwaysSample` (rate ≥1.0) or `TraceIDRatioBased(rate)`, wires `TracerProvider` + composite propagator (`TraceContext` + `Baggage`). `NewLogger(serviceName)` — JSON `slog` to stdout at Info level, adds `service=<name>` attr, sets default. `Enrich(ctx, logger)` — returns logger with `trace_id`+`span_id` when span context is valid, else passthrough |

## For AI Agents

### Working In This Directory
- Server / worker should call `Init` once and `defer tp.Shutdown(ctx)` with a 5s timeout.
- Exporter is **OTLP gRPC only**. Switching to HTTP requires `otlptracehttp` import + endpoint scheme handling.
- Default sampler when `TracesSampleRate >= 1.0` is `AlwaysSample`; below 1.0 uses `TraceIDRatioBased`. Sampling decisions are head-based; no tail sampling.
- `NewLogger` calls `slog.SetDefault(logger)` — that means after `Init`, `slog.Info` anywhere in the process picks up the service attr.
- `Enrich` returns the original logger when there is no active span — safe to call unconditionally.
- Semconv import pinned to `semconv/v1.24.0`. Bumping the schema version requires changing the URL too.

## Dependencies

### Internal
- `internal/infrastructure/config` (OTel sub-config struct only)

### External
- `go.opentelemetry.io/otel`, `.../exporters/otlp/otlptrace/otlptracegrpc`, `.../propagation`, `.../sdk/{resource,trace}`, `.../semconv/v1.24.0`, `.../trace`, `log/slog`

<!-- MANUAL: -->

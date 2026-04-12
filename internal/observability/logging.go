package observability

import (
	"context"
	"log/slog"
	"os"

	"go.opentelemetry.io/otel/trace"
)

// InitLogging configures structured JSON logging via slog.
func InitLogging(serviceName string) *slog.Logger {
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level:     slog.LevelInfo,
		AddSource: false,
	})

	logger := slog.New(handler).With(
		slog.String("service", serviceName),
	)

	slog.SetDefault(logger)
	return logger
}

// EnrichWithTrace returns a logger enriched with trace context and deployment metadata.
func EnrichWithTrace(ctx context.Context, logger *slog.Logger) *slog.Logger {
	span := trace.SpanFromContext(ctx)
	sc := span.SpanContext()

	enriched := logger
	if sc.HasTraceID() {
		enriched = enriched.With(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}

	return enriched
}

// EnrichWithDeployment returns a logger enriched with deployment-specific fields.
func EnrichWithDeployment(logger *slog.Logger, deploymentID, application, environment string) *slog.Logger {
	return logger.With(
		slog.String("deployment_id", deploymentID),
		slog.String("application", application),
		slog.String("environment", environment),
	)
}

package api

import (
	"log/slog"
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/myorg/platform-orchestrator/internal/observability"
)

var apiTracer = otel.Tracer("platform-orchestrator/api")

// TracingMiddleware adds OpenTelemetry trace context to HTTP requests.
func TracingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, span := apiTracer.Start(r.Context(), r.Method+" "+r.URL.Path,
			trace.WithAttributes(
				attribute.String("http.method", r.Method),
				attribute.String("http.url", r.URL.String()),
				attribute.String("http.user_agent", r.UserAgent()),
			),
		)
		defer span.End()

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// LoggingMiddleware logs request/response details.
func LoggingMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			enriched := observability.EnrichWithTrace(r.Context(), logger)

			rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
			next.ServeHTTP(rw, r)

			enriched.InfoContext(r.Context(), "request completed",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rw.statusCode),
				slog.Duration("duration", time.Since(start)),
				slog.String("remote_addr", r.RemoteAddr),
			)
		})
	}
}

// RecoverMiddleware recovers from panics and returns a 500 error.
func RecoverMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					logger.ErrorContext(r.Context(), "panic recovered",
						slog.Any("panic", rec),
						slog.String("path", r.URL.Path),
					)
					writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", r.Header.Get("X-Request-ID"))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

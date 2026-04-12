package api

import (
	"log/slog"
	"net/http"
)

// NewRouter creates the HTTP router with all routes and middleware.
func NewRouter(handler *Handler, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()

	// Health endpoints (no auth required)
	mux.HandleFunc("GET /healthz", handler.HealthCheck)
	mux.HandleFunc("GET /readyz", handler.ReadyCheck)

	// Deployment API
	mux.HandleFunc("POST /api/v1/deployments", handler.CreateDeployment)
	mux.HandleFunc("GET /api/v1/deployments/{id}", handler.GetDeployment)

	// Apply middleware stack: recover → logging → tracing
	var h http.Handler = mux
	h = TracingMiddleware(h)
	h = LoggingMiddleware(logger)(h)
	h = RecoverMiddleware(logger)(h)

	return h
}

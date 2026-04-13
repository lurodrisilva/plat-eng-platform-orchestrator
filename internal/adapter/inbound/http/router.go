package http

import (
	"log/slog"
	"net/http"

	"github.com/myorg/platform-orchestrator/internal/adapter/inbound/http/handler"
	"github.com/myorg/platform-orchestrator/internal/adapter/inbound/http/middleware"
)

// NewRouter creates the HTTP router with all routes and middleware.
func NewRouter(deployment *handler.Deployment, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()

	health := &handler.Health{}
	mux.HandleFunc("GET /healthz", health.Liveness)
	mux.HandleFunc("GET /readyz", health.Readiness)

	mux.HandleFunc("POST /api/v1/deployments", deployment.Create)
	mux.HandleFunc("GET /api/v1/deployments/{id}", deployment.Status)

	var h http.Handler = mux
	h = middleware.Tracing(h)
	h = middleware.Logging(logger)(h)
	h = middleware.Recovery(logger)(h)

	return h
}

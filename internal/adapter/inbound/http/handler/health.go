package handler

import "net/http"

// Health handles liveness and readiness probes.
type Health struct{}

// Liveness handles GET /healthz.
func (h *Health) Liveness(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Readiness handles GET /readyz.
func (h *Health) Readiness(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

package http

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/myorg/platform-orchestrator/internal/adapter/inbound/http/handler"
	deploymentapp "github.com/myorg/platform-orchestrator/internal/application/deployment"
	"github.com/myorg/platform-orchestrator/internal/application/port"
)

// acceptingValidator stands in for a configured Entra verifier. It accepts any
// token, so a route reaching it can never be the reason a request 401s — only
// the absence of the header can be.
type acceptingValidator struct{}

func (acceptingValidator) Validate(context.Context, string) (port.OIDCClaims, error) {
	return port.OIDCClaims{}, nil
}

func testRouter() http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	deployment := handler.NewDeployment(deploymentapp.Application{}, acceptingValidator{}, nil, logger)
	apps := handler.NewApps(handler.AppsDeps{Validator: acceptingValidator{}, Logger: logger})
	return NewRouter(deployment, apps, logger)
}

// Every /api/v1 route must refuse an unauthenticated caller (P2).
//
// This is asserted at the ROUTER, not per handler, on purpose. Per-handler
// tests prove the handlers that exist today are closed; they say nothing about
// the next route somebody adds. Two of these were anonymous for exactly that
// reason — they were written as "read-only, no secrets" and nothing was
// watching the boundary as a whole.
//
// The validator here accepts every token, so a 401 can only come from the
// missing header. A route that answered 200 would be genuinely open.
func TestEveryAPIRouteRefusesAnonymousCallers(t *testing.T) {
	routes := []struct{ method, path string }{
		{http.MethodPost, "/api/v1/deployments"},
		{http.MethodPost, "/api/v1/deployments:validate"},
		{http.MethodGet, "/api/v1/deployments/dep-1"},
		{http.MethodPost, "/api/v1/deployments/dep-1/deploy"},
		{http.MethodPost, "/api/v1/apps"},
		{http.MethodGet, "/api/v1/apps/orders"},
	}

	router := testRouter()
	for _, rt := range routes {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			req := httptest.NewRequest(rt.method, rt.path, strings.NewReader(`{}`))
			rr := httptest.NewRecorder()
			router.ServeHTTP(rr, req)

			if rr.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401 — this route is reachable without a token", rr.Code)
			}
		})
	}
}

// The health probes must stay open. kubelet sends no Authorization header, so
// closing these would make the pod fail its own liveness check and crash-loop —
// a plausible over-correction when closing the API routes above.
func TestHealthProbesStayAnonymous(t *testing.T) {
	router := testRouter()
	for _, path := range []string{"/healthz", "/readyz"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rr := httptest.NewRecorder()
			router.ServeHTTP(rr, req)

			if rr.Code == http.StatusUnauthorized {
				t.Fatalf("%s answered 401; kubelet sends no token and the pod would crash-loop", path)
			}
		})
	}
}

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/myorg/platform-orchestrator/internal/adapter/outbound/policy"
	deploymentapp "github.com/myorg/platform-orchestrator/internal/application/deployment"
	"github.com/myorg/platform-orchestrator/internal/application/port"
)

var errStubReject = errors.New("stub: token rejected")

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newValidateHandler(t *testing.T, mode string) *Deployment {
	t.Helper()
	al, err := policy.NewTunableAllowlist(mode, []string{
		"replicaCount",
		"resources.requests.cpu",
		"resources.requests.memory",
	}, nil)
	if err != nil {
		t.Fatalf("NewTunableAllowlist: %v", err)
	}
	// app + validator are unused by the validate endpoint.
	return NewDeployment(deploymentapp.Application{}, nil, al, nil)
}

func postValidate(t *testing.T, h *Deployment, body string) (*httptest.ResponseRecorder, validateResponse) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/deployments:validate", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	h.ValidateTunables(rr, req)
	var resp validateResponse
	if rr.Code == http.StatusOK {
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v (body=%s)", err, rr.Body.String())
		}
	}
	return rr, resp
}

func TestValidateTunables_AllowedKnobs(t *testing.T) {
	h := newValidateHandler(t, policy.ModeEnforce)
	rr, resp := postValidate(t, h, `{"values":{"replicaCount":3,"resources":{"requests":{"cpu":"500m","memory":"1Gi"}}}}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if resp.Blocked {
		t.Error("allowed knobs must not be blocked")
	}
	if len(resp.Violations) != 0 {
		t.Errorf("violations = %v, want none", resp.Violations)
	}
}

func TestValidateTunables_LockedKnob_Enforce(t *testing.T) {
	h := newValidateHandler(t, policy.ModeEnforce)
	rr, resp := postValidate(t, h, `{"values":{"securityContext":{"runAsNonRoot":false}}}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !resp.Blocked {
		t.Error("locked knob in enforce mode must be blocked")
	}
	if len(resp.Violations) != 1 || resp.Violations[0] != "securityContext.runAsNonRoot" {
		t.Errorf("violations = %v, want [securityContext.runAsNonRoot]", resp.Violations)
	}
	if resp.Mode != policy.ModeEnforce {
		t.Errorf("mode = %q, want enforce", resp.Mode)
	}
}

func TestValidateTunables_LockedKnob_Audit(t *testing.T) {
	h := newValidateHandler(t, policy.ModeAudit)
	_, resp := postValidate(t, h, `{"values":{"securityContext":{"runAsNonRoot":false}}}`)
	if resp.Blocked {
		t.Error("audit mode must not block")
	}
	if len(resp.Violations) == 0 {
		t.Error("audit mode should still report violations for the UI to warn on")
	}
	if resp.Mode != policy.ModeAudit {
		t.Errorf("mode = %q, want audit", resp.Mode)
	}
}

func TestValidateTunables_PerEnvironment(t *testing.T) {
	// autoscaling is tunable in development but platform-locked in production;
	// the same overlay flips verdict on the environment field alone.
	al, err := policy.NewTunableAllowlist(policy.ModeEnforce,
		[]string{"resources.requests.cpu"},
		map[string][]string{
			"production":  {"resources.requests.cpu"},
			"development": {"resources.requests.cpu", "autoscaling.maxReplicas"},
		})
	if err != nil {
		t.Fatalf("NewTunableAllowlist: %v", err)
	}
	h := NewDeployment(deploymentapp.Application{}, nil, al, nil)

	body := `{"environment":%q,"values":{"autoscaling":{"maxReplicas":20}}}`

	_, prod := postValidate(t, h, fmt.Sprintf(body, "production"))
	if !prod.Blocked {
		t.Error("autoscaling.maxReplicas must be blocked in production")
	}
	if prod.Environment != "production" {
		t.Errorf("environment echoed = %q, want production", prod.Environment)
	}

	_, dev := postValidate(t, h, fmt.Sprintf(body, "development"))
	if dev.Blocked {
		t.Error("autoscaling.maxReplicas must be tunable in development")
	}
	if len(dev.Violations) != 0 {
		t.Errorf("development violations = %v, want none", dev.Violations)
	}
}

func TestValidateTunables_BadJSON(t *testing.T) {
	h := newValidateHandler(t, policy.ModeEnforce)
	rr, _ := postValidate(t, h, `{not json`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestValidateTunables_NilAllowlist(t *testing.T) {
	h := NewDeployment(deploymentapp.Application{}, nil, nil, nil)
	rr, resp := postValidate(t, h, `{"values":{"securityContext":{"runAsNonRoot":false}}}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if resp.Blocked || len(resp.Violations) != 0 {
		t.Errorf("nil allowlist should report no violations, got %+v", resp)
	}
}

// --- Create auth boundary (ADR-0015) ---------------------------------------

// stubValidator lets a Create test drive the auth path without a real tenant.
type stubValidator struct {
	claims port.OIDCClaims
	err    error
}

func (s stubValidator) Validate(_ context.Context, _ string) (port.OIDCClaims, error) {
	return s.claims, s.err
}

func postCreate(t *testing.T, h *Deployment, authHeader, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/deployments", bytes.NewBufferString(body))
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	rr := httptest.NewRecorder()
	h.Create(rr, req)
	return rr
}

// A nil validator must fail closed with 401, never panic on the nil interface.
func TestCreate_NilValidator_FailsClosed(t *testing.T) {
	h := NewDeployment(deploymentapp.Application{}, nil, nil, discardLogger())
	rr := postCreate(t, h, "Bearer whatever", `{}`)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

// A configured validator with no bearer header → 401.
func TestCreate_MissingBearer_Returns401(t *testing.T) {
	h := NewDeployment(deploymentapp.Application{}, stubValidator{}, nil, discardLogger())
	rr := postCreate(t, h, "", `{}`)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

// A token the validator rejects → 401.
func TestCreate_InvalidToken_Returns401(t *testing.T) {
	h := NewDeployment(deploymentapp.Application{}, stubValidator{err: errStubReject}, nil, discardLogger())
	rr := postCreate(t, h, "Bearer bad", `{}`)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

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
	"github.com/myorg/platform-orchestrator/internal/domain/deployment"
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
	// The validate endpoint is authenticated, so it needs a validator that
	// accepts. Passing nil here would make every verdict test assert 401 and
	// pass for the wrong reason — the auth boundary itself is covered by the
	// dedicated 401 tests below, not by these.
	return NewDeployment(deploymentapp.Application{}, stubValidator{}, al, discardLogger())
}

func postValidate(t *testing.T, h *Deployment, body string) (*httptest.ResponseRecorder, validateResponse) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/deployments:validate", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer test-token")
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
	h := NewDeployment(deploymentapp.Application{}, stubValidator{}, al, discardLogger())

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
	h := NewDeployment(deploymentapp.Application{}, stubValidator{}, nil, discardLogger())
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

// --- declared application dependencies (S4, ADR-0023) ------------------------

// capturingRepo records the aggregate the use case persists, so a test can
// assert what the API actually created rather than only what it answered.
type capturingRepo struct{ saved *deployment.Deployment }

func (r *capturingRepo) Save(_ context.Context, d *deployment.Deployment) error {
	r.saved = d
	return nil
}
func (r *capturingRepo) FindByID(context.Context, deployment.DeploymentID) (*deployment.Deployment, error) {
	return nil, deployment.ErrNotFound
}
func (r *capturingRepo) FindByApplication(context.Context, string, string, int) ([]*deployment.Deployment, error) {
	return nil, nil
}

func createHandlerWith(repo *capturingRepo, rp port.ResourcePolicyEvaluator) *Deployment {
	app := deploymentapp.Application{
		Commands: deploymentapp.Commands{
			CreateDeployment: deploymentapp.NewCreateDeploymentHandler(repo, nil, nil, rp, discardLogger()),
		},
	}
	return NewDeployment(app, stubValidator{}, nil, discardLogger())
}

const createBodyWithNamedResource = `{
  "application": {"id": "orders-v3", "team": "payments"},
  "image": {"repository": "ghcr.io/acme/orders-v3", "tag": "latest",
            "digest": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
  "chart": {"repository": "ghcr.io/acme/helm-charts", "name": "orders-v3-umbrella", "versionConstraint": "*"},
  "target": {"environment": "development", "cluster": "aks-test",
             "namespace": "orders-v3-development", "appProject": "platform"},
  "resources": [{"type": "postgres", "name": "acct", "size": "small", "version": "16"}],
  "source": {"gitSha": "abcdef1234567890abcdef1234567890abcdef12"}
}`

// The reported symptom, at the API boundary: a caller naming the database is
// IGNORED, not honoured, so the request cannot make the app and its database
// disagree. The request still succeeds — refusing it would break a portal that
// echoes back what the API reported.
func TestCreate_CallerSuppliedResourceNameIsIgnored(t *testing.T) {
	repo := &capturingRepo{}
	rr := postCreate(t, createHandlerWith(repo, allowResources{}), "Bearer good", createBodyWithNamedResource)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body=%s)", rr.Code, rr.Body.String())
	}
	if repo.saved == nil {
		t.Fatal("nothing was persisted")
	}

	resources := repo.saved.Resources()
	if len(resources) != 1 {
		t.Fatalf("persisted %d resources, want 1", len(resources))
	}
	if resources[0].Name() != "orders-v3" {
		t.Errorf("resource name = %q, want orders-v3 (derived) — the caller sent \"acct\"", resources[0].Name())
	}
	if resources[0].DatabaseName() != "orders-v3-db" {
		t.Errorf("database name = %q, want orders-v3-db", resources[0].DatabaseName())
	}
}

// A refused dependency is its own 422 code, distinguishable from a malformed
// deployment: the first is a policy answer a different size or environment may
// change.
func TestCreate_RefusedResourceReturns422ResourceNotAllowed(t *testing.T) {
	repo := &capturingRepo{}
	rr := postCreate(t, createHandlerWith(repo, denyResources{}), "Bearer good", createBodyWithNamedResource)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (body=%s)", rr.Code, rr.Body.String())
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte("RESOURCE_NOT_ALLOWED")) {
		t.Errorf("body = %s, want the RESOURCE_NOT_ALLOWED code", rr.Body.String())
	}
	if repo.saved != nil {
		t.Error("a refused deployment must not be persisted")
	}
}

// --- the dry-run must agree with the create boundary (S7) --------------------

// After ADR-0023 reserved `sqldatabase.*`, the validate endpoint answered
// "not blocked" for an overlay that create refuses outright. A dry-run that
// disagrees with create is worse than no dry-run: the portal would offer a
// Deploy button for a request that cannot succeed. Reserved paths are refused
// regardless of allowlist mode, so audit mode must NOT soften this one.
func TestValidateTunables_ReservedPathIsBlockedEvenInAuditMode(t *testing.T) {
	h := newValidateHandler(t, "audit")

	rr, resp := postValidate(t, h, `{"environment":"development","values":{"sqldatabase":{"enabled":true}}}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if resp.Mode != "audit" {
		t.Fatalf("mode = %q, want audit", resp.Mode)
	}
	if !resp.Blocked {
		t.Error("a reserved path is refused at create regardless of mode; the dry-run must say so")
	}
	if len(resp.Violations) == 0 || resp.Violations[0] != "sqldatabase" {
		t.Errorf("violations = %v, want the reserved path reported first", resp.Violations)
	}
}

// The app's bind is the other reserved half — it decides which database's
// credentials the pod reads.
func TestValidateTunables_ReservedBindPathIsBlocked(t *testing.T) {
	h := newValidateHandler(t, "audit")

	_, resp := postValidate(t, h, `{"environment":"development","values":{"hex-scaffold":{"postgres":{"bindBuildingBlock":{"instanceName":"someone-elses-db"}}}}}`)
	if !resp.Blocked {
		t.Error("the bind is platform-owned and must be reported as blocked")
	}
	found := false
	for _, v := range resp.Violations {
		if v == "hex-scaffold.postgres.bindBuildingBlock" {
			found = true
		}
	}
	if !found {
		t.Errorf("violations = %v, want the bind path named", resp.Violations)
	}
}

// A clean overlay stays clean — the reserved check must not add noise to the
// common case.
func TestValidateTunables_CleanOverlayIsNotBlockedByTheReservedCheck(t *testing.T) {
	h := newValidateHandler(t, "enforce")

	_, resp := postValidate(t, h, `{"environment":"development","values":{"replicaCount":3}}`)
	if resp.Blocked {
		t.Errorf("an allowlisted overlay must not be blocked: %v", resp.Violations)
	}
	if len(resp.Violations) != 0 {
		t.Errorf("violations = %v, want none", resp.Violations)
	}
}

// --- P2: the two formerly-anonymous routes ----------------------------------
//
// Status and ValidateTunables answered 200 to a caller with no Authorization
// header at all. Neither mutates anything, which is why it was tolerated, and
// neither is public: Status returns an application's identity, team and
// resources, and ValidateTunables returns a readout of the governance rules —
// which knobs are locked per environment, which paths are reserved.
//
// Each route is asserted three ways. 401-on-missing alone would pass against a
// handler that rejects an absent header but accepts any garbage string, and
// 200-on-valid is what stops a "fix" that simply refuses everything.

func getDeploymentStatus(t *testing.T, h *Deployment, authHeader string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/deployments/dep-1", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	req.SetPathValue("id", "dep-1")
	rr := httptest.NewRecorder()
	h.Status(rr, req)
	return rr
}

func TestStatus_MissingBearer_Returns401(t *testing.T) {
	h := NewDeployment(deploymentapp.Application{}, stubValidator{}, nil, discardLogger())
	if rr := getDeploymentStatus(t, h, ""); rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestStatus_InvalidToken_Returns401(t *testing.T) {
	h := NewDeployment(deploymentapp.Application{}, stubValidator{err: errStubReject}, nil, discardLogger())
	if rr := getDeploymentStatus(t, h, "Bearer bad"); rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

// A nil validator must fail closed here too, not panic on the nil interface.
func TestStatus_NilValidator_FailsClosed(t *testing.T) {
	h := NewDeployment(deploymentapp.Application{}, nil, nil, discardLogger())
	if rr := getDeploymentStatus(t, h, "Bearer whatever"); rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

// With a valid token the request reaches the query and fails on the missing
// deployment instead — 404, not 401. That distinction is the point: it proves
// the auth boundary passed rather than that everything is refused.
func TestStatus_ValidToken_ReachesTheQuery(t *testing.T) {
	app := deploymentapp.Application{
		Queries: deploymentapp.Queries{
			// capturingRepo.FindByID always answers ErrNotFound, which is exactly
			// what makes this assertion meaningful: reaching it at all proves the
			// request got past authenticate.
			GetDeployment: deploymentapp.NewGetDeploymentHandler(&capturingRepo{}),
		},
	}
	h := NewDeployment(app, stubValidator{}, nil, discardLogger())
	rr := getDeploymentStatus(t, h, "Bearer good")
	if rr.Code == http.StatusUnauthorized {
		t.Fatalf("status = 401 with a valid token; the auth boundary is refusing everything")
	}
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for an unknown deployment", rr.Code)
	}
}

func TestValidateTunables_MissingBearer_Returns401(t *testing.T) {
	h := newValidateHandler(t, policy.ModeEnforce)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/deployments:validate",
		bytes.NewBufferString(`{"values":{"replicaCount":3}}`))
	rr := httptest.NewRecorder()
	h.ValidateTunables(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestValidateTunables_InvalidToken_Returns401(t *testing.T) {
	al, err := policy.NewTunableAllowlist(policy.ModeEnforce, []string{"replicaCount"}, nil)
	if err != nil {
		t.Fatalf("NewTunableAllowlist: %v", err)
	}
	h := NewDeployment(deploymentapp.Application{}, stubValidator{err: errStubReject}, al, discardLogger())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/deployments:validate",
		bytes.NewBufferString(`{"values":{"replicaCount":3}}`))
	req.Header.Set("Authorization", "Bearer bad")
	rr := httptest.NewRecorder()
	h.ValidateTunables(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestValidateTunables_NilValidator_FailsClosed(t *testing.T) {
	h := NewDeployment(deploymentapp.Application{}, nil, nil, discardLogger())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/deployments:validate",
		bytes.NewBufferString(`{"values":{}}`))
	req.Header.Set("Authorization", "Bearer whatever")
	rr := httptest.NewRecorder()
	h.ValidateTunables(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

// The verdict path still works for an authenticated caller. Without this, every
// assertion above is satisfied by a handler that 401s unconditionally.
func TestValidateTunables_ValidToken_Returns200(t *testing.T) {
	h := newValidateHandler(t, policy.ModeEnforce)
	rr, resp := postValidate(t, h, `{"values":{"replicaCount":3}}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if resp.Blocked {
		t.Error("an allowed knob must not be blocked")
	}
}

// A nil logger must not panic. Every route now runs through authenticate, which
// logs on its refusal paths, and telemetry.Enrich passes a nil logger straight
// through when the context carries no trace — so the endpoint that crashed
// would have been the one refusing an unauthenticated caller.
func TestNilLogger_AuthRefusalDoesNotPanic(t *testing.T) {
	h := NewDeployment(deploymentapp.Application{}, nil, nil, nil)
	if rr := getDeploymentStatus(t, h, "Bearer whatever"); rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

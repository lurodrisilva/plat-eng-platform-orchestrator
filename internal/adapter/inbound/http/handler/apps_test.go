package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/myorg/platform-orchestrator/internal/application/port"
)

// fakeScaffolder drives the Apps handler without a real GitHub App.
type fakeScaffolder struct {
	dispatchResult port.ScaffoldResult
	dispatchErr    error
	statusResult   port.RepoStatus
	statusErr      error

	gotReq  port.ScaffoldRequest
	gotName string
}

func (f *fakeScaffolder) Dispatch(_ context.Context, req port.ScaffoldRequest) (port.ScaffoldResult, error) {
	f.gotReq = req
	return f.dispatchResult, f.dispatchErr
}

func (f *fakeScaffolder) RepoStatus(_ context.Context, name string) (port.RepoStatus, error) {
	f.gotName = name
	return f.statusResult, f.statusErr
}

func postApps(t *testing.T, h *Apps, authHeader, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps", bytes.NewBufferString(body))
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	rr := httptest.NewRecorder()
	h.Create(rr, req)
	return rr
}

func TestApps_Create_NilValidator_FailsClosed(t *testing.T) {
	h := NewApps(AppsDeps{Scaffolder: &fakeScaffolder{}, Logger: discardLogger()})
	rr := postApps(t, h, "Bearer whatever", `{"name":"my-app"}`)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestApps_Create_MissingBearer_Returns401(t *testing.T) {
	h := NewApps(AppsDeps{Scaffolder: &fakeScaffolder{}, Validator: stubValidator{}, Logger: discardLogger()})
	rr := postApps(t, h, "", `{"name":"my-app"}`)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestApps_Create_InvalidToken_Returns401(t *testing.T) {
	h := NewApps(AppsDeps{Scaffolder: &fakeScaffolder{}, Validator: stubValidator{err: errStubReject}, Logger: discardLogger()})
	rr := postApps(t, h, "Bearer bad", `{"name":"my-app"}`)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestApps_Create_NilScaffolder_Returns503(t *testing.T) {
	h := NewApps(AppsDeps{Validator: stubValidator{}, Logger: discardLogger()})
	rr := postApps(t, h, "Bearer good", `{"name":"my-app"}`)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
}

func TestApps_Create_HappyPath_Returns202(t *testing.T) {
	fake := &fakeScaffolder{
		dispatchResult: port.ScaffoldResult{
			AppName:    "my-app",
			RepoName:   "my-app-t3st",
			Repository: "lurodrisilva/my-app-t3st",
			RepoURL:    "https://github.com/lurodrisilva/my-app-t3st",
		},
	}
	h := NewApps(AppsDeps{Scaffolder: fake, Validator: stubValidator{claims: port.OIDCClaims{ObjectID: "obj-1"}}, Logger: discardLogger()})
	rr := postApps(t, h, "Bearer good", `{"name":"my-app","team":"payments"}`)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body=%s)", rr.Code, rr.Body.String())
	}

	var resp map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["repository"] != "lurodrisilva/my-app-t3st" {
		t.Errorf("repository = %q, want lurodrisilva/my-app-t3st", resp["repository"])
	}
	// Poll URL must target the suffixed repo name, not the clean app name.
	if resp["statusUrl"] != "/api/v1/apps/my-app-t3st" {
		t.Errorf("statusUrl = %q, want /api/v1/apps/my-app-t3st", resp["statusUrl"])
	}
	if resp["appName"] != "my-app" {
		t.Errorf("appName = %q, want my-app (clean identity)", resp["appName"])
	}
	// Domain defaults to "account" when omitted; the actor is the principal.
	if fake.gotReq.Domain != "account" {
		t.Errorf("dispatched domain = %q, want account (default)", fake.gotReq.Domain)
	}
	if fake.gotReq.Actor != "obj-1" {
		t.Errorf("dispatched actor = %q, want obj-1", fake.gotReq.Actor)
	}
}

func TestApps_Create_InvalidName_Returns400(t *testing.T) {
	fake := &fakeScaffolder{dispatchErr: errors.New(`invalid app name "Bad": must be lowercase`)}
	h := NewApps(AppsDeps{Scaffolder: fake, Validator: stubValidator{}, Logger: discardLogger()})
	rr := postApps(t, h, "Bearer good", `{"name":"Bad"}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestApps_Status_HappyPath_Returns200(t *testing.T) {
	fake := &fakeScaffolder{
		statusResult: port.RepoStatus{
			Name:          "my-app",
			Exists:        true,
			RepoURL:       "https://github.com/lurodrisilva/my-app",
			DefaultBranch: "main",
		},
	}
	h := NewApps(AppsDeps{Scaffolder: fake, Validator: stubValidator{}, Logger: discardLogger()})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/my-app", nil)
	req.Header.Set("Authorization", "Bearer good")
	req.SetPathValue("name", "my-app")
	rr := httptest.NewRecorder()
	h.Status(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["ready"] != true {
		t.Errorf("ready = %v, want true", resp["ready"])
	}
	if resp["defaultBranch"] != "main" {
		t.Errorf("defaultBranch = %v, want main", resp["defaultBranch"])
	}
	if fake.gotName != "my-app" {
		t.Errorf("RepoStatus called with %q, want my-app", fake.gotName)
	}
}

func TestApps_Status_NilValidator_FailsClosed(t *testing.T) {
	h := NewApps(AppsDeps{Scaffolder: &fakeScaffolder{}, Logger: discardLogger()})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/my-app", nil)
	req.SetPathValue("name", "my-app")
	rr := httptest.NewRecorder()
	h.Status(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

// --- declared application dependencies (S4, ADR-0023) ------------------------

// allowResources / denyResources are canned resource-policy verdicts.
type allowResources struct{}

func (allowResources) Evaluate(context.Context, []port.ResourceRequest, string) port.ResourceDecision {
	return port.ResourceDecision{Mode: "enforce"}
}

type denyResources struct{}

func (denyResources) Evaluate(context.Context, []port.ResourceRequest, string) port.ResourceDecision {
	return port.ResourceDecision{Reject: true, Mode: "enforce",
		Violations: []string{`resource type "postgres" is not allowed in environment "" (allowed: none)`}}
}

// The create half of the passthrough: what the caller asked for becomes the
// scaffolded repo's default database shape.
func TestApps_Create_PassesTheDeclaredDatabaseToTheScaffolder(t *testing.T) {
	fake := &fakeScaffolder{dispatchResult: port.ScaffoldResult{AppName: "my-app", RepoName: "my-app-t3st"}}
	h := NewApps(AppsDeps{Scaffolder: fake, Validator: stubValidator{}, ResourcePolicy: allowResources{}, Logger: discardLogger()})

	rr := postApps(t, h, "Bearer good",
		`{"name":"my-app","team":"payments","resources":[{"type":"postgres","size":"medium","version":"15","storageMb":65536}]}`)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body=%s)", rr.Code, rr.Body.String())
	}
	if fake.gotReq.DBSize != "medium" || fake.gotReq.DBVersion != "15" || fake.gotReq.DBStorageMb != 65536 {
		t.Errorf("dispatched db shape = %s/%s/%d, want medium/15/65536",
			fake.gotReq.DBSize, fake.gotReq.DBVersion, fake.gotReq.DBStorageMb)
	}
}

// A create declaring no resources must dispatch exactly as before, leaving the
// workflow's own defaults to apply.
func TestApps_Create_NoResourcesLeavesTheScaffoldDefaults(t *testing.T) {
	fake := &fakeScaffolder{dispatchResult: port.ScaffoldResult{AppName: "my-app", RepoName: "my-app-t3st"}}
	h := NewApps(AppsDeps{Scaffolder: fake, Validator: stubValidator{}, Logger: discardLogger()})

	if rr := postApps(t, h, "Bearer good", `{"name":"my-app","team":"payments"}`); rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rr.Code)
	}
	if fake.gotReq.DBSize != "" || fake.gotReq.DBVersion != "" || fake.gotReq.DBStorageMb != 0 {
		t.Errorf("dispatched db shape = %s/%s/%d, want all empty",
			fake.gotReq.DBSize, fake.gotReq.DBVersion, fake.gotReq.DBStorageMb)
	}
}

// A refused dependency stops the scaffold before the repo exists — a repo whose
// very first deploy cannot succeed is worse than no repo.
func TestApps_Create_RefusedResourceReturns422AndDispatchesNothing(t *testing.T) {
	fake := &fakeScaffolder{}
	h := NewApps(AppsDeps{Scaffolder: fake, Validator: stubValidator{}, ResourcePolicy: denyResources{}, Logger: discardLogger()})

	rr := postApps(t, h, "Bearer good", `{"name":"my-app","team":"payments","resources":[{"type":"postgres"}]}`)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (body=%s)", rr.Code, rr.Body.String())
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte("RESOURCE_NOT_ALLOWED")) {
		t.Errorf("body = %s, want the RESOURCE_NOT_ALLOWED code", rr.Body.String())
	}
	if fake.gotReq.Name != "" {
		t.Errorf("a refused resource must not dispatch the scaffold workflow, but Dispatch saw %q", fake.gotReq.Name)
	}
}

// An unconfigured resource policy refuses rather than skipping the gate: the
// scaffolded default becomes a real Azure server on the app's first deploy.
func TestApps_Create_NilResourcePolicyRefusesADeclaredResource(t *testing.T) {
	h := NewApps(AppsDeps{Scaffolder: &fakeScaffolder{}, Validator: stubValidator{}, Logger: discardLogger()})

	rr := postApps(t, h, "Bearer good", `{"name":"my-app","team":"payments","resources":[{"type":"postgres"}]}`)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rr.Code)
	}
}

// A structurally invalid shape is a 400, not a 422: the caller can fix it by
// editing the request rather than by asking for less.
func TestApps_Create_InvalidResourceShapeReturns400(t *testing.T) {
	h := NewApps(AppsDeps{Scaffolder: &fakeScaffolder{}, Validator: stubValidator{}, ResourcePolicy: allowResources{}, Logger: discardLogger()})

	rr := postApps(t, h, "Bearer good",
		`{"name":"my-app","team":"payments","resources":[{"type":"postgres","size":"enormous"}]}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", rr.Code, rr.Body.String())
	}
}

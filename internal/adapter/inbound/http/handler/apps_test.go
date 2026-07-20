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
	h := NewApps(&fakeScaffolder{}, nil, discardLogger())
	rr := postApps(t, h, "Bearer whatever", `{"name":"my-app"}`)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestApps_Create_MissingBearer_Returns401(t *testing.T) {
	h := NewApps(&fakeScaffolder{}, stubValidator{}, discardLogger())
	rr := postApps(t, h, "", `{"name":"my-app"}`)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestApps_Create_InvalidToken_Returns401(t *testing.T) {
	h := NewApps(&fakeScaffolder{}, stubValidator{err: errStubReject}, discardLogger())
	rr := postApps(t, h, "Bearer bad", `{"name":"my-app"}`)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestApps_Create_NilScaffolder_Returns503(t *testing.T) {
	h := NewApps(nil, stubValidator{}, discardLogger())
	rr := postApps(t, h, "Bearer good", `{"name":"my-app"}`)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
}

func TestApps_Create_HappyPath_Returns202(t *testing.T) {
	fake := &fakeScaffolder{
		dispatchResult: port.ScaffoldResult{
			AppName:    "my-app",
			Repository: "lurodrisilva/my-app",
			RepoURL:    "https://github.com/lurodrisilva/my-app",
		},
	}
	h := NewApps(fake, stubValidator{claims: port.OIDCClaims{ObjectID: "obj-1"}}, discardLogger())
	rr := postApps(t, h, "Bearer good", `{"name":"my-app","team":"payments"}`)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body=%s)", rr.Code, rr.Body.String())
	}

	var resp map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["repository"] != "lurodrisilva/my-app" {
		t.Errorf("repository = %q, want lurodrisilva/my-app", resp["repository"])
	}
	if resp["statusUrl"] != "/api/v1/apps/my-app" {
		t.Errorf("statusUrl = %q, want /api/v1/apps/my-app", resp["statusUrl"])
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
	h := NewApps(fake, stubValidator{}, discardLogger())
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
	h := NewApps(fake, stubValidator{}, discardLogger())

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
	h := NewApps(&fakeScaffolder{}, nil, discardLogger())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/my-app", nil)
	req.SetPathValue("name", "my-app")
	rr := httptest.NewRecorder()
	h.Status(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

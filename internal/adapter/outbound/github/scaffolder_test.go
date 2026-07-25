package github

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/myorg/platform-orchestrator/internal/application/port"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newTestScaffolder builds a Scaffolder via the real constructor, then points
// both its own base URL and its AppAuth's base URL at the test server so every
// GitHub call (token mint + workflow + repo read) hits the httptest handler.
func newTestScaffolder(t *testing.T, baseURL string) *Scaffolder {
	t.Helper()
	keyPEM, _ := testPrivateKeyPEM(t)
	s, err := NewScaffolder(ScaffolderConfig{
		AppID:           12345,
		InstallationID:  67890,
		PrivateKeyPEM:   keyPEM,
		ScaffolderOwner: "lurodrisilva",
		ScaffolderRepo:  "net-hexagonal",
		WorkflowFile:    "scaffold-new-app.yml",
		Ref:             "main",
		NewRepoOwner:    "lurodrisilva",
	}, discardLogger())
	if err != nil {
		t.Fatalf("NewScaffolder: %v", err)
	}
	s.baseURL = baseURL
	s.auth.baseURL = baseURL
	// Deterministic repo suffix so dispatch/repo-name assertions are stable.
	s.newSuffix = func() string { return "t3st" }
	return s
}

// tokenHandler answers the installation-token mint so Dispatch/RepoStatus can
// proceed to the call under test.
func writeToken(w http.ResponseWriter) {
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{"token": "ghs_test", "expires_at": "2999-01-01T00:00:00Z"})
}

func TestNewScaffolder_ValidatesRequiredFields(t *testing.T) {
	keyPEM, _ := testPrivateKeyPEM(t)
	base := ScaffolderConfig{
		AppID:           1,
		InstallationID:  2,
		PrivateKeyPEM:   keyPEM,
		ScaffolderOwner: "o",
		ScaffolderRepo:  "r",
		WorkflowFile:    "w.yml",
		NewRepoOwner:    "n",
	}
	// Each mutation blanks one required field and must fail.
	mutations := map[string]func(*ScaffolderConfig){
		"appID":          func(c *ScaffolderConfig) { c.AppID = 0 },
		"installationID": func(c *ScaffolderConfig) { c.InstallationID = 0 },
		"privateKey":     func(c *ScaffolderConfig) { c.PrivateKeyPEM = "" },
		"owner":          func(c *ScaffolderConfig) { c.ScaffolderOwner = "" },
		"repo":           func(c *ScaffolderConfig) { c.ScaffolderRepo = "" },
		"workflowFile":   func(c *ScaffolderConfig) { c.WorkflowFile = "" },
		"newRepoOwner":   func(c *ScaffolderConfig) { c.NewRepoOwner = "" },
	}
	for name, mut := range mutations {
		cfg := base
		mut(&cfg)
		if _, err := NewScaffolder(cfg, discardLogger()); err == nil {
			t.Errorf("missing %s: expected error, got nil", name)
		}
	}

	// A valid config with an empty Ref defaults the ref to main.
	s, err := NewScaffolder(base, discardLogger())
	if err != nil {
		t.Fatalf("valid config: %v", err)
	}
	if s.ref != "main" {
		t.Errorf("default ref = %q, want main", s.ref)
	}
}

func TestDispatch_SendsCorrectBodyAndTreats204AsSuccess(t *testing.T) {
	var gotBody map[string]any
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/access_tokens") {
			writeToken(w)
			return
		}
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	s := newTestScaffolder(t, srv.URL)
	res, err := s.Dispatch(context.Background(), port.ScaffoldRequest{
		Name:        "my-app",
		Team:        "payments",
		Domain:      "account",
		Description: "a new app",
		Actor:       "dev@example.com",
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	wantPath := "/repos/lurodrisilva/net-hexagonal/actions/workflows/scaffold-new-app.yml/dispatches"
	if gotPath != wantPath {
		t.Errorf("path = %q, want %q", gotPath, wantPath)
	}
	if gotBody["ref"] != "main" {
		t.Errorf("ref = %v, want main", gotBody["ref"])
	}
	inputs, ok := gotBody["inputs"].(map[string]any)
	if !ok {
		t.Fatalf("inputs missing or wrong type: %v", gotBody["inputs"])
	}
	if inputs["appName"] != "my-app" {
		t.Errorf("inputs.appName = %v, want my-app", inputs["appName"])
	}
	if inputs["repoName"] != "my-app-t3st" {
		t.Errorf("inputs.repoName = %v, want my-app-t3st", inputs["repoName"])
	}
	if inputs["owner"] != "lurodrisilva" {
		t.Errorf("inputs.owner = %v, want lurodrisilva", inputs["owner"])
	}
	if inputs["domain"] != "account" {
		t.Errorf("inputs.domain = %v, want account", inputs["domain"])
	}
	if inputs["description"] != "a new app" {
		t.Errorf("inputs.description = %v, want 'a new app'", inputs["description"])
	}

	if res.AppName != "my-app" {
		t.Errorf("AppName = %q, want my-app", res.AppName)
	}
	if res.RepoName != "my-app-t3st" {
		t.Errorf("RepoName = %q, want my-app-t3st", res.RepoName)
	}
	if res.Repository != "lurodrisilva/my-app-t3st" {
		t.Errorf("Repository = %q, want lurodrisilva/my-app-t3st", res.Repository)
	}
	if res.RepoURL != "https://github.com/lurodrisilva/my-app-t3st" {
		t.Errorf("RepoURL = %q", res.RepoURL)
	}
}

// TestRepoNameFor_TrimsToBound verifies the suffix never pushes the repo name
// past nameRE's 39-char ceiling: a max-length app name is trimmed, not rejected.
func TestRepoNameFor_TrimsToBound(t *testing.T) {
	s := newTestScaffolder(t, "http://unused.invalid")
	long := strings.Repeat("a", 39)
	got := s.repoNameFor(long)
	if len(got) > maxRepoLen {
		t.Errorf("repoNameFor(%d chars) = %q (%d), want <= %d", len(long), got, len(got), maxRepoLen)
	}
	if !nameRE.MatchString(got) {
		t.Errorf("repoNameFor produced non-conforming name %q", got)
	}
	if !strings.HasSuffix(got, "-t3st") {
		t.Errorf("repoNameFor = %q, want -t3st suffix", got)
	}
}

func TestDispatch_Non204IsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/access_tokens") {
			writeToken(w)
			return
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"workflow not found"}`))
	}))
	defer srv.Close()

	s := newTestScaffolder(t, srv.URL)
	if _, err := s.Dispatch(context.Background(), port.ScaffoldRequest{Name: "my-app"}); err == nil {
		t.Fatal("expected error for non-204 dispatch response, got nil")
	}
}

func TestDispatch_RejectsBadNames(t *testing.T) {
	// No server needed: validation happens before any call.
	s := newTestScaffolder(t, "http://unused.invalid")
	bad := []string{"", "MyApp", "my_app", "my app", "-leading", strings.Repeat("a", 40), "app!"}
	for _, name := range bad {
		if _, err := s.Dispatch(context.Background(), port.ScaffoldRequest{Name: name}); err == nil {
			t.Errorf("Dispatch(%q): expected validation error, got nil", name)
		}
	}
}

func TestRepoStatus_200Exists(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/access_tokens") {
			writeToken(w)
			return
		}
		if r.URL.Path != "/repos/lurodrisilva/my-app" {
			t.Errorf("repo path = %q, want /repos/lurodrisilva/my-app", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"default_branch": "main",
			"html_url":       "https://github.com/lurodrisilva/my-app",
		})
	}))
	defer srv.Close()

	s := newTestScaffolder(t, srv.URL)
	st, err := s.RepoStatus(context.Background(), "my-app")
	if err != nil {
		t.Fatalf("RepoStatus: %v", err)
	}
	if !st.Exists {
		t.Error("Exists = false, want true for 200")
	}
	if st.DefaultBranch != "main" {
		t.Errorf("DefaultBranch = %q, want main", st.DefaultBranch)
	}
	if st.RepoURL != "https://github.com/lurodrisilva/my-app" {
		t.Errorf("RepoURL = %q", st.RepoURL)
	}
}

func TestRepoStatus_404NotExistsNotError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/access_tokens") {
			writeToken(w)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	}))
	defer srv.Close()

	s := newTestScaffolder(t, srv.URL)
	st, err := s.RepoStatus(context.Background(), "not-yet")
	if err != nil {
		t.Fatalf("RepoStatus: 404 must not be an error, got %v", err)
	}
	if st.Exists {
		t.Error("Exists = true, want false for 404")
	}
}

func TestRepoStatus_500IsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/access_tokens") {
			writeToken(w)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	s := newTestScaffolder(t, srv.URL)
	if _, err := s.RepoStatus(context.Background(), "my-app"); err == nil {
		t.Fatal("expected error for 500 repo status, got nil")
	}
}

func TestRepoStatus_RejectsBadName(t *testing.T) {
	s := newTestScaffolder(t, "http://unused.invalid")
	if _, err := s.RepoStatus(context.Background(), "Bad Name"); err == nil {
		t.Fatal("expected validation error for bad name, got nil")
	}
}

// dispatchInputs fires a Dispatch against a stub GitHub and returns the
// workflow_dispatch inputs it sent.
func dispatchInputs(t *testing.T, req port.ScaffoldRequest) map[string]any {
	t.Helper()
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/access_tokens") {
			writeToken(w)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	if _, err := newTestScaffolder(t, srv.URL).Dispatch(context.Background(), req); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	inputs, ok := gotBody["inputs"].(map[string]any)
	if !ok {
		t.Fatalf("inputs missing or wrong type: %v", gotBody["inputs"])
	}
	return inputs
}

// The POST /apps passthrough (S4, ADR-0023): the requested database shape
// becomes the scaffolded repo's default. workflow_dispatch inputs are strings
// on the wire even for a numeric input, so storage is formatted rather than
// sent as a number.
func TestDispatch_PassesTheDeclaredDatabaseShape(t *testing.T) {
	inputs := dispatchInputs(t, port.ScaffoldRequest{
		Name: "my-app", Team: "payments", Domain: "account",
		DBSize: "medium", DBVersion: "15", DBStorageMb: 65536,
	})

	if inputs["dbSize"] != "medium" {
		t.Errorf("inputs.dbSize = %v, want medium", inputs["dbSize"])
	}
	if inputs["dbVersion"] != "15" {
		t.Errorf("inputs.dbVersion = %v, want 15", inputs["dbVersion"])
	}
	if inputs["dbStorageMb"] != "65536" {
		t.Errorf("inputs.dbStorageMb = %v (%T), want the string \"65536\"", inputs["dbStorageMb"], inputs["dbStorageMb"])
	}
}

// An unset field must be OMITTED, not sent empty. A workflow's `default:` only
// applies to an input that was not provided at all; sending "" would override
// the default and the render would write an empty size into the umbrella.
func TestDispatch_OmitsUnsetDatabaseInputs(t *testing.T) {
	inputs := dispatchInputs(t, port.ScaffoldRequest{Name: "my-app", Team: "payments", Domain: "account"})

	for _, key := range []string{"dbSize", "dbVersion", "dbStorageMb"} {
		if v, present := inputs[key]; present {
			t.Errorf("inputs.%s = %v, want the key absent so the workflow default applies", key, v)
		}
	}
}

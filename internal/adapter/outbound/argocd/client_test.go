package argocd

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

// TestCreateOrUpdate_EnablesCreateNamespace locks the contract that the Argo CD
// Application is created with CreateNamespace=true. Each app deploys into a
// per-app/-env namespace (${appId}-${environment}) that does not pre-exist, so
// without this the first sync fails with `namespaces "<ns>" not found`.
func TestCreateOrUpdate_EnablesCreateNamespace(t *testing.T) {
	var captured []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/api/v1/applications") {
			captured, _ = io.ReadAll(r.Body)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-token", slog.New(slog.NewTextHandler(io.Discard, nil)))
	err := c.CreateOrUpdate(context.Background(), port.ArgoAppSpec{
		Name: "app", Namespace: "devops-system", Project: "default",
		RepoURL: "acr.example", Chart: "umbrella", Version: "1.0.0",
		DestServer: "https://kubernetes.default.svc", DestNS: "app-production",
	})
	if err != nil {
		t.Fatalf("CreateOrUpdate: %v", err)
	}
	if captured == nil {
		t.Fatal("no POST /api/v1/applications payload captured")
	}

	var app map[string]any
	if err := json.Unmarshal(captured, &app); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	spec, _ := app["spec"].(map[string]any)
	sp, _ := spec["syncPolicy"].(map[string]any)
	rawOpts, _ := sp["syncOptions"].([]any)
	var found bool
	for _, o := range rawOpts {
		if s, _ := o.(string); s == "CreateNamespace=true" {
			found = true
		}
		if s, _ := o.(string); s == "CreateNamespace=false" {
			t.Fatalf("syncOptions still has CreateNamespace=false: %v", rawOpts)
		}
	}
	if !found {
		t.Fatalf("syncOptions missing CreateNamespace=true: %v", rawOpts)
	}
}

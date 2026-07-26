package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// scaffoldedUmbrella is a real scaffolded app's Chart.yaml, abbreviated: the
// template's `hex-scaffold` names have been substituted for the app's own, which
// is exactly the case a hardcoded alias gets wrong.
const scaffoldedUmbrella = `apiVersion: v2
name: orders-v3-umbrella
type: application
version: 0.3.0
dependencies:
  - name: orders-v3
    version: "0.3.0"
    repository: "file://../helm/orders-v3"
  - name: plat-eng-sql-database-package
    alias: sqldatabase
    version: "0.2.0"
    repository: "oci://ghcr.io/lurodrisilva/helm-charts"
    condition: sqldatabase.enabled
`

// contentsServer serves the token mint plus one repository-contents response.
func contentsServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/access_tokens") {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"token":"ghs_test","expires_at":"2099-01-01T00:00:00Z"}`))
			return
		}
		if !strings.Contains(r.URL.Path, "/contents/deploy/umbrella/Chart.yaml") {
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

// githubContents wraps content the way the API does: base64, wrapped at 60
// columns. The wrapping is the point — the standard decoder rejects newlines.
func githubContents(raw string) string {
	enc := base64.StdEncoding.EncodeToString([]byte(raw))
	var wrapped strings.Builder
	for i := 0; i < len(enc); i += 60 {
		end := i + 60
		if end > len(enc) {
			end = len(enc)
		}
		wrapped.WriteString(enc[i:end])
		wrapped.WriteString("\n")
	}
	b, err := json.Marshal(map[string]string{"encoding": "base64", "content": wrapped.String()})
	if err != nil {
		panic(err)
	}
	return string(b)
}

func TestReadUmbrellaChart_ReportsChartNameAndAppValuesKey(t *testing.T) {
	srv := contentsServer(t, http.StatusOK, githubContents(scaffoldedUmbrella))
	defer srv.Close()
	s := newTestScaffolder(t, srv.URL)

	got, err := s.ReadUmbrellaChart(context.Background(), "orders-v3-fcdc")
	if err != nil {
		t.Fatalf("ReadUmbrellaChart: %v", err)
	}
	if got.ChartName != "orders-v3-umbrella" {
		t.Errorf("ChartName = %q, want orders-v3-umbrella", got.ChartName)
	}
	// The app subchart is the LOCAL (file://) dependency and it has no alias, so
	// its values key is its chart name — the app's own name, not `hex-scaffold`.
	if got.AppValuesKey != "orders-v3" {
		t.Errorf("AppValuesKey = %q, want orders-v3", got.AppValuesKey)
	}
}

// A missing file (or repository) is a state, not a failure: the scaffold workflow
// creates the repository before it pushes into it.
func TestReadUmbrellaChart_NotFound_IsNotAnError(t *testing.T) {
	srv := contentsServer(t, http.StatusNotFound, `{"message":"Not Found"}`)
	defer srv.Close()
	s := newTestScaffolder(t, srv.URL)

	got, err := s.ReadUmbrellaChart(context.Background(), "orders-v3-fcdc")
	if err != nil {
		t.Fatalf("404 should not be an error, got %v", err)
	}
	if got.ChartName != "" || got.AppValuesKey != "" {
		t.Errorf("got %+v, want a zero UmbrellaChart", got)
	}
}

// Anything else must surface. A 500 reported as "no chart" would read as "still
// building" for as long as GitHub stayed unhealthy.
func TestReadUmbrellaChart_ServerError_Errors(t *testing.T) {
	srv := contentsServer(t, http.StatusInternalServerError, `{"message":"boom"}`)
	defer srv.Close()
	s := newTestScaffolder(t, srv.URL)

	if _, err := s.ReadUmbrellaChart(context.Background(), "orders-v3-fcdc"); err == nil {
		t.Fatal("expected an error for a 500")
	}
}

func TestReadUmbrellaChart_RejectsInvalidRepoName(t *testing.T) {
	s := newTestScaffolder(t, "http://127.0.0.1:0")
	// No server needed: the name is validated before any request is built, which
	// is what keeps a malformed name out of the URL.
	if _, err := s.ReadUmbrellaChart(context.Background(), "Bad/../name"); err == nil {
		t.Fatal("expected an error for an invalid repository name")
	}
}

func TestAppValuesKey(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{
			// An alias wins, because that is what Helm keys values by.
			name: "aliased local dependency",
			yaml: "name: x\ndependencies:\n  - name: orders-v3\n    alias: app\n    repository: \"file://../helm/orders-v3\"\n",
			want: "app",
		},
		{
			// The template's own umbrella, which is where the hardcoded
			// `hex-scaffold` came from — correct here and only here.
			name: "template umbrella",
			yaml: "name: hex-scaffold-umbrella\ndependencies:\n  - name: hex-scaffold\n    repository: \"file://../helm/hex-scaffold\"\n",
			want: "hex-scaffold",
		},
		{
			// Registry-resolved dependencies are building blocks, never the app.
			name: "only remote dependencies",
			yaml: "name: x\ndependencies:\n  - name: plat-eng-sql-database-package\n    alias: sqldatabase\n    repository: \"oci://ghcr.io/o/helm-charts\"\n",
			want: "",
		},
		{
			name: "no dependencies at all",
			yaml: "name: x\n",
			want: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := contentsServer(t, http.StatusOK, githubContents(tc.yaml))
			defer srv.Close()
			s := newTestScaffolder(t, srv.URL)

			got, err := s.ReadUmbrellaChart(context.Background(), "some-app")
			if err != nil {
				t.Fatalf("ReadUmbrellaChart: %v", err)
			}
			if got.AppValuesKey != tc.want {
				t.Errorf("AppValuesKey = %q, want %q", got.AppValuesKey, tc.want)
			}
		})
	}
}

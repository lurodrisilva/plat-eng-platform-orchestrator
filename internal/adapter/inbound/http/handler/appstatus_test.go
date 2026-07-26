package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/myorg/platform-orchestrator/internal/application/port"
)

// fakeRepoReader stands in for reading a scaffolded repo's own Chart.yaml.
type fakeRepoReader struct {
	chart   port.UmbrellaChart
	err     error
	gotRepo string
}

func (f *fakeRepoReader) ReadUmbrellaChart(_ context.Context, repo string) (port.UmbrellaChart, error) {
	f.gotRepo = repo
	return f.chart, f.err
}

// fakeArtifacts stands in for the OCI registry lookup.
type fakeArtifacts struct {
	published port.PublishedApp
	err       error

	gotRepository  string
	gotChartName   string
	gotAppValueKey string
}

func (f *fakeArtifacts) ResolveLatest(_ context.Context, chartRepository, chartName, appValuesKey string) (port.PublishedApp, error) {
	f.gotRepository, f.gotChartName, f.gotAppValueKey = chartRepository, chartName, appValuesKey
	return f.published, f.err
}

// existingRepo is the scaffolded repository as GitHub reports it: note the
// collision-avoidance suffix, which the chart name does not carry.
func existingRepo() *fakeScaffolder {
	return &fakeScaffolder{statusResult: port.RepoStatus{
		Name:          "orders-v3-fcdc",
		Exists:        true,
		RepoURL:       "https://github.com/lurodrisilva/orders-v3-fcdc",
		DefaultBranch: "main",
	}}
}

func getStatus(t *testing.T, h *Apps, name string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/"+name, nil)
	req.Header.Set("Authorization", "Bearer good")
	req.SetPathValue("name", name)
	rr := httptest.NewRecorder()
	h.Status(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}

// The S5 definition of done, positive half: against a scaffold whose CI has run,
// the endpoint reports a resolvable chart version and a sha256: digest.
func TestApps_Status_Published_ReportsChartAndImage(t *testing.T) {
	reader := &fakeRepoReader{chart: port.UmbrellaChart{
		ChartName:    "orders-v3-umbrella",
		AppValuesKey: "orders-v3",
	}}
	artifacts := &fakeArtifacts{published: port.PublishedApp{
		Published:         true,
		ChartVersion:      "0.3.0-sha-abc1234",
		ImageRepository:   "ghcr.io/lurodrisilva/orders-v3-fcdc",
		ImageTag:          "latest",
		ImageDigest:       "sha256:9f2c1a4e8b7d6053f1e2a3b4c5d6e7f80912a3b4c5d6e7f8091a2b3c4d5e6f708",
		ImageSourceCommit: "35a48cd",
	}}
	h := NewApps(AppsDeps{
		Scaffolder: existingRepo(), RepoReader: reader, Artifacts: artifacts,
		ChartRepo: "ghcr.io/lurodrisilva/helm-charts",
		Validator: stubValidator{}, Logger: discardLogger(),
	})

	resp := getStatus(t, h, "orders-v3-fcdc")

	if resp["chartPublished"] != true {
		t.Errorf("chartPublished = %v, want true", resp["chartPublished"])
	}
	if resp["chartStatus"] != chartStatusPublished {
		t.Errorf("chartStatus = %v, want %s", resp["chartStatus"], chartStatusPublished)
	}

	chart, ok := resp["chart"].(map[string]any)
	if !ok {
		t.Fatalf("chart missing from response: %v", resp)
	}
	// The chart is named after the APP, the repository after the app PLUS a
	// suffix. Reporting the repo name here would send the portal looking for
	// orders-v3-fcdc-umbrella, which nothing publishes.
	if chart["name"] != "orders-v3-umbrella" {
		t.Errorf("chart.name = %v, want orders-v3-umbrella", chart["name"])
	}
	if chart["version"] != "0.3.0-sha-abc1234" {
		t.Errorf("chart.version = %v, want the prerelease CI published", chart["version"])
	}
	if chart["repository"] != "ghcr.io/lurodrisilva/helm-charts" {
		t.Errorf("chart.repository = %v", chart["repository"])
	}
	// The subchart key is the app's name, NOT the template's `hex-scaffold`.
	// A caller that scopes its values overlay to the wrong alias writes a key
	// Helm discards without erroring, which is defect D3.
	if chart["appValuesKey"] != "orders-v3" {
		t.Errorf("chart.appValuesKey = %v, want orders-v3", chart["appValuesKey"])
	}

	image, ok := resp["image"].(map[string]any)
	if !ok {
		t.Fatalf("image missing from response: %v", resp)
	}
	// The image must be the app's OWN repository. The template's chart defaults
	// this to the template's repository, so a scaffolded app that reports
	// ghcr.io/<owner>/net-hexagonal is reporting an image it cannot pull.
	if image["repository"] != "ghcr.io/lurodrisilva/orders-v3-fcdc" {
		t.Errorf("image.repository = %v, want the app's own repository", image["repository"])
	}
	digest, _ := image["digest"].(string)
	if len(digest) < 8 || digest[:7] != "sha256:" {
		t.Errorf("image.digest = %q, want a sha256: reference", digest)
	}
	// Provenance the deployment domain will accept. Without it the caller has to
	// invent a commit, which is how every portal deploy came to carry the
	// TEMPLATE repository's HEAD as its source.
	if image["sourceCommit"] != "35a48cd" {
		t.Errorf("image.sourceCommit = %v, want the app's own build commit", image["sourceCommit"])
	}

	// The registry lookup must be keyed on what the repo said, not on the app name.
	if artifacts.gotChartName != "orders-v3-umbrella" {
		t.Errorf("resolver asked for chart %q, want orders-v3-umbrella", artifacts.gotChartName)
	}
	if artifacts.gotAppValueKey != "orders-v3" {
		t.Errorf("resolver got appValuesKey %q, want orders-v3", artifacts.gotAppValueKey)
	}
	if reader.gotRepo != "orders-v3-fcdc" {
		t.Errorf("repo reader asked about %q, want the suffixed repo name", reader.gotRepo)
	}
}

// The S5 definition of done, negative half: before CI finishes the endpoint says
// so without erroring. The portal polls this, so a 5xx here would surface as a
// broken platform during the ordinary wait for a first build.
func TestApps_Status_ChartNotPublishedYet_ReportsAwaitingWithoutErroring(t *testing.T) {
	reader := &fakeRepoReader{chart: port.UmbrellaChart{
		ChartName:    "orders-v3-umbrella",
		AppValuesKey: "orders-v3",
	}}
	h := NewApps(AppsDeps{
		Scaffolder: existingRepo(), RepoReader: reader,
		Artifacts: &fakeArtifacts{published: port.PublishedApp{Published: false}},
		ChartRepo: "ghcr.io/lurodrisilva/helm-charts",
		Validator: stubValidator{}, Logger: discardLogger(),
	})

	resp := getStatus(t, h, "orders-v3-fcdc")

	if resp["chartPublished"] != false {
		t.Errorf("chartPublished = %v, want false", resp["chartPublished"])
	}
	if resp["chartStatus"] != chartStatusAwaitingCI {
		t.Errorf("chartStatus = %v, want %s", resp["chartStatus"], chartStatusAwaitingCI)
	}
	// The chart's identity is known even though no version exists yet — the repo
	// already told us. Reporting it lets the portal show what WILL deploy.
	chart, ok := resp["chart"].(map[string]any)
	if !ok {
		t.Fatalf("chart missing: %v", resp)
	}
	if chart["name"] != "orders-v3-umbrella" || chart["version"] != "" {
		t.Errorf("chart = %v, want a named chart with an empty version", chart)
	}
	if _, present := resp["image"]; present {
		t.Errorf("image reported for an unpublished chart: %v", resp["image"])
	}
}

// A registry failure is NOT "your app has not built yet". Conflating them leaves
// the portal waiting on a build that already finished.
func TestApps_Status_RegistryError_ReportsUnavailableNotAwaiting(t *testing.T) {
	h := NewApps(AppsDeps{
		Scaffolder: existingRepo(),
		RepoReader: &fakeRepoReader{chart: port.UmbrellaChart{ChartName: "orders-v3-umbrella", AppValuesKey: "orders-v3"}},
		Artifacts:  &fakeArtifacts{err: errors.New("ghcr.io: 503 service unavailable")},
		ChartRepo:  "ghcr.io/lurodrisilva/helm-charts",
		Validator:  stubValidator{}, Logger: discardLogger(),
	})

	resp := getStatus(t, h, "orders-v3-fcdc")

	if resp["chartPublished"] != false {
		t.Errorf("chartPublished = %v, want false", resp["chartPublished"])
	}
	if resp["chartStatus"] != chartStatusUnavailable {
		t.Errorf("chartStatus = %v, want %s", resp["chartStatus"], chartStatusUnavailable)
	}
}

// Reading the repo can fail for its own reasons; same rule applies.
func TestApps_Status_RepoReadError_ReportsUnavailable(t *testing.T) {
	h := NewApps(AppsDeps{
		Scaffolder: existingRepo(),
		RepoReader: &fakeRepoReader{err: errors.New("github returned 500")},
		Artifacts:  &fakeArtifacts{},
		ChartRepo:  "ghcr.io/lurodrisilva/helm-charts",
		Validator:  stubValidator{}, Logger: discardLogger(),
	})

	if got := getStatus(t, h, "orders-v3-fcdc")["chartStatus"]; got != chartStatusUnavailable {
		t.Errorf("chartStatus = %v, want %s", got, chartStatusUnavailable)
	}
}

// The repository is created before the scaffold workflow pushes into it, so
// "exists but empty" is a real window rather than a corrupt state.
func TestApps_Status_RepoExistsButNoChartYet_ReportsAwaiting(t *testing.T) {
	artifacts := &fakeArtifacts{}
	h := NewApps(AppsDeps{
		Scaffolder: existingRepo(),
		RepoReader: &fakeRepoReader{}, // zero chart, nil error: no Chart.yaml
		Artifacts:  artifacts,
		ChartRepo:  "ghcr.io/lurodrisilva/helm-charts",
		Validator:  stubValidator{}, Logger: discardLogger(),
	})

	resp := getStatus(t, h, "orders-v3-fcdc")

	if resp["chartStatus"] != chartStatusAwaitingCI {
		t.Errorf("chartStatus = %v, want %s", resp["chartStatus"], chartStatusAwaitingCI)
	}
	if _, present := resp["chart"]; present {
		t.Errorf("chart reported for a repo with no Chart.yaml: %v", resp["chart"])
	}
	// Nothing to look up, so nothing was looked up.
	if artifacts.gotChartName != "" {
		t.Errorf("registry queried for chart %q despite no chart in the repo", artifacts.gotChartName)
	}
}

// Unwired collaborators degrade to "cannot tell", never to "not published".
// Reporting the latter would make an unconfigured orchestrator indistinguishable
// from an app whose build is still running.
func TestApps_Status_UnwiredResolvers_ReportUnavailable(t *testing.T) {
	h := NewApps(AppsDeps{
		Scaffolder: existingRepo(), Validator: stubValidator{}, Logger: discardLogger(),
	})

	resp := getStatus(t, h, "orders-v3-fcdc")

	if resp["ready"] != true {
		t.Errorf("ready = %v, want true — repo existence still answers", resp["ready"])
	}
	if resp["chartStatus"] != chartStatusUnavailable {
		t.Errorf("chartStatus = %v, want %s", resp["chartStatus"], chartStatusUnavailable)
	}
}

// A repo that does not exist yet has nothing to resolve, and asking the registry
// would spend a round trip to answer a question already settled.
func TestApps_Status_RepoMissing_SkipsArtifactLookup(t *testing.T) {
	artifacts := &fakeArtifacts{}
	reader := &fakeRepoReader{}
	h := NewApps(AppsDeps{
		Scaffolder: &fakeScaffolder{statusResult: port.RepoStatus{Name: "orders-v3-fcdc", Exists: false}},
		RepoReader: reader, Artifacts: artifacts,
		ChartRepo: "ghcr.io/lurodrisilva/helm-charts",
		Validator: stubValidator{}, Logger: discardLogger(),
	})

	resp := getStatus(t, h, "orders-v3-fcdc")

	if resp["ready"] != false {
		t.Errorf("ready = %v, want false", resp["ready"])
	}
	if resp["chartStatus"] != chartStatusAwaitingCI {
		t.Errorf("chartStatus = %v, want %s", resp["chartStatus"], chartStatusAwaitingCI)
	}
	if reader.gotRepo != "" || artifacts.gotChartName != "" {
		t.Errorf("resolved artifacts for a repository that does not exist")
	}
}

// The pre-S5 response keys must survive: the portal already polls `ready` and
// `defaultBranch`, and S6 lands after this.
func TestApps_Status_KeepsExistingResponseKeys(t *testing.T) {
	h := NewApps(AppsDeps{
		Scaffolder: existingRepo(), Validator: stubValidator{}, Logger: discardLogger(),
	})
	resp := getStatus(t, h, "orders-v3-fcdc")
	for _, k := range []string{"name", "ready", "repoUrl", "defaultBranch"} {
		if _, present := resp[k]; !present {
			t.Errorf("response lost the pre-existing key %q", k)
		}
	}
}

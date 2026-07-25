package oci

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"testing"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	"sigs.k8s.io/yaml"
)

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// makeArchive packages a minimal but valid chart with the given defaults and
// returns its .tgz bytes — the shape Compose receives from the resolver.
func makeArchive(t *testing.T, name string, defaults map[string]any) []byte {
	t.Helper()
	// A real resolved umbrella carries its defaults in the raw values.yaml file;
	// helm's Save serializes that file, not chart.Values, so mirror it here.
	valuesYAML, err := yaml.Marshal(defaults)
	if err != nil {
		t.Fatalf("makeArchive: marshal defaults: %v", err)
	}
	c := &chart.Chart{
		Metadata: &chart.Metadata{APIVersion: "v2", Name: name, Version: "0.1.0", AppVersion: "0.1.0"},
		Values:   defaults,
		Raw:      []*chart.File{{Name: chartutil.ValuesfileName, Data: valuesYAML}},
		Templates: []*chart.File{
			{Name: "templates/cm.yaml", Data: []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: {{ .Chart.Name }}\n")},
		},
	}
	dir := t.TempDir()
	path, err := chartutil.Save(c, dir)
	if err != nil {
		t.Fatalf("makeArchive: save: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("makeArchive: read: %v", err)
	}
	return b
}

func loadComposed(t *testing.T, pkg []byte) *chart.Chart {
	t.Helper()
	c, err := loader.LoadArchive(bytes.NewReader(pkg))
	if err != nil {
		t.Fatalf("load composed: %v", err)
	}
	return c
}

func TestCompose_StampsMetadataAndBakesValues(t *testing.T) {
	archive := makeArchive(t, "umbrella", map[string]any{"replicaCount": "1", "extra": "keep"})
	comp := NewComposer(testLogger())

	out, err := comp.Compose(
		context.Background(),
		archive,
		map[string]any{"replicaCount": "3"}, // user overlay
		map[string]any{"securityContext": "locked"}, // platform
		"0.2.0-sha-abc1234",                         // version
		"abc1234",                                   // appVersion
		map[string]string{"platform.myorg.io/corr": "xyz"}, // annotations
	)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if out.ChartName != "umbrella" {
		t.Errorf("ChartName = %q, want umbrella", out.ChartName)
	}
	if out.DeploymentVersion != "0.2.0-sha-abc1234" {
		t.Errorf("DeploymentVersion = %q", out.DeploymentVersion)
	}
	if len(out.PackageBytes) == 0 {
		t.Fatal("PackageBytes empty")
	}

	c := loadComposed(t, out.PackageBytes)
	if c.Metadata.Version != "0.2.0-sha-abc1234" {
		t.Errorf("baked Version = %q", c.Metadata.Version)
	}
	if c.Metadata.AppVersion != "abc1234" {
		t.Errorf("baked AppVersion = %q", c.Metadata.AppVersion)
	}
	if c.Metadata.Annotations["platform.myorg.io/corr"] != "xyz" {
		t.Errorf("annotation not baked: %v", c.Metadata.Annotations)
	}
	if c.Values["replicaCount"] != "3" {
		t.Errorf("user overlay not baked: replicaCount = %v", c.Values["replicaCount"])
	}
	if c.Values["securityContext"] != "locked" {
		t.Errorf("platform value not baked: %v", c.Values["securityContext"])
	}
	if c.Values["extra"] != "keep" {
		t.Errorf("chart default dropped: extra = %v", c.Values["extra"])
	}
}

func TestCompose_PrecedencePlatformWinsUserWinsDefault(t *testing.T) {
	archive := makeArchive(t, "umbrella", map[string]any{
		"a":      "default",
		"nested": map[string]any{"x": "default", "y": "default"},
	})
	comp := NewComposer(testLogger())

	out, err := comp.Compose(context.Background(), archive,
		map[string]any{"a": "user", "nested": map[string]any{"y": "user", "z": "user"}},
		map[string]any{"a": "platform", "nested": map[string]any{"z": "platform"}},
		"1.0.0", "", nil,
	)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	c := loadComposed(t, out.PackageBytes)

	if c.Values["a"] != "platform" {
		t.Errorf("top-level: a = %v, want platform (platform wins)", c.Values["a"])
	}
	nested, ok := c.Values["nested"].(map[string]any)
	if !ok {
		t.Fatalf("nested is %T", c.Values["nested"])
	}
	if nested["x"] != "default" {
		t.Errorf("nested.x = %v, want default (untouched)", nested["x"])
	}
	if nested["y"] != "user" {
		t.Errorf("nested.y = %v, want user (user over default)", nested["y"])
	}
	if nested["z"] != "platform" {
		t.Errorf("nested.z = %v, want platform (platform over user)", nested["z"])
	}
}

func TestCompose_DoesNotMutateInputs(t *testing.T) {
	archive := makeArchive(t, "umbrella", map[string]any{"a": "default"})
	comp := NewComposer(testLogger())

	user := map[string]any{"nested": map[string]any{"k": "user"}}
	platform := map[string]any{"nested": map[string]any{"k": "platform"}}

	if _, err := comp.Compose(context.Background(), archive, user, platform, "1.0.0", "", nil); err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if user["nested"].(map[string]any)["k"] != "user" {
		t.Error("user input mutated")
	}
	if platform["nested"].(map[string]any)["k"] != "platform" {
		t.Error("platform input mutated")
	}
}

// A chart with no values.yaml in Raw must still get its merged values baked —
// the composer appends the file rather than only replacing it.
func TestCompose_AppendsValuesWhenChartHasNone(t *testing.T) {
	c := &chart.Chart{
		Metadata: &chart.Metadata{APIVersion: "v2", Name: "novalues", Version: "0.1.0"},
		Templates: []*chart.File{
			{Name: "templates/cm.yaml", Data: []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x\n")},
		},
	}
	dir := t.TempDir()
	path, err := chartutil.Save(c, dir)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	archive, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	out, err := NewComposer(testLogger()).Compose(context.Background(), archive,
		map[string]any{"user": "u"}, map[string]any{"platform": "p"}, "1.0.0", "", nil)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	loaded := loadComposed(t, out.PackageBytes)
	if loaded.Values["user"] != "u" || loaded.Values["platform"] != "p" {
		t.Errorf("append path lost values: %v", loaded.Values)
	}
}

func TestCompose_Guards(t *testing.T) {
	comp := NewComposer(testLogger())
	if _, err := comp.Compose(context.Background(), nil, nil, nil, "1.0.0", "", nil); err == nil {
		t.Error("empty archive should error")
	}
	archive := makeArchive(t, "umbrella", nil)
	if _, err := comp.Compose(context.Background(), archive, nil, nil, "", "", nil); err == nil {
		t.Error("empty version should error")
	}
}

// --- declared application dependencies (S4, ADR-0023) ------------------------

// umbrellaDefaults mirrors the shape the real hex-scaffold-umbrella ships: a
// template-named database the deployment must replace, not merge with.
func umbrellaDefaults() map[string]any {
	return map[string]any{
		"sqldatabase": map[string]any{
			"enabled": true,
			"engine":  "azure-flexibleserver",
			"databases": map[string]any{
				"sql": []any{
					map[string]any{
						"name": "acct",
						"azureFlexibleServer": map[string]any{
							"size": "small", "version": "16", "storageMb": 32768, "databaseName": "acct-db",
						},
					},
				},
			},
		},
		"hex-scaffold": map[string]any{
			"postgres": map[string]any{
				"bindBuildingBlock": map[string]any{"enabled": true, "instanceName": "acct"},
			},
		},
	}
}

// platformValuesFor is the shape the orchestrator's translator emits. It is
// asserted key-for-key against the real translator in
// internal/application/deployment/resources_test.go
// (TestPlatformValues_OnePostgresEmitsTheExactChartShape); this package cannot
// import the application layer, so the two are pinned by that test rather than
// shared as one literal.
func platformValuesFor(app, team, environment string) map[string]any {
	return map[string]any{
		"sqldatabase": map[string]any{
			"enabled": true, "engine": "azure-flexibleserver",
			"team": team, "environment": environment,
			"databases": map[string]any{
				"sql": []any{
					map[string]any{
						"name": app,
						"azureFlexibleServer": map[string]any{
							"size": "medium", "version": "16", "storageMb": 65536, "databaseName": app + "-db",
						},
					},
				},
			},
		},
		"hex-scaffold": map[string]any{
			"postgres": map[string]any{
				"bindBuildingBlock": map[string]any{"enabled": true, "instanceName": app},
			},
		},
	}
}

// The round trip the S4 DoD asks for: compose the umbrella with the translator's
// values and prove the baked values.yaml describes exactly ONE database, named
// after the application, with the app bound to that same name.
//
// The template's own `acct` entry must be GONE, not merged alongside. That is
// load-bearing and it works because databases.sql is a LIST — deepMerge recurses
// into maps only, so a list replaces wholesale. Half-merging would leave the
// template's database in the render, which is the billed orphan this slice
// exists to eliminate.
func TestCompose_DeclaredDatabaseReplacesTheTemplateDefault(t *testing.T) {
	archive := makeArchive(t, "orders-v3-umbrella", umbrellaDefaults())

	out, err := NewComposer(testLogger()).Compose(context.Background(), archive,
		nil, platformValuesFor("orders-v3", "payments", "development"), "1.0.0", "abc1234", nil)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	c := loadComposed(t, out.PackageBytes)

	sql, ok := c.Values["sqldatabase"].(map[string]any)["databases"].(map[string]any)["sql"].([]any)
	if !ok {
		t.Fatalf("databases.sql is %T", c.Values["sqldatabase"].(map[string]any)["databases"])
	}
	if len(sql) != 1 {
		t.Fatalf("composed %d databases, want exactly 1 — each one is a separate billed server: %#v", len(sql), sql)
	}

	entry := sql[0].(map[string]any)
	if entry["name"] != "orders-v3" {
		t.Errorf("XR name = %v, want orders-v3 (the application id), not the template's", entry["name"])
	}
	azure := entry["azureFlexibleServer"].(map[string]any)
	if azure["databaseName"] != "orders-v3-db" || azure["size"] != "medium" || azure["storageMb"] != float64(65536) {
		t.Errorf("azureFlexibleServer = %#v, want the declared shape", azure)
	}

	bind := c.Values["hex-scaffold"].(map[string]any)["postgres"].(map[string]any)["bindBuildingBlock"].(map[string]any)
	if bind["instanceName"] != entry["name"] {
		t.Errorf("bind instanceName = %v but the XR is named %v — the app would wait forever on Secrets nothing creates",
			bind["instanceName"], entry["name"])
	}
}

// The engine is platform-locked. A caller cannot reach it through `values`,
// because the composer merges defaults < user < platform. The create boundary
// refuses such an overlay outright (reservedValuePaths), so this is the second
// of two independent guards, not the only one.
func TestCompose_UserOverlayCannotReachTheDeclaredDatabase(t *testing.T) {
	archive := makeArchive(t, "orders-v3-umbrella", umbrellaDefaults())

	hostile := map[string]any{
		"sqldatabase": map[string]any{
			"engine": "cloudnativepg",
			"databases": map[string]any{
				"sql": []any{map[string]any{"name": "someone-elses-db"}},
			},
		},
		"hex-scaffold": map[string]any{
			"postgres": map[string]any{"bindBuildingBlock": map[string]any{"instanceName": "someone-elses-db"}},
		},
	}

	out, err := NewComposer(testLogger()).Compose(context.Background(), archive,
		hostile, platformValuesFor("orders-v3", "payments", "development"), "1.0.0", "abc1234", nil)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	c := loadComposed(t, out.PackageBytes)

	sqldb := c.Values["sqldatabase"].(map[string]any)
	if sqldb["engine"] != "azure-flexibleserver" {
		t.Errorf("engine = %v, want azure-flexibleserver (platform-locked)", sqldb["engine"])
	}
	sql := sqldb["databases"].(map[string]any)["sql"].([]any)
	if len(sql) != 1 || sql[0].(map[string]any)["name"] != "orders-v3" {
		t.Errorf("databases.sql = %#v, want the platform's single orders-v3 entry", sql)
	}
	bind := c.Values["hex-scaffold"].(map[string]any)["postgres"].(map[string]any)["bindBuildingBlock"].(map[string]any)
	if bind["instanceName"] != "orders-v3" {
		t.Errorf("bind instanceName = %v, want orders-v3", bind["instanceName"])
	}
}

// A deployment that declares no resources composes exactly as it did before
// declared dependencies existed: the chart's own defaults, untouched.
func TestCompose_NoPlatformValuesLeavesTheChartDefaultsAlone(t *testing.T) {
	archive := makeArchive(t, "orders-v3-umbrella", umbrellaDefaults())

	out, err := NewComposer(testLogger()).Compose(context.Background(), archive, nil, nil, "1.0.0", "abc1234", nil)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	c := loadComposed(t, out.PackageBytes)

	sql := c.Values["sqldatabase"].(map[string]any)["databases"].(map[string]any)["sql"].([]any)
	if len(sql) != 1 || sql[0].(map[string]any)["name"] != "acct" {
		t.Errorf("databases.sql = %#v, want the chart's own default untouched", sql)
	}
}

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

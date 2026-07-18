package oci

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	"sigs.k8s.io/yaml"

	"github.com/myorg/platform-orchestrator/internal/application/port"
)

// Composer implements port.ChartComposer.
//
// It takes the resolved umbrella archive (ADR-0008), bakes the per-deployment
// values INTO the chart's values.yaml, stamps the deployment version, and
// repackages to a self-contained .tgz. The values are baked (not passed as an
// ArgoCD Application override) so the published artifact fully determines what
// deploys: chart version X always renders the same thing, which is the
// reproducibility ADR-0008/ADR-0016 rely on.
//
// Value precedence is deliberate and fixed: chart defaults < user overlay <
// platform values. The platform layer always wins — it carries the locks the
// tunable allowlist enforces at the API boundary (ADR-0006), so a user overlay
// can never override a platform-locked knob even if one slips past validation.
type Composer struct {
	logger *slog.Logger
}

// NewComposer builds a chart composer.
func NewComposer(logger *slog.Logger) *Composer {
	return &Composer{logger: logger}
}

// Compose merges values into the archive, stamps metadata, and repackages.
//
//	archive         the resolved umbrella .tgz bytes (deps already vendored)
//	values          the user's tunable overlay (allowlist-validated upstream)
//	platformValues  platform-enforced values; win over user and defaults
//	version         the deployment version stamped as Chart.yaml version
//	appVersion      stamped as Chart.yaml appVersion (usually the image sha)
//	annotations     added to Chart.yaml annotations (provenance/correlation)
func (c *Composer) Compose(ctx context.Context, archive []byte, values, platformValues map[string]any, version, appVersion string, annotations map[string]string) (port.ComposedChart, error) {
	if len(archive) == 0 {
		return port.ComposedChart{}, fmt.Errorf("compose: empty archive")
	}
	if version == "" {
		return port.ComposedChart{}, fmt.Errorf("compose: empty version")
	}

	chrt, err := loader.LoadArchive(bytes.NewReader(archive))
	if err != nil {
		return port.ComposedChart{}, fmt.Errorf("compose: load archive: %w", err)
	}
	if chrt.Metadata == nil {
		return port.ComposedChart{}, fmt.Errorf("compose: archive has no chart metadata")
	}

	// defaults < user < platform (platform wins). deepMerge deep-copies, so the
	// chart's own defaults and the caller's maps are never mutated.
	merged := deepMerge(deepMerge(chrt.Values, values), platformValues)

	// The packaged values.yaml is what actually ships: helm's chartutil.Save
	// serializes values.yaml from chrt.Raw, NOT chrt.Values (save.go), so the
	// merged values are written into the raw values.yaml file below. chrt.Values
	// is set too, only to keep the in-memory chart self-consistent for any
	// in-process reader; Save ignores it. Marshal with sigs.k8s.io/yaml to match
	// how helm's loader parses values.yaml back into map[string]any.
	chrt.Values = merged
	mergedYAML, err := yaml.Marshal(merged)
	if err != nil {
		return port.ComposedChart{}, fmt.Errorf("compose: marshal merged values: %w", err)
	}
	replaced := false
	for _, f := range chrt.Raw {
		if f.Name == chartutil.ValuesfileName {
			f.Data = mergedYAML
			replaced = true
			break
		}
	}
	if !replaced {
		chrt.Raw = append(chrt.Raw, &chart.File{Name: chartutil.ValuesfileName, Data: mergedYAML})
	}

	chrt.Metadata.Version = version
	if appVersion != "" {
		chrt.Metadata.AppVersion = appVersion
	}
	if len(annotations) > 0 {
		if chrt.Metadata.Annotations == nil {
			chrt.Metadata.Annotations = map[string]string{}
		}
		for k, v := range annotations {
			chrt.Metadata.Annotations[k] = v
		}
	}

	// chartutil.Save serializes Metadata + Values + Templates + vendored deps
	// (it ignores chrt.Raw), so the edits above land in the repackaged chart.
	// It writes to a directory, so stage to a temp dir and read the bytes back.
	tmpDir, err := os.MkdirTemp("", "composed-chart-*")
	if err != nil {
		return port.ComposedChart{}, fmt.Errorf("compose: temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	path, err := chartutil.Save(chrt, tmpDir)
	if err != nil {
		return port.ComposedChart{}, fmt.Errorf("compose: package chart: %w", err)
	}
	pkg, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return port.ComposedChart{}, fmt.Errorf("compose: read packaged chart: %w", err)
	}

	c.logger.InfoContext(ctx, "composed chart",
		slog.String("chart", chrt.Metadata.Name),
		slog.String("version", version),
		slog.Int("bytes", len(pkg)),
	)

	return port.ComposedChart{
		ChartName:         chrt.Metadata.Name,
		DeploymentVersion: version,
		PackageBytes:      pkg,
	}, nil
}

// deepMerge returns a deep copy of low with high merged over it: high wins on
// scalar conflicts, and nested maps are merged recursively rather than
// replaced. Inputs are never mutated.
func deepMerge(low, high map[string]any) map[string]any {
	out := deepCopyMap(low)
	for k, hv := range high {
		if lv, ok := out[k]; ok {
			if lm, lok := lv.(map[string]any); lok {
				if hm, hok := hv.(map[string]any); hok {
					out[k] = deepMerge(lm, hm)
					continue
				}
			}
		}
		out[k] = deepCopyValue(hv)
	}
	return out
}

func deepCopyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = deepCopyValue(v)
	}
	return out
}

func deepCopyValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return deepCopyMap(t)
	case []any:
		s := make([]any, len(t))
		for i, e := range t {
			s[i] = deepCopyValue(e)
		}
		return s
	default:
		return t
	}
}

// compile-time proof the adapter satisfies the port.
var _ port.ChartComposer = (*Composer)(nil)

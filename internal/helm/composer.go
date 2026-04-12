package helm

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

	"github.com/myorg/platform-orchestrator/internal/domain"
)

// Composer loads, enriches, and packages Helm charts.
type Composer struct {
	logger *slog.Logger
}

func NewComposer(logger *slog.Logger) *Composer {
	return &Composer{logger: logger}
}

// Compose takes a resolved chart archive, merges values, mutates metadata, and packages the result.
func (c *Composer) Compose(ctx context.Context, resolved domain.ResolvedChart, req domain.DeploymentRequest, meta domain.DeploymentMetadata) (domain.ComposedChart, error) {
	ch, err := c.loadFromArchive(resolved.ArchiveBytes)
	if err != nil {
		return domain.ComposedChart{}, domain.NewChartValidationError("failed to load chart", err)
	}

	if err := c.validateDependencies(ch); err != nil {
		return domain.ComposedChart{}, err
	}

	finalValues := c.mergeValues(ch, req, meta)

	c.mutateMetadata(ch, meta, req, resolved)

	pkgBytes, err := c.packageChart(ch, finalValues)
	if err != nil {
		return domain.ComposedChart{}, fmt.Errorf("packaging chart: %w", err)
	}

	c.logger.InfoContext(ctx, "composed chart",
		slog.String("chart", ch.Name()),
		slog.String("version", ch.Metadata.Version),
		slog.String("appVersion", ch.Metadata.AppVersion),
		slog.Int("packageBytes", len(pkgBytes)),
	)

	return domain.ComposedChart{
		ChartName:         ch.Name(),
		DeploymentVersion: meta.DeploymentVersion,
		PackageBytes:      pkgBytes,
	}, nil
}

func (c *Composer) loadFromArchive(archive []byte) (*chart.Chart, error) {
	ch, err := loader.LoadArchive(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("loading chart archive: %w", err)
	}
	if ch.Metadata == nil {
		return nil, fmt.Errorf("chart has no metadata")
	}
	if ch.Metadata.Name == "" {
		return nil, fmt.Errorf("chart has no name")
	}
	return ch, nil
}

func (c *Composer) validateDependencies(ch *chart.Chart) error {
	deps := ch.Metadata.Dependencies
	if len(deps) == 0 {
		return nil // no dependencies to validate
	}

	// Check that Chart.lock exists (dependencies must be pre-locked)
	hasLock := false
	for _, f := range ch.Raw {
		if f.Name == "Chart.lock" {
			hasLock = true
			break
		}
	}
	if !hasLock {
		return domain.NewChartDependencyError("Chart.lock is missing — dependencies must be pre-locked")
	}

	// Verify all dependencies are present in charts/ subdirectory
	loadedDeps := ch.Dependencies()
	depMap := make(map[string]bool, len(loadedDeps))
	for _, d := range loadedDeps {
		depMap[d.Name()] = true
	}

	for _, required := range deps {
		if !depMap[required.Name] {
			return domain.NewChartDependencyError(
				fmt.Sprintf("dependency %q (version %s) is declared but not present in charts/", required.Name, required.Version),
			)
		}
	}

	return nil
}

func (c *Composer) mergeValues(ch *chart.Chart, req domain.DeploymentRequest, meta domain.DeploymentMetadata) map[string]any {
	// Platform-injected values — highest priority, cannot be overridden
	platformValues := map[string]any{
		"image": map[string]any{
			"repository": req.Image.Repository,
			"tag":        req.Image.Tag,
			"digest":     req.Image.Digest,
		},
		"deploymentMetadata": map[string]any{
			"deploymentId":  meta.DeploymentID,
			"componentId":   meta.ComponentID,
			"gitSha":        req.Source.GitSHA,
			"environment":   req.Target.Environment,
			"deployedAt":    meta.Timestamp.UTC().Format("2006-01-02T15:04:05Z"),
			"deployedBy":    req.Source.Actor,
			"chartVersion":  meta.DeploymentVersion,
			"workflowRunId": req.Source.GitHubRunID,
		},
	}

	// Merge order: chart defaults ← request values ← platform values
	merged := chartutil.CoalesceTables(copyValues(ch.Values), copyValues(req.Values))
	merged = chartutil.CoalesceTables(merged, platformValues)

	return merged
}

func (c *Composer) mutateMetadata(ch *chart.Chart, meta domain.DeploymentMetadata, req domain.DeploymentRequest, resolved domain.ResolvedChart) {
	ch.Metadata.Version = meta.DeploymentVersion
	ch.Metadata.AppVersion = req.Source.GitSHA

	if ch.Metadata.Annotations == nil {
		ch.Metadata.Annotations = make(map[string]string)
	}
	ch.Metadata.Annotations["platform.example.com/deployment-id"] = meta.DeploymentID
	ch.Metadata.Annotations["platform.example.com/source-chart-version"] = resolved.ResolvedVersion
	ch.Metadata.Annotations["platform.example.com/environment"] = req.Target.Environment
	ch.Metadata.Annotations["platform.example.com/git-sha"] = req.Source.GitSHA
	ch.Metadata.Annotations["platform.example.com/image-digest"] = req.Image.Digest
}

func (c *Composer) packageChart(ch *chart.Chart, values map[string]any) ([]byte, error) {
	// Replace chart values with final merged values
	ch.Values = values

	// Write to temp dir and package
	tmpDir, err := os.MkdirTemp("", "helm-package-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	pkgPath, err := chartutil.Save(ch, tmpDir)
	if err != nil {
		return nil, fmt.Errorf("saving chart: %w", err)
	}

	data, err := os.ReadFile(filepath.Clean(pkgPath))
	if err != nil {
		return nil, fmt.Errorf("reading packaged chart: %w", err)
	}

	return data, nil
}

// copyValues deep-copies a values map to avoid mutation of originals.
func copyValues(src map[string]any) map[string]any {
	if src == nil {
		return make(map[string]any)
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		if sub, ok := v.(map[string]any); ok {
			dst[k] = copyValues(sub)
		} else {
			dst[k] = v
		}
	}
	return dst
}

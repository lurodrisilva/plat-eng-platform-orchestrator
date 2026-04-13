package port

import "context"

// ResolvedChart holds the result of chart resolution.
type ResolvedChart struct {
	SourceRepository string
	ChartName        string
	ResolvedVersion  string
	ResolvedTag      string
	ArchiveBytes     []byte
}

// ComposedChart holds the packaged chart ready for publishing.
type ComposedChart struct {
	ChartName         string
	DeploymentVersion string
	PackageBytes      []byte
}

// ChartResolver resolves Helm chart versions from a repository.
type ChartResolver interface {
	Resolve(ctx context.Context, repository, name, constraint string, allowPrerelease bool) (ResolvedChart, error)
}

// ChartComposer loads, enriches, and packages Helm charts.
type ChartComposer interface {
	Compose(ctx context.Context, archive []byte, values map[string]any, platformValues map[string]any, version, appVersion string, annotations map[string]string) (ComposedChart, error)
}

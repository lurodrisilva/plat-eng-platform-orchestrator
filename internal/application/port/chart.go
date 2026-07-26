package port

import (
	"context"
	"strings"
)

// ResolvedChart holds the result of chart resolution.
type ResolvedChart struct {
	SourceRepository string
	ChartName        string
	ResolvedVersion  string
	ResolvedTag      string
	ArchiveBytes     []byte

	// AppValuesKey is the umbrella values key the application subchart hangs
	// under, read out of the resolved chart's own metadata.
	//
	// It exists because platform values must address the application, and its key
	// is NOT a constant: the scaffolder substitutes the template's `hex-scaffold`
	// token for the app's own name when it renders, so a scaffolded umbrella keys
	// its app subchart `<app>`. Writing platform values under the wrong key is
	// not an error Helm reports — it silently discards them, which is how the
	// application's database bind came to be unmanaged while the XR it points at
	// was being renamed.
	//
	// Empty when the resolver cannot determine it. Callers that need it must
	// refuse rather than guess (see platformValues).
	AppValuesKey string
}

// ChartDependency is one entry of a chart's dependency list, reduced to the
// three fields that identify the application subchart.
type ChartDependency struct {
	Name       string
	Alias      string
	Repository string
}

// AppValuesKeyOf picks the values key the application subchart hangs under.
//
// The application is the umbrella's LOCAL dependency: it ships inside the app
// repository and is referenced as `file://../helm/<app>`, while every building
// block resolves from an OCI registry. That distinction is the identification —
// it holds whatever the app happens to be called, whereas matching on a name
// would require the app's identity, which is the thing being looked up.
//
// Helm keys a subchart's values by its alias when it has one and by its chart
// name otherwise, so the same rule is applied here.
//
// Lives in the port package so both chart resolvers apply one rule: they parse
// different formats (an OCI archive's metadata, a repository's Chart.yaml) and a
// second copy of this would drift.
func AppValuesKeyOf(deps []ChartDependency) string {
	for _, d := range deps {
		if !strings.HasPrefix(d.Repository, "file://") {
			continue
		}
		if d.Alias != "" {
			return d.Alias
		}
		return d.Name
	}
	return ""
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

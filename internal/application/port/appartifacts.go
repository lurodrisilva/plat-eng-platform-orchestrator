package port

import "context"

// UmbrellaChart is a scaffolded repository's own statement of what it deploys,
// read from its `deploy/umbrella/Chart.yaml` rather than derived.
//
// Deriving it was the obvious alternative and it is wrong. The repository name
// carries a collision-avoidance suffix the chart name does not (`orders-v3-fcdc`
// publishes `orders-v3-umbrella`), and the scaffolder TRUNCATES a long app name
// to fit the suffix inside GitHub's 39-character limit — so stripping the suffix
// back off does not always return the name that was rendered. A derivation that
// is right for short names and silently wrong for long ones is worse than no
// derivation: it reports `chartPublished: false` forever and reads as "CI has
// not finished yet".
type UmbrellaChart struct {
	// ChartName is the umbrella's `name`, e.g. orders-v3-umbrella.
	ChartName string

	// AppValuesKey is the umbrella values key the application subchart hangs
	// under — its dependency alias, or its chart name when it has no alias.
	//
	// It is NOT the constant `hex-scaffold`. The scaffolder renders the template
	// by substituting identity tokens, and `hex-scaffold` is one of them, so a
	// scaffolded app's subchart is called `<app>` and its values live under
	// `<app>:`. Anything that addresses the app's values by a hardcoded name
	// addresses a key Helm will discard without error.
	AppValuesKey string
}

// AppRepoReader reads deploy-time identity out of a scaffolded application's
// repository. Separate from Scaffolder's dispatch concern because this reads the
// repository's content rather than its existence, but the same GitHub App
// installation backs both.
type AppRepoReader interface {
	// ReadUmbrellaChart parses deploy/umbrella/Chart.yaml on the repository's
	// default branch. A repository without that file is not an error — a
	// chart-less repository simply has nothing to deploy yet.
	ReadUmbrellaChart(ctx context.Context, repo string) (UmbrellaChart, error)
}

// PublishedApp is what an application's CI has actually published: the chart
// version the platform would deploy, and the image that chart will run.
//
// The image is read out of the PUBLISHED CHART, not resolved separately from the
// container registry. Release CI pins the digest it just built into the
// umbrella's values, so the chart already holds the answer, and reading it there
// means the reported image is by construction the image that will run. Querying
// the container registry for "the newest tag" would answer a different question
// and could disagree with the chart on a slow or failed publish.
type PublishedApp struct {
	// Published is false when the chart has never been pushed. It is not an
	// error: between `POST /apps` and the app's first green release run, this is
	// the normal, expected state.
	Published bool

	ChartVersion string

	ImageRepository string
	ImageTag        string
	ImageDigest     string

	// ImageSourceCommit is the commit the image was built from, taken from the
	// published chart's appVersion — release CI packages a master build with
	// `--app-version <short sha>` (verified on the live registry: chart
	// 0.3.0-sha-35a48cd carries appVersion 35a48cd).
	//
	// It exists because a deployment REQUIRES provenance: the domain wants a
	// >=7-character git sha for source.gitSha, since the short sha drives the
	// deployment version and component id. Without it the caller has to invent
	// one, which is what the portal did — it sent the template repository's HEAD
	// for every app. Empty when the chart's appVersion is not a sha (a tagged
	// release publishes the semver there), and empty is honest: a caller with no
	// provenance should be refused, not handed a plausible lie.
	ImageSourceCommit string
}

// AppArtifactResolver reports what an application can currently deploy.
type AppArtifactResolver interface {
	// ResolveLatest returns the highest published version of chartName in
	// chartRepository, and the image that version runs, read from the chart's
	// values under appValuesKey.
	//
	// A chart that has never been published returns Published=false and a nil
	// error. Every other failure — registry unreachable, malformed chart — is an
	// error, because reporting "your app has not built yet" for a registry
	// outage leaves the caller waiting for something that already happened.
	ResolveLatest(ctx context.Context, chartRepository, chartName, appValuesKey string) (PublishedApp, error)
}

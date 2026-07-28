package oci

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/registry"
	"oras.land/oras-go/v2/registry/remote/errcode"

	"github.com/myorg/platform-orchestrator/internal/application/port"
)

// ResolveLatest reports the highest published version of a scaffolded app's
// umbrella and the image that version runs.
//
// The constraint is deliberately "*" with prereleases allowed. The umbrella
// publishes ONLY prerelease versions off master — 0.3.0-sha-<short> — and there
// is no clean 0.3.0 to find (`helm pull --version 0.3.0` ⇒ not found). A
// resolver that asked for "the release version" would answer "nothing published"
// for an app whose CI has been green for hours.
func (r *Resolver) ResolveLatest(ctx context.Context, chartRepository, chartName, appValuesKey string) (port.PublishedApp, error) {
	if chartRepository == "" || chartName == "" {
		return port.PublishedApp{}, fmt.Errorf("oci resolve latest: repository and chart name are required")
	}
	repoRef := path(chartRepository, chartName)

	if err := r.ensureLogin(ctx, repoRef); err != nil {
		return port.PublishedApp{}, fmt.Errorf("oci resolve latest: %w", err)
	}

	tags, err := r.client.Tags(repoRef)
	if err != nil {
		if r.isNotPublished(repoRef, err) {
			r.logger.InfoContext(ctx, "app chart is not published yet",
				slog.String("repository", repoRef),
				slog.String("registryError", err.Error()))
			return port.PublishedApp{}, nil
		}
		return port.PublishedApp{}, fmt.Errorf("oci resolve latest: list tags for %s: %w", repoRef, err)
	}
	if len(tags) == 0 {
		return port.PublishedApp{}, nil
	}

	chosen, err := selectVersion(tags, "*", true)
	if err != nil {
		// Tags exist but none parse as semver. That is a published-but-unusable
		// chart, not an unpublished one, and saying "not published yet" would
		// send the caller off to wait for a build that already ran.
		return port.PublishedApp{}, fmt.Errorf("oci resolve latest %s: %w", repoRef, err)
	}

	ref := repoRef + ":" + chosen
	pulled, err := r.client.Pull(ref, registry.PullOptWithChart(true))
	if err != nil {
		return port.PublishedApp{}, fmt.Errorf("oci resolve latest: pull %s: %w", ref, err)
	}
	if pulled.Chart == nil || len(pulled.Chart.Data) == 0 {
		return port.PublishedApp{}, fmt.Errorf("oci resolve latest: pulled %s but got no chart data", ref)
	}

	chrt, err := loader.LoadArchive(bytes.NewReader(pulled.Chart.Data))
	if err != nil {
		return port.PublishedApp{}, fmt.Errorf("oci resolve latest: load %s: %w", ref, err)
	}

	repository, tag, digest := appImage(chrt.Values, appValuesKey)
	sourceCommit := ""
	if chrt.Metadata != nil {
		sourceCommit = shortSHA(chrt.Metadata.AppVersion)
	}
	r.logger.InfoContext(ctx, "resolved app artifacts",
		slog.String("repository", repoRef),
		slog.String("version", chosen),
		slog.String("appValuesKey", appValuesKey),
		slog.String("imageRepository", repository),
		slog.String("imageDigest", digest),
	)

	return port.PublishedApp{
		Published:         true,
		ChartVersion:      chosen,
		ImageRepository:   repository,
		ImageTag:          tag,
		ImageDigest:       digest,
		ImageSourceCommit: sourceCommit,
	}, nil
}

// shortSHA returns v when it looks like an abbreviated git commit and "" when it
// does not. Release CI publishes a master build with `--app-version <short sha>`,
// but a TAGGED release publishes the semver there instead — and the template's
// own chart ships `appVersion: "latest"`. Returning those as provenance would
// hand the caller a value the deployment domain rejects, or worse, accepts as a
// commit that never existed.
func shortSHA(v string) string {
	if len(v) < 7 || len(v) > 40 {
		return ""
	}
	for _, c := range v {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return ""
		}
	}
	return v
}

// appImage reads the app subchart's image block out of the umbrella's merged
// values. Release CI pins the digest it just built there, so this is the image
// the chart will actually run rather than whatever the container registry
// happens to hold now.
//
// Missing pieces come back empty rather than erroring. A chart published before
// the digest pin existed is a real thing to report — the caller can see it has
// no digest and refuse to offer it — whereas failing here would hide the version
// that was found.
func appImage(values map[string]any, appValuesKey string) (repository, tag, digest string) {
	if appValuesKey == "" {
		return "", "", ""
	}
	app, ok := values[appValuesKey].(map[string]any)
	if !ok {
		return "", "", ""
	}
	image, ok := app["image"].(map[string]any)
	if !ok {
		return "", "", ""
	}
	str := func(k string) string {
		s, _ := image[k].(string)
		return s
	}
	return str("repository"), str("tag"), str("digest")
}

// isNotPublished reports whether a tag-listing failure means "this chart has
// never been pushed" rather than "the registry is having a bad day".
//
// GHCR answers 401 for a repository that does not exist, not 404: the token
// exchange happens before existence is checked, so an anonymous client asking
// about a chart nobody has pushed is told it is unauthorized. 403 and 404 are
// folded in for registries that answer either.
//
// That compromise only holds while the read is ANONYMOUS, and it cost real time:
// every scaffolded app's repository is private, so its umbrella package is
// private too, and on 2026-07-28 three apps whose release CI had been green for
// days were all reporting "waiting for the first build" — for charts that were
// pushed and sitting in GHCR. A 401 meaning "you may not look" was being read as
// "there is nothing there", and the portal polls a waiting state forever.
//
// So the classification depends on whether a credential is configured for that
// host. Authenticated, 401/403 is a real authentication failure and saying "not
// published yet" would hide it; only 404 still means never pushed. Anonymous,
// the old ambiguity is genuine and the old compromise stands — breaking the poll
// for every app would be the worse trade.
func (r *Resolver) isNotPublished(repoRef string, err error) bool {
	var resp *errcode.ErrorResponse
	if !errors.As(err, &resp) {
		return false
	}
	switch resp.StatusCode {
	case http.StatusNotFound:
		return true
	case http.StatusUnauthorized, http.StatusForbidden:
		return !r.authenticated(repoRef)
	default:
		return false
	}
}

// compile-time proof the adapter satisfies the port.
var _ port.AppArtifactResolver = (*Resolver)(nil)

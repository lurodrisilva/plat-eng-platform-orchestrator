package oci

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/Masterminds/semver/v3"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/registry"

	"github.com/myorg/platform-orchestrator/internal/application/port"
)

// Resolver implements port.ChartResolver against an OCI registry.
//
// This is the resolver the platform actually uses (ADR-0021): Phase D publishes
// the umbrella as a packaged OCI chart to GHCR — dependencies bundled and the
// image digest already pinned — at versions like 0.2.0-sha-<short> that have no
// git tag. The GitHub-API resolver in the sibling package lists git tags and
// downloads a source tarball, so it cannot see those versions and would throw
// the digest pin away; it stays only for git-sourced charts.
type Resolver struct {
	client *registry.Client
	logger *slog.Logger

	// creds maps a registry host to the credential to log in with, and loggedIn
	// records the hosts already logged in — helm's Login writes a credentials
	// file, so repeating it per request is wasted work, not a correctness issue.
	mu       sync.Mutex
	creds    map[string]CredentialFunc
	loggedIn map[string]bool
}

// ResolverOption configures a Resolver.
type ResolverOption func(*Resolver)

// WithHostCredential makes the resolver log in to host before reading from it.
//
// A scaffolded app's repository is PRIVATE, so the umbrella chart its release CI
// pushes to GHCR is a private package. Read anonymously, GHCR answers 401 for a
// private chart and 401 for a chart that was never pushed — the same status for
// "you may not look" and "there is nothing there". That ambiguity is why
// isNotPublished treats 401 as not-published, and why an app whose CI went green
// days ago can sit in the portal reporting "waiting for the first build".
//
// Supplying a credential removes the ambiguity rather than papering over it:
// authenticated, a missing repository is a 404 and a private one is readable.
func WithHostCredential(host string, c CredentialFunc) ResolverOption {
	return func(r *Resolver) {
		if host == "" || c == nil {
			return
		}
		if r.creds == nil {
			r.creds = map[string]CredentialFunc{}
		}
		r.creds[host] = c
	}
}

// NewResolver builds an OCI chart resolver. Without a host credential the
// registry client authenticates from the ambient Docker/Helm config if present,
// and reads anonymously otherwise.
func NewResolver(logger *slog.Logger, opts ...ResolverOption) (*Resolver, error) {
	client, err := registry.NewClient()
	if err != nil {
		return nil, fmt.Errorf("oci resolver: new registry client: %w", err)
	}
	r := &Resolver{client: client, logger: logger, loggedIn: map[string]bool{}}
	for _, opt := range opts {
		opt(r)
	}
	return r, nil
}

// registryHost returns the host of an OCI repository reference — the segment
// before the first slash of e.g. ghcr.io/lurodrisilva/helm-charts/x-umbrella.
func registryHost(repository string) string {
	host, _, _ := strings.Cut(repository, "/")
	return host
}

// authenticated reports whether a credential is configured for repoRef's host.
// The classification of a 401 depends on it: anonymous, 401 is ambiguous;
// authenticated, it is a real authentication failure and must not be reported as
// "not published yet".
func (r *Resolver) authenticated(repoRef string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.creds[registryHost(repoRef)]
	return ok
}

// ensureLogin logs in to repoRef's host once, if a credential is configured.
// A login failure is returned rather than swallowed: continuing anonymously
// would turn a bad credential into "not published yet", which is the exact
// misreport this option exists to prevent.
func (r *Resolver) ensureLogin(ctx context.Context, repoRef string) error {
	host := registryHost(repoRef)

	r.mu.Lock()
	cred, ok := r.creds[host]
	done := r.loggedIn[host]
	r.mu.Unlock()

	if !ok || done {
		return nil
	}

	user, pass, err := cred(ctx)
	if err != nil {
		return fmt.Errorf("acquire registry credential for %s: %w", host, err)
	}
	if err := r.client.Login(host, registry.LoginOptBasicAuth(user, pass)); err != nil {
		return fmt.Errorf("registry login to %s: %w", host, err)
	}

	r.mu.Lock()
	r.loggedIn[host] = true
	r.mu.Unlock()

	r.logger.InfoContext(ctx, "authenticated to registry", slog.String("host", host))
	return nil
}

// Resolve selects the highest chart version satisfying constraint and pulls its
// packaged bytes.
//
//	repository  the OCI repository holding the chart, WITHOUT the oci:// scheme
//	            and WITHOUT the chart name, e.g. ghcr.io/lurodrisilva/helm-charts
//	name        the chart name, the final path segment, e.g. hex-scaffold-umbrella
//	constraint  a semver constraint (e.g. ">=0.2.0", "0.2.0-sha-6d234ca", "*")
//	allowPre    whether prerelease versions (the master-build 0.2.0-sha-… line)
//	            are eligible candidates at all
//
// Prerelease matching follows the standard Masterminds/helm rule, and this is
// deliberate: a NON-prerelease bound like ">=0.2.0" never matches a prerelease
// even with allowPre=true — that is the "don't deploy a master build when asked
// for a release" guarantee. To include master builds under a bound, name a
// prerelease bound (">=0.2.0-0"), or pass "*" / "" for the highest available.
// An exact prerelease ("0.2.0-sha-6d234ca") always matches itself.
func (r *Resolver) Resolve(ctx context.Context, repository, name, constraint string, allowPre bool) (port.ResolvedChart, error) {
	repoRef := path(repository, name)

	if err := r.ensureLogin(ctx, repoRef); err != nil {
		return port.ResolvedChart{}, fmt.Errorf("oci resolve: %w", err)
	}

	tags, err := r.client.Tags(repoRef)
	if err != nil {
		return port.ResolvedChart{}, fmt.Errorf("oci resolve: list tags for %s: %w", repoRef, err)
	}
	if len(tags) == 0 {
		return port.ResolvedChart{}, fmt.Errorf("oci resolve: no tags at %s", repoRef)
	}

	chosen, err := selectVersion(tags, constraint, allowPre)
	if err != nil {
		return port.ResolvedChart{}, fmt.Errorf("oci resolve %s: %w", repoRef, err)
	}

	ref := repoRef + ":" + chosen
	pulled, err := r.client.Pull(ref, registry.PullOptWithChart(true))
	if err != nil {
		return port.ResolvedChart{}, fmt.Errorf("oci resolve: pull %s: %w", ref, err)
	}
	if pulled.Chart == nil || len(pulled.Chart.Data) == 0 {
		return port.ResolvedChart{}, fmt.Errorf("oci resolve: pulled %s but got no chart data", ref)
	}

	digest := ""
	if pulled.Manifest != nil {
		digest = pulled.Manifest.Digest
	}
	r.logger.InfoContext(ctx, "resolved chart from OCI",
		slog.String("repository", repoRef),
		slog.String("version", chosen),
		slog.String("digest", digest),
	)

	// The application subchart's values key, read from the chart we just pulled.
	// A parse failure here is not fatal: the archive is still deployable, and the
	// one caller that needs the key refuses on its own when it is empty rather
	// than composing values it cannot address.
	appValuesKey := ""
	if chrt, err := loader.LoadArchive(bytes.NewReader(pulled.Chart.Data)); err == nil {
		appValuesKey = appValuesKeyOf(chrt)
	} else {
		r.logger.WarnContext(ctx, "could not read the resolved chart's dependencies",
			slog.String("repository", repoRef), slog.String("error", err.Error()))
	}

	return port.ResolvedChart{
		SourceRepository: repository,
		ChartName:        name,
		ResolvedVersion:  chosen,
		ResolvedTag:      chosen,
		ArchiveBytes:     pulled.Chart.Data,
		AppValuesKey:     appValuesKey,
	}, nil
}

// appValuesKeyOf maps helm's dependency metadata onto the shared rule in the
// port package, so this adapter and the GitHub one cannot disagree about which
// subchart is the application.
func appValuesKeyOf(chrt *chart.Chart) string {
	if chrt == nil || chrt.Metadata == nil {
		return ""
	}
	deps := make([]port.ChartDependency, 0, len(chrt.Metadata.Dependencies))
	for _, d := range chrt.Metadata.Dependencies {
		if d == nil {
			continue
		}
		deps = append(deps, port.ChartDependency{Name: d.Name, Alias: d.Alias, Repository: d.Repository})
	}
	return port.AppValuesKeyOf(deps)
}

// selectVersion picks the highest tag satisfying constraint. An empty or "*"
// constraint means "highest available". Prereleases are excluded unless allowPre
// or the constraint itself names a prerelease (Masterminds treats a bare
// constraint as prerelease-excluding, which is what we want by default).
func selectVersion(tags []string, constraint string, allowPre bool) (string, error) {
	var check *semver.Constraints
	if c := strings.TrimSpace(constraint); c != "" && c != "*" {
		var err error
		check, err = semver.NewConstraint(c)
		if err != nil {
			return "", fmt.Errorf("invalid version constraint %q: %w", constraint, err)
		}
	}

	type candidate struct {
		raw string
		ver *semver.Version
	}
	var candidates []candidate
	for _, t := range tags {
		v, err := semver.NewVersion(t)
		if err != nil {
			continue // non-semver tag; ignore
		}
		if v.Prerelease() != "" && !allowPre {
			continue
		}
		if check != nil && !check.Check(v) {
			continue
		}
		candidates = append(candidates, candidate{raw: t, ver: v})
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("no tag satisfies constraint %q (allowPrerelease=%t)", constraint, allowPre)
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].ver.GreaterThan(candidates[j].ver)
	})
	return candidates[0].raw, nil
}

// path joins a repository and chart name into an OCI reference base, tolerating
// a stray oci:// scheme or trailing slash on the repository.
func path(repository, name string) string {
	repo := strings.TrimSuffix(strings.TrimPrefix(repository, "oci://"), "/")
	return repo + "/" + name
}

// compile-time proof the adapter satisfies the port.
var _ port.ChartResolver = (*Resolver)(nil)

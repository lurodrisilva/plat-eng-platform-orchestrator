package oci

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
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
}

// NewResolver builds an OCI chart resolver. The registry client authenticates
// from the ambient Docker/Helm config if present; public charts (the J3 case)
// need no credential.
func NewResolver(logger *slog.Logger) (*Resolver, error) {
	client, err := registry.NewClient()
	if err != nil {
		return nil, fmt.Errorf("oci resolver: new registry client: %w", err)
	}
	return &Resolver{client: client, logger: logger}, nil
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

	return port.ResolvedChart{
		SourceRepository: repository,
		ChartName:        name,
		ResolvedVersion:  chosen,
		ResolvedTag:      chosen,
		ArchiveBytes:     pulled.Chart.Data,
	}, nil
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

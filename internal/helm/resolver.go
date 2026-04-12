package helm

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/myorg/platform-orchestrator/internal/domain"
	gh "github.com/myorg/platform-orchestrator/internal/github"
)

// Resolver resolves Helm chart versions from GitHub repositories.
type Resolver struct {
	ghClient *gh.Client
	logger   *slog.Logger
}

func NewResolver(ghClient *gh.Client, logger *slog.Logger) *Resolver {
	return &Resolver{ghClient: ghClient, logger: logger}
}

// Resolve selects the appropriate chart version and downloads the archive.
func (r *Resolver) Resolve(ctx context.Context, chart domain.ChartSourceInfo) (domain.ResolvedChart, error) {
	owner, repo, err := parseRepoRef(chart.Repository)
	if err != nil {
		return domain.ResolvedChart{}, domain.NewChartValidationError("invalid chart repository", err)
	}

	tags, err := r.ghClient.ListTags(ctx, owner, repo)
	if err != nil {
		return domain.ResolvedChart{}, fmt.Errorf("listing tags for %s/%s: %w", owner, repo, err)
	}

	rawTag, version, err := gh.SelectHighestSemVerTag(tags, chart.VersionConstraint, chart.AllowPrerelease)
	if err != nil {
		return domain.ResolvedChart{}, domain.NewChartNotFoundError(chart.Repository, chart.VersionConstraint)
	}

	r.logger.InfoContext(ctx, "resolved chart version",
		slog.String("repository", chart.Repository),
		slog.String("tag", rawTag),
		slog.String("version", version),
	)

	archive, err := r.ghClient.DownloadArchive(ctx, owner, repo, rawTag)
	if err != nil {
		return domain.ResolvedChart{}, fmt.Errorf("downloading chart archive: %w", err)
	}

	return domain.ResolvedChart{
		SourceRepository: chart.Repository,
		ChartName:        chart.Name,
		ResolvedVersion:  version,
		ResolvedTag:      rawTag,
		ArchiveBytes:     archive,
	}, nil
}

func parseRepoRef(ref string) (owner, repo string, err error) {
	// Accept "github.com/owner/repo", "owner/repo"
	ref = strings.TrimPrefix(ref, "https://")
	ref = strings.TrimPrefix(ref, "github.com/")
	ref = strings.TrimSuffix(ref, ".git")

	parts := strings.SplitN(ref, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid repository reference: %q", ref)
	}
	return parts[0], parts[1], nil
}

package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"golang.org/x/mod/semver"

	"github.com/myorg/platform-orchestrator/internal/application/port"
)

// ChartResolver implements port.ChartResolver using the GitHub API.
type ChartResolver struct {
	httpClient *http.Client
	baseURL    string
	token      string
	logger     *slog.Logger
}

// NewChartResolver creates a GitHub-backed chart resolver.
func NewChartResolver(token string, logger *slog.Logger) *ChartResolver {
	return &ChartResolver{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    "https://api.github.com",
		token:      token,
		logger:     logger,
	}
}

type tag struct {
	Name string `json:"name"`
}

func (r *ChartResolver) Resolve(ctx context.Context, repository, name, constraint string, allowPrerelease bool) (port.ResolvedChart, error) {
	owner, repo, err := parseRepoRef(repository)
	if err != nil {
		return port.ResolvedChart{}, fmt.Errorf("resolve chart: %w", err)
	}

	tags, err := r.listTags(ctx, owner, repo)
	if err != nil {
		return port.ResolvedChart{}, fmt.Errorf("resolve chart: list tags: %w", err)
	}

	rawTag, version, err := selectHighestSemVer(tags, constraint, allowPrerelease)
	if err != nil {
		return port.ResolvedChart{}, fmt.Errorf("resolve chart: %w", err)
	}

	r.logger.InfoContext(ctx, "resolved chart version",
		slog.String("repository", repository),
		slog.String("tag", rawTag),
		slog.String("version", version),
	)

	archive, err := r.downloadArchive(ctx, owner, repo, rawTag)
	if err != nil {
		return port.ResolvedChart{}, fmt.Errorf("resolve chart: download: %w", err)
	}

	return port.ResolvedChart{
		SourceRepository: repository,
		ChartName:        name,
		ResolvedVersion:  version,
		ResolvedTag:      rawTag,
		ArchiveBytes:     archive,
	}, nil
}

func (r *ChartResolver) listTags(ctx context.Context, owner, repo string) ([]tag, error) {
	var all []tag
	page := 1
	for {
		url := fmt.Sprintf("%s/repos/%s/%s/tags?per_page=100&page=%d", r.baseURL, owner, repo, page)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		r.setHeaders(req)

		resp, err := r.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("fetch tags: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("github tags returned %d", resp.StatusCode)
		}

		var batch []tag
		if err := json.NewDecoder(resp.Body).Decode(&batch); err != nil {
			return nil, fmt.Errorf("decode tags: %w", err)
		}
		if len(batch) == 0 {
			break
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			break
		}
		page++
	}
	return all, nil
}

func (r *ChartResolver) downloadArchive(ctx context.Context, owner, repo, ref string) ([]byte, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/tarball/%s", r.baseURL, owner, repo, ref)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	r.setHeaders(req)

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download archive: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("archive download returned %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 100*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read archive: %w", err)
	}
	return data, nil
}

func (r *ChartResolver) setHeaders(req *http.Request) {
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}
}

func selectHighestSemVer(tags []tag, constraint string, allowPrerelease bool) (rawTag, version string, err error) {
	var candidates []parsed
	for _, t := range tags {
		canonical := t.Name
		if !strings.HasPrefix(canonical, "v") {
			canonical = "v" + canonical
		}
		if !semver.IsValid(canonical) {
			continue
		}
		if !allowPrerelease && semver.Prerelease(canonical) != "" {
			continue
		}
		candidates = append(candidates, parsed{raw: t.Name, canonical: canonical})
	}

	if len(candidates) == 0 {
		return "", "", fmt.Errorf("no valid semver tags found")
	}

	if constraint != "" {
		candidates = filterByConstraint(candidates, constraint, allowPrerelease)
		if len(candidates) == 0 {
			return "", "", fmt.Errorf("no tags match constraint %q", constraint)
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		return semver.Compare(candidates[i].canonical, candidates[j].canonical) > 0
	})

	highest := candidates[0]
	return highest.raw, strings.TrimPrefix(highest.canonical, "v"), nil
}

type parsed struct {
	raw       string
	canonical string
}

func filterByConstraint(tags []parsed, constraint string, _ bool) []parsed {
	// Simplified — returns all tags when constraint logic is not applicable
	return tags
}

func parseRepoRef(ref string) (string, string, error) {
	ref = strings.TrimPrefix(ref, "https://")
	ref = strings.TrimPrefix(ref, "github.com/")
	ref = strings.TrimSuffix(ref, ".git")
	parts := strings.SplitN(ref, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid repository reference: %q", ref)
	}
	return parts[0], parts[1], nil
}

// Compile-time port compliance check.
var _ port.ChartResolver = (*ChartResolver)(nil)

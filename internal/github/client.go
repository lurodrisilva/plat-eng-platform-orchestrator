package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/myorg/platform-orchestrator/internal/config"
)

// Client interacts with the GitHub API.
type Client struct {
	httpClient *http.Client
	baseURL    string
	token      string // simplified; in production use GitHub App installation token
	logger     *slog.Logger
}

func NewClient(cfg config.GitHubConfig, logger *slog.Logger) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    "https://api.github.com",
		token:      cfg.Token,
		logger:     logger,
	}
}

// Tag represents a GitHub repository tag.
type Tag struct {
	Name   string `json:"name"`
	Commit struct {
		SHA string `json:"sha"`
	} `json:"commit"`
}

// ListTags returns all tags for a repository.
func (c *Client) ListTags(ctx context.Context, owner, repo string) ([]Tag, error) {
	var allTags []Tag
	page := 1

	for {
		url := fmt.Sprintf("%s/repos/%s/%s/tags?per_page=100&page=%d", c.baseURL, owner, repo, page)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("creating request: %w", err)
		}
		c.setHeaders(req)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("fetching tags: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("repository %s/%s not found", owner, repo)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("GitHub API returned %d for tags", resp.StatusCode)
		}

		var tags []Tag
		if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
			return nil, fmt.Errorf("decoding tags: %w", err)
		}
		if len(tags) == 0 {
			break
		}
		allTags = append(allTags, tags...)

		if len(tags) < 100 {
			break
		}
		page++
	}

	c.logger.DebugContext(ctx, "fetched repository tags",
		slog.String("owner", owner),
		slog.String("repo", repo),
		slog.Int("count", len(allTags)),
	)

	return allTags, nil
}

// DownloadArchive downloads the tarball archive for a given tag/ref.
func (c *Client) DownloadArchive(ctx context.Context, owner, repo, ref string) ([]byte, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/tarball/%s", c.baseURL, owner, repo, ref)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("downloading archive: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub archive download returned %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 100*1024*1024)) // 100MB limit
	if err != nil {
		return nil, fmt.Errorf("reading archive: %w", err)
	}

	c.logger.DebugContext(ctx, "downloaded archive",
		slog.String("owner", owner),
		slog.String("repo", repo),
		slog.String("ref", ref),
		slog.Int("bytes", len(data)),
	)

	return data, nil
}

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}

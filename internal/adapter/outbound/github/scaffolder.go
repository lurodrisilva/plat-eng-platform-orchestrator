package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"github.com/myorg/platform-orchestrator/internal/application/port"
)

// nameRE constrains a new application (and repository) name to a GitHub-safe,
// DNS-label-ish slug: lowercase alphanumerics and hyphens, 1-39 chars. Validated
// before any API call so a malformed name can never be interpolated into a URL.
var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,38}$`)

// Scaffolder implements port.Scaffolder by dispatching a GitHub Actions
// "scaffold-new-app" workflow via a GitHub App, and by reading whether the
// scaffolded repository exists. It does NOT clone, render, or push — that runs
// inside the dispatched workflow (ADR-0009).
type Scaffolder struct {
	auth            *AppAuth
	scaffolderOwner string
	scaffolderRepo  string
	workflowFile    string
	ref             string
	newRepoOwner    string
	httpClient      *http.Client
	baseURL         string
	logger          *slog.Logger
}

// ScaffolderConfig carries the settings NewScaffolder needs. The App fields
// authenticate as a GitHub App installation; the scaffolder* fields locate the
// workflow to dispatch; newRepoOwner is the org/user the new repo lands under.
type ScaffolderConfig struct {
	AppID          int64
	InstallationID int64
	PrivateKeyPEM  string

	ScaffolderOwner string
	ScaffolderRepo  string
	WorkflowFile    string
	Ref             string
	NewRepoOwner    string
}

// NewScaffolder builds a Scaffolder, validating that every required field is
// set. It performs no network I/O.
func NewScaffolder(cfg ScaffolderConfig, logger *slog.Logger) (*Scaffolder, error) {
	if cfg.AppID == 0 {
		return nil, fmt.Errorf("github scaffolder: appID is required")
	}
	if cfg.InstallationID == 0 {
		return nil, fmt.Errorf("github scaffolder: installationID is required")
	}
	if cfg.PrivateKeyPEM == "" {
		return nil, fmt.Errorf("github scaffolder: privateKeyPEM is required")
	}
	if cfg.ScaffolderOwner == "" {
		return nil, fmt.Errorf("github scaffolder: scaffolderOwner is required")
	}
	if cfg.ScaffolderRepo == "" {
		return nil, fmt.Errorf("github scaffolder: scaffolderRepo is required")
	}
	if cfg.WorkflowFile == "" {
		return nil, fmt.Errorf("github scaffolder: workflowFile is required")
	}
	if cfg.NewRepoOwner == "" {
		return nil, fmt.Errorf("github scaffolder: newRepoOwner is required")
	}

	ref := cfg.Ref
	if ref == "" {
		ref = "main"
	}

	httpClient := &http.Client{Timeout: 30 * time.Second}
	return &Scaffolder{
		auth: &AppAuth{
			appID:          cfg.AppID,
			installationID: cfg.InstallationID,
			privateKeyPEM:  cfg.PrivateKeyPEM,
			httpClient:     httpClient,
			baseURL:        "https://api.github.com",
		},
		scaffolderOwner: cfg.ScaffolderOwner,
		scaffolderRepo:  cfg.ScaffolderRepo,
		workflowFile:    cfg.WorkflowFile,
		ref:             ref,
		newRepoOwner:    cfg.NewRepoOwner,
		httpClient:      httpClient,
		baseURL:         "https://api.github.com",
		logger:          logger,
	}, nil
}

// Dispatch validates the app name, mints an installation token, and fires the
// scaffold-new-app workflow_dispatch. GitHub answers 204 on success; anything
// else is an error. The returned result points at the repository the workflow
// will create — it does not confirm the repo exists yet (use RepoStatus).
func (s *Scaffolder) Dispatch(ctx context.Context, req port.ScaffoldRequest) (port.ScaffoldResult, error) {
	if !nameRE.MatchString(req.Name) {
		return port.ScaffoldResult{}, fmt.Errorf("invalid app name %q: must be lowercase alphanumeric and hyphens, 1-39 chars", req.Name)
	}

	token, err := s.auth.installationToken(ctx)
	if err != nil {
		return port.ScaffoldResult{}, fmt.Errorf("dispatch scaffold: %w", err)
	}

	payload, err := json.Marshal(map[string]any{
		"ref": s.ref,
		"inputs": map[string]string{
			"appName":     req.Name,
			"owner":       s.newRepoOwner,
			"domain":      req.Domain,
			"description": req.Description,
		},
	})
	if err != nil {
		return port.ScaffoldResult{}, fmt.Errorf("dispatch scaffold: marshal payload: %w", err)
	}

	url := fmt.Sprintf("%s/repos/%s/%s/actions/workflows/%s/dispatches",
		s.baseURL, s.scaffolderOwner, s.scaffolderRepo, s.workflowFile)

	status, body, err := s.doRequest(ctx, http.MethodPost, url, token, payload)
	if err != nil {
		return port.ScaffoldResult{}, fmt.Errorf("dispatch scaffold: %w", err)
	}
	if status != http.StatusNoContent {
		return port.ScaffoldResult{}, fmt.Errorf("dispatch scaffold: workflow dispatch returned %d: %s", status, string(body))
	}

	s.logger.InfoContext(ctx, "dispatched scaffold-new-app workflow",
		slog.String("app", req.Name),
		slog.String("workflow", s.workflowFile),
		slog.String("owner", s.newRepoOwner),
		slog.String("actor", req.Actor),
	)

	return port.ScaffoldResult{
		AppName:    req.Name,
		Repository: s.newRepoOwner + "/" + req.Name,
		RepoURL:    "https://github.com/" + s.newRepoOwner + "/" + req.Name,
	}, nil
}

// repoResponse is the subset of the GitHub repo object we consume.
type repoResponse struct {
	DefaultBranch string `json:"default_branch"`
	HTMLURL       string `json:"html_url"`
}

// RepoStatus reads whether the scaffolded repository exists. 200 → Exists=true
// with its default branch and browse URL; 404 → Exists=false (not an error, the
// workflow simply has not created it yet); anything else is an error.
func (s *Scaffolder) RepoStatus(ctx context.Context, name string) (port.RepoStatus, error) {
	if !nameRE.MatchString(name) {
		return port.RepoStatus{}, fmt.Errorf("invalid app name %q: must be lowercase alphanumeric and hyphens, 1-39 chars", name)
	}

	token, err := s.auth.installationToken(ctx)
	if err != nil {
		return port.RepoStatus{}, fmt.Errorf("repo status: %w", err)
	}

	url := fmt.Sprintf("%s/repos/%s/%s", s.baseURL, s.newRepoOwner, name)
	status, body, err := s.doRequest(ctx, http.MethodGet, url, token, nil)
	if err != nil {
		return port.RepoStatus{}, fmt.Errorf("repo status: %w", err)
	}

	switch status {
	case http.StatusOK:
		var repo repoResponse
		if err := json.Unmarshal(body, &repo); err != nil {
			return port.RepoStatus{}, fmt.Errorf("repo status: decode repo: %w", err)
		}
		return port.RepoStatus{
			Name:          name,
			Exists:        true,
			RepoURL:       repo.HTMLURL,
			DefaultBranch: repo.DefaultBranch,
		}, nil
	case http.StatusNotFound:
		return port.RepoStatus{Name: name, Exists: false}, nil
	default:
		return port.RepoStatus{}, fmt.Errorf("repo status: returned %d: %s", status, string(body))
	}
}

// doRequest performs one authenticated GitHub API call and returns the status
// code and raw body. Unlike the argocd doRequest it does not treat >=400 as an
// error itself, because callers here distinguish 204/404 from real failures.
func (s *Scaffolder) doRequest(ctx context.Context, method, url, token string, body []byte) (int, []byte, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return 0, nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("%s %s: %w", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("read response: %w", err)
	}
	return resp.StatusCode, respBody, nil
}

// Compile-time port compliance check.
var _ port.Scaffolder = (*Scaffolder)(nil)

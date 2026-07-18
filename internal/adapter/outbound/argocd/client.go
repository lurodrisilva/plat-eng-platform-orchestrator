package argocd

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/myorg/platform-orchestrator/internal/application/port"
)

// Client implements port.ArgoCD using the Argo CD REST API.
type Client struct {
	serverURL  string
	token      string
	httpClient *http.Client
	logger     *slog.Logger
}

// Option configures a Client.
type Option func(*Client)

// WithInsecureTLS skips TLS verification. In-cluster argocd-server runs in
// secure mode with a self-signed certificate, so a same-cluster client that
// reaches it over https://argocd-server.<ns>.svc must skip verification (the
// hop stays inside the cluster network). Do not use across trust boundaries.
func WithInsecureTLS() Option {
	return func(c *Client) {
		c.httpClient.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // in-cluster self-signed argocd
		}
	}
}

// NewClient creates an Argo CD API client.
func NewClient(serverURL, token string, logger *slog.Logger, opts ...Option) *Client {
	c := &Client{
		serverURL:  serverURL,
		token:      token,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		logger:     logger,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Client) ValidateProject(ctx context.Context, project, sourceRepo, destServer, destNS string) error {
	url := fmt.Sprintf("%s/api/v1/projects/%s", c.serverURL, project)
	body, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("fetch project %q: %w", project, err)
	}

	var proj struct {
		Spec struct {
			SourceRepos  []string `json:"sourceRepos"`
			Destinations []struct {
				Server    string `json:"server"`
				Namespace string `json:"namespace"`
			} `json:"destinations"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(body, &proj); err != nil {
		return fmt.Errorf("decode project: %w", err)
	}

	sourceOK := false
	for _, allowed := range proj.Spec.SourceRepos {
		if allowed == "*" || allowed == sourceRepo {
			sourceOK = true
			break
		}
	}
	if !sourceOK {
		return fmt.Errorf("source %q not allowed in project %q", sourceRepo, project)
	}

	destOK := false
	for _, d := range proj.Spec.Destinations {
		if (d.Server == "*" || d.Server == destServer) && (d.Namespace == "*" || d.Namespace == destNS) {
			destOK = true
			break
		}
	}
	if !destOK {
		return fmt.Errorf("destination %s/%s not allowed in project %q", destServer, destNS, project)
	}

	return nil
}

func (c *Client) CreateOrUpdate(ctx context.Context, spec port.ArgoAppSpec) error {
	app := map[string]any{
		"metadata": map[string]any{
			"name":        spec.Name,
			"namespace":   spec.Namespace,
			"labels":      spec.Labels,
			"annotations": spec.Annotations,
		},
		"spec": map[string]any{
			"project": spec.Project,
			"source": map[string]any{
				"repoURL":        spec.RepoURL,
				"chart":          spec.Chart,
				"targetRevision": spec.Version,
			},
			"destination": map[string]any{
				"server":    spec.DestServer,
				"namespace": spec.DestNS,
			},
			"syncPolicy": map[string]any{
				"automated":   map[string]any{"prune": true, "selfHeal": false},
				"syncOptions": []string{"CreateNamespace=false", "PruneLast=true", "ApplyOutOfSyncOnly=true"},
				"retry":       map[string]any{"limit": 2, "backoff": map[string]any{"duration": "10s", "factor": 2, "maxDuration": "60s"}},
			},
		},
	}

	payload, _ := json.Marshal(app)

	// Try create first
	createURL := fmt.Sprintf("%s/api/v1/applications", c.serverURL)
	_, err := c.doRequest(ctx, http.MethodPost, createURL, payload)
	if err != nil {
		// Attempt update
		updateURL := fmt.Sprintf("%s/api/v1/applications/%s", c.serverURL, spec.Name)
		if _, updateErr := c.doRequest(ctx, http.MethodPut, updateURL, payload); updateErr != nil {
			return fmt.Errorf("create/update app: create=%w, update=%w", err, updateErr)
		}
	}

	c.logger.InfoContext(ctx, "created/updated Argo CD application", slog.String("name", spec.Name))
	return nil
}

func (c *Client) Sync(ctx context.Context, appName string) error {
	url := fmt.Sprintf("%s/api/v1/applications/%s/sync", c.serverURL, appName)
	payload, _ := json.Marshal(map[string]any{"prune": true})
	_, err := c.doRequest(ctx, http.MethodPost, url, payload)
	if err != nil {
		// The app's automated syncPolicy may already be syncing it — that is
		// exactly the state this call wants, so an in-progress operation is
		// success, not failure. Makes Sync idempotent against a re-drive.
		if strings.Contains(err.Error(), "another operation is already in progress") {
			c.logger.InfoContext(ctx, "argo sync already in progress; treating as synced",
				slog.String("app", appName))
			return nil
		}
		return err
	}
	return nil
}

func (c *Client) Status(ctx context.Context, appName string) (port.ArgoAppStatus, error) {
	url := fmt.Sprintf("%s/api/v1/applications/%s", c.serverURL, appName)
	body, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return port.ArgoAppStatus{}, err
	}

	var app struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Status struct {
			Health struct {
				Status  string `json:"status"`
				Message string `json:"message"`
			} `json:"health"`
			Sync struct {
				Status string `json:"status"`
			} `json:"sync"`
		} `json:"status"`
	}
	if err := json.Unmarshal(body, &app); err != nil {
		return port.ArgoAppStatus{}, fmt.Errorf("decode app status: %w", err)
	}

	return port.ArgoAppStatus{
		Name:         app.Metadata.Name,
		SyncStatus:   app.Status.Sync.Status,
		HealthStatus: app.Status.Health.Status,
		Message:      app.Status.Health.Message,
	}, nil
}

func (c *Client) Delete(ctx context.Context, appName string) error {
	url := fmt.Sprintf("%s/api/v1/applications/%s", c.serverURL, appName)
	_, err := c.doRequest(ctx, http.MethodDelete, url, nil)
	return err
}

func (c *Client) doRequest(ctx context.Context, method, url string, body []byte) ([]byte, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%s %s returned %d: %s", method, url, resp.StatusCode, string(respBody))
	}
	return respBody, nil
}

// Compile-time port compliance check.
var _ port.ArgoCD = (*Client)(nil)

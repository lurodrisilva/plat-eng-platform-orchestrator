package argocd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// Client communicates with the Argo CD REST API.
type Client struct {
	serverURL  string
	token      string
	httpClient *http.Client
	logger     *slog.Logger
}

func NewClient(serverURL, token string, logger *slog.Logger) *Client {
	return &Client{
		serverURL: serverURL,
		token:     token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		logger: logger,
	}
}

// Application represents an Argo CD Application resource.
type Application struct {
	Metadata AppMetadata `json:"metadata"`
	Spec     AppSpec     `json:"spec"`
	Status   AppStatus  `json:"status,omitempty"`
}

type AppMetadata struct {
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

type AppSpec struct {
	Project     string      `json:"project"`
	Source      AppSource   `json:"source"`
	Destination Destination `json:"destination"`
	SyncPolicy  *SyncPolicy `json:"syncPolicy,omitempty"`
}

type AppSource struct {
	RepoURL        string `json:"repoURL"`
	Chart          string `json:"chart,omitempty"`
	TargetRevision string `json:"targetRevision"`
}

type Destination struct {
	Server    string `json:"server"`
	Namespace string `json:"namespace"`
}

type SyncPolicy struct {
	Automated   *AutomatedSync `json:"automated,omitempty"`
	SyncOptions []string       `json:"syncOptions,omitempty"`
	Retry       *RetryPolicy   `json:"retry,omitempty"`
}

type AutomatedSync struct {
	Prune    bool `json:"prune"`
	SelfHeal bool `json:"selfHeal"`
}

type RetryPolicy struct {
	Limit   int            `json:"limit"`
	Backoff RetryBackoff   `json:"backoff"`
}

type RetryBackoff struct {
	Duration    string `json:"duration"`
	Factor      int    `json:"factor"`
	MaxDuration string `json:"maxDuration"`
}

type AppStatus struct {
	Health HealthStatus `json:"health"`
	Sync   SyncStatus   `json:"sync"`
}

type HealthStatus struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type SyncStatus struct {
	Status   string `json:"status"`
	Revision string `json:"revision,omitempty"`
}

// AppProject represents an Argo CD AppProject.
type AppProject struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec AppProjectSpec `json:"spec"`
}

type AppProjectSpec struct {
	SourceRepos  []string              `json:"sourceRepos"`
	Destinations []ProjectDestination  `json:"destinations"`
}

type ProjectDestination struct {
	Server    string `json:"server"`
	Namespace string `json:"namespace"`
}

// GetProject retrieves an AppProject by name.
func (c *Client) GetProject(ctx context.Context, name string) (*AppProject, error) {
	url := fmt.Sprintf("%s/api/v1/projects/%s", c.serverURL, name)
	body, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	var project AppProject
	if err := json.Unmarshal(body, &project); err != nil {
		return nil, fmt.Errorf("decoding project: %w", err)
	}
	return &project, nil
}

// CreateApplication creates a new Argo CD Application.
func (c *Client) CreateApplication(ctx context.Context, app *Application) (*Application, error) {
	url := fmt.Sprintf("%s/api/v1/applications", c.serverURL)
	payload, err := json.Marshal(app)
	if err != nil {
		return nil, fmt.Errorf("marshaling application: %w", err)
	}
	body, err := c.doRequest(ctx, http.MethodPost, url, payload)
	if err != nil {
		return nil, err
	}
	var created Application
	if err := json.Unmarshal(body, &created); err != nil {
		return nil, fmt.Errorf("decoding created application: %w", err)
	}
	c.logger.InfoContext(ctx, "created Argo CD application",
		slog.String("name", created.Metadata.Name),
		slog.String("project", created.Spec.Project),
	)
	return &created, nil
}

// UpdateApplication updates an existing Argo CD Application.
func (c *Client) UpdateApplication(ctx context.Context, app *Application) (*Application, error) {
	url := fmt.Sprintf("%s/api/v1/applications/%s", c.serverURL, app.Metadata.Name)
	payload, err := json.Marshal(app)
	if err != nil {
		return nil, fmt.Errorf("marshaling application: %w", err)
	}
	body, err := c.doRequest(ctx, http.MethodPut, url, payload)
	if err != nil {
		return nil, err
	}
	var updated Application
	if err := json.Unmarshal(body, &updated); err != nil {
		return nil, fmt.Errorf("decoding updated application: %w", err)
	}
	return &updated, nil
}

// GetApplication retrieves an Application by name.
func (c *Client) GetApplication(ctx context.Context, name string) (*Application, error) {
	url := fmt.Sprintf("%s/api/v1/applications/%s", c.serverURL, name)
	body, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	var app Application
	if err := json.Unmarshal(body, &app); err != nil {
		return nil, fmt.Errorf("decoding application: %w", err)
	}
	return &app, nil
}

// DeleteApplication deletes an Argo CD Application.
func (c *Client) DeleteApplication(ctx context.Context, name string) error {
	url := fmt.Sprintf("%s/api/v1/applications/%s", c.serverURL, name)
	_, err := c.doRequest(ctx, http.MethodDelete, url, nil)
	if err != nil {
		c.logger.WarnContext(ctx, "failed to delete application", slog.String("name", name), slog.String("error", err.Error()))
	}
	return err
}

// SyncApplication triggers a sync for the named application.
func (c *Client) SyncApplication(ctx context.Context, name string) error {
	url := fmt.Sprintf("%s/api/v1/applications/%s/sync", c.serverURL, name)
	syncReq := map[string]any{
		"prune": true,
	}
	payload, _ := json.Marshal(syncReq)
	_, err := c.doRequest(ctx, http.MethodPost, url, payload)
	return err
}

// ValidateAppProject checks whether the given source and destination are allowed by the AppProject.
func (c *Client) ValidateAppProject(ctx context.Context, projectName, sourceRepo, destServer, destNamespace string) error {
	project, err := c.GetProject(ctx, projectName)
	if err != nil {
		return fmt.Errorf("fetching AppProject %q: %w", projectName, err)
	}

	// Validate source repo
	sourceAllowed := false
	for _, allowed := range project.Spec.SourceRepos {
		if allowed == "*" || allowed == sourceRepo {
			sourceAllowed = true
			break
		}
	}
	if !sourceAllowed {
		return fmt.Errorf("source %q not allowed in AppProject %q (allowed: %v)", sourceRepo, projectName, project.Spec.SourceRepos)
	}

	// Validate destination
	destAllowed := false
	for _, dest := range project.Spec.Destinations {
		serverMatch := dest.Server == "*" || dest.Server == destServer
		nsMatch := dest.Namespace == "*" || dest.Namespace == destNamespace
		if serverMatch && nsMatch {
			destAllowed = true
			break
		}
	}
	if !destAllowed {
		return fmt.Errorf("destination %s/%s not allowed in AppProject %q", destServer, destNamespace, projectName)
	}

	return nil
}

func (c *Client) doRequest(ctx context.Context, method, url string, body []byte) ([]byte, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, url, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%s %s returned %d: %s", method, url, resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// Package v1 defines the public API types for the Platform Orchestrator.
// These types are safe for external consumers to import.
package v1

import "time"

type CreateDeploymentRequest struct {
	Application   ApplicationInfo `json:"application"`
	Image         ImageInfo       `json:"image"`
	Chart         ChartInfo       `json:"chart"`
	Target        TargetInfo      `json:"target"`
	Values        map[string]any  `json:"values"`
	Source        SourceInfo      `json:"source"`
	CorrelationID string          `json:"correlationId"`
}

type ApplicationInfo struct {
	ID   string `json:"id"`
	Team string `json:"team"`
}

type ImageInfo struct {
	Repository string `json:"repository"`
	Tag        string `json:"tag"`
	Digest     string `json:"digest"`
}

type ChartInfo struct {
	Repository        string `json:"repository"`
	Name              string `json:"name"`
	VersionConstraint string `json:"versionConstraint"`
	AllowPrerelease   bool   `json:"allowPrerelease"`
}

type TargetInfo struct {
	Environment string `json:"environment"`
	Cluster     string `json:"cluster"`
	Namespace   string `json:"namespace"`
	AppProject  string `json:"appProject"`
}

type SourceInfo struct {
	GitSHA             string `json:"gitSha"`
	GitRef             string `json:"gitRef"`
	GitHubRunID        string `json:"githubRunId"`
	GitHubRunAttempt   int    `json:"githubRunAttempt"`
	WorkflowName       string `json:"workflowName"`
	Actor              string `json:"actor"`
	RepositoryFullName string `json:"repositoryFullName"`
}

type CreateDeploymentResponse struct {
	DeploymentID       string `json:"deploymentId"`
	TemporalWorkflowID string `json:"temporalWorkflowId"`
	TemporalRunID      string `json:"temporalRunId"`
	Status             string `json:"status"`
	StatusURL          string `json:"statusUrl"`
	CreatedAt          string `json:"createdAt"`
}

type DeploymentStatusResponse struct {
	DeploymentID       string         `json:"deploymentId"`
	Status             string         `json:"status"`
	Application        string         `json:"application"`
	Environment        string         `json:"environment"`
	Cluster            string         `json:"cluster"`
	Namespace          string         `json:"namespace"`
	Image              *ImageInfo     `json:"image,omitempty"`
	ChartVersion       string         `json:"chartVersion,omitempty"`
	ArgoAppName        string         `json:"argoAppName,omitempty"`
	ArgoSyncStatus     string         `json:"argoSyncStatus,omitempty"`
	ArgoHealthStatus   string         `json:"argoHealthStatus,omitempty"`
	TemporalWorkflowID string         `json:"temporalWorkflowId,omitempty"`
	StartedAt          time.Time      `json:"startedAt"`
	CompletedAt        *time.Time     `json:"completedAt,omitempty"`
	DurationMs         int64          `json:"durationMs,omitempty"`
	StateHistory       []StateEntry   `json:"stateHistory,omitempty"`
	Error              string         `json:"error,omitempty"`
}

type StateEntry struct {
	State string    `json:"state"`
	At    time.Time `json:"at"`
}

type ErrorResponse struct {
	Error     ErrorDetail `json:"error"`
	RequestID string      `json:"requestId"`
	Timestamp string      `json:"timestamp"`
}

type ErrorDetail struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

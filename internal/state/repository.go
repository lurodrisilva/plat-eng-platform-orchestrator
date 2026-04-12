package state

import (
	"context"
	"time"

	"github.com/myorg/platform-orchestrator/internal/domain"
)

// DeploymentDocument represents the current state of a deployment in DocumentDB.
type DeploymentDocument struct {
	ID            string              `json:"id" bson:"_id"`
	ApplicationID string             `json:"applicationId" bson:"applicationId"`
	Environment   string             `json:"environment" bson:"environment"`
	Cluster       string             `json:"cluster" bson:"cluster"`
	Namespace     string             `json:"namespace" bson:"namespace"`
	Team          string             `json:"team" bson:"team"`
	Status        domain.DeploymentState `json:"status" bson:"status"`
	Image         ImageDoc           `json:"image" bson:"image"`
	Chart         ChartDoc           `json:"chart" bson:"chart"`
	Source        SourceDoc          `json:"source" bson:"source"`
	ArgoCD        ArgoCDDoc          `json:"argocd" bson:"argocd"`
	Temporal      TemporalDoc        `json:"temporal" bson:"temporal"`
	CorrelationID string             `json:"correlationId" bson:"correlationId"`
	Error         string             `json:"error,omitempty" bson:"error,omitempty"`
	StartedAt     time.Time          `json:"startedAt" bson:"startedAt"`
	CompletedAt   *time.Time         `json:"completedAt,omitempty" bson:"completedAt,omitempty"`
	DurationMs    int64              `json:"durationMs,omitempty" bson:"durationMs,omitempty"`
	CreatedAt     time.Time          `json:"createdAt" bson:"createdAt"`
	UpdatedAt     time.Time          `json:"updatedAt" bson:"updatedAt"`
}

type ImageDoc struct {
	Repository string `json:"repository" bson:"repository"`
	Tag        string `json:"tag" bson:"tag"`
	Digest     string `json:"digest" bson:"digest"`
}

type ChartDoc struct {
	SourceRepository  string `json:"sourceRepository" bson:"sourceRepository"`
	SourceName        string `json:"sourceName" bson:"sourceName"`
	ResolvedVersion   string `json:"resolvedVersion" bson:"resolvedVersion"`
	DeploymentVersion string `json:"deploymentVersion" bson:"deploymentVersion"`
	OCIReference      string `json:"ociReference" bson:"ociReference"`
	OCIDigest         string `json:"ociDigest" bson:"ociDigest"`
}

type SourceDoc struct {
	GitSHA           string `json:"gitSha" bson:"gitSha"`
	GitRef           string `json:"gitRef" bson:"gitRef"`
	GitHubRunID      string `json:"githubRunId" bson:"githubRunId"`
	GitHubRunAttempt int    `json:"githubRunAttempt" bson:"githubRunAttempt"`
	WorkflowName     string `json:"workflowName" bson:"workflowName"`
	Actor            string `json:"actor" bson:"actor"`
	RepositoryFull   string `json:"repositoryFullName" bson:"repositoryFullName"`
}

type ArgoCDDoc struct {
	ApplicationName string     `json:"applicationName" bson:"applicationName"`
	SyncStatus      string     `json:"syncStatus" bson:"syncStatus"`
	HealthStatus    string     `json:"healthStatus" bson:"healthStatus"`
	LastSyncedAt    *time.Time `json:"lastSyncedAt,omitempty" bson:"lastSyncedAt,omitempty"`
}

type TemporalDoc struct {
	WorkflowID string `json:"workflowId" bson:"workflowId"`
	RunID      string `json:"runId" bson:"runId"`
	TaskQueue  string `json:"taskQueue" bson:"taskQueue"`
}

// EventDocument represents a state transition event in the audit trail.
type EventDocument struct {
	ID            string             `json:"id" bson:"_id"`
	DeploymentID  string             `json:"deploymentId" bson:"deploymentId"`
	ApplicationID string            `json:"applicationId" bson:"applicationId"`
	State         domain.DeploymentState `json:"state" bson:"state"`
	PreviousState *domain.DeploymentState `json:"previousState,omitempty" bson:"previousState,omitempty"`
	Timestamp     time.Time          `json:"timestamp" bson:"timestamp"`
	Details       map[string]any     `json:"details,omitempty" bson:"details,omitempty"`
	TraceID       string             `json:"traceId,omitempty" bson:"traceId,omitempty"`
	SpanID        string             `json:"spanId,omitempty" bson:"spanId,omitempty"`
}

// Repository defines the interface for deployment state persistence.
type Repository interface {
	UpsertDeployment(ctx context.Context, doc *DeploymentDocument) error
	GetDeployment(ctx context.Context, deploymentID string) (*DeploymentDocument, error)
	ListDeployments(ctx context.Context, filter DeploymentFilter) ([]DeploymentDocument, error)
	AppendEvent(ctx context.Context, event *EventDocument) error
	ListEvents(ctx context.Context, deploymentID string) ([]EventDocument, error)
}

// DeploymentFilter defines query criteria for listing deployments.
type DeploymentFilter struct {
	ApplicationID string
	Environment   string
	Status        domain.DeploymentState
	Actor         string
	Since         *time.Time
	Limit         int
}

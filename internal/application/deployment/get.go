package deploymentapp

import (
	"context"
	"fmt"
	"time"

	"github.com/myorg/platform-orchestrator/internal/domain/deployment"
)

// GetDeploymentQuery requests a deployment by ID.
type GetDeploymentQuery struct {
	DeploymentID string
}

// DeploymentDTO is the read-model output.
type DeploymentDTO struct {
	ID              string        `json:"id"`
	ApplicationID   string        `json:"applicationId"`
	Environment     string        `json:"environment"`
	Cluster         string        `json:"cluster"`
	Namespace       string        `json:"namespace"`
	Status          string        `json:"status"`
	ImageRepository string        `json:"imageRepository,omitempty"`
	ImageDigest     string        `json:"imageDigest,omitempty"`
	ChartVersion    string        `json:"chartVersion,omitempty"`
	ArgoAppName     string        `json:"argoAppName,omitempty"`
	ArgoSyncStatus  string        `json:"argoSyncStatus,omitempty"`
	ArgoHealthStatus string       `json:"argoHealthStatus,omitempty"`
	Error           string        `json:"error,omitempty"`
	StartedAt       time.Time     `json:"startedAt"`
	CompletedAt     *time.Time    `json:"completedAt,omitempty"`
	DurationMs      int64         `json:"durationMs,omitempty"`
}

// GetDeploymentHandler handles GetDeploymentQuery.
type GetDeploymentHandler struct {
	repo deployment.Repository
}

// NewGetDeploymentHandler creates a handler.
func NewGetDeploymentHandler(repo deployment.Repository) *GetDeploymentHandler {
	return &GetDeploymentHandler{repo: repo}
}

// Handle retrieves a deployment and maps to DTO.
func (h *GetDeploymentHandler) Handle(ctx context.Context, q GetDeploymentQuery) (DeploymentDTO, error) {
	id := deployment.DeploymentID(q.DeploymentID)

	d, err := h.repo.FindByID(ctx, id)
	if err != nil {
		return DeploymentDTO{}, fmt.Errorf("get deployment: %w", err)
	}

	return toDTO(d), nil
}

func toDTO(d *deployment.Deployment) DeploymentDTO {
	dto := DeploymentDTO{
		ID:            d.ID().String(),
		ApplicationID: d.ApplicationID(),
		Environment:   d.Target().Environment(),
		Cluster:       d.Target().Cluster(),
		Namespace:     d.Target().Namespace(),
		Status:        d.Status().String(),
		ImageRepository: d.Image().Repository(),
		ImageDigest:     d.Image().Digest(),
		Error:         d.Error(),
		StartedAt:     d.StartedAt(),
		DurationMs:    d.DurationMs(),
	}

	if meta := d.Metadata(); meta != nil {
		dto.ChartVersion = meta.DeploymentVersion.String()
		dto.ArgoAppName = meta.ArgoAppName.String()
	}

	if h := d.HealthInfo(); h != nil {
		dto.ArgoSyncStatus = h.SyncStatus
		dto.ArgoHealthStatus = h.HealthStatus
	}

	if !d.CompletedAt().IsZero() {
		t := d.CompletedAt()
		dto.CompletedAt = &t
	}

	return dto
}

package deploymentapp

import (
	"context"

	"github.com/myorg/platform-orchestrator/internal/domain/deployment"
)

// Application aggregates all deployment use case handlers.
type Application struct {
	Commands Commands
	Queries  Queries
	// Executor drives a created deployment through the resolve→deploy pipeline.
	// Optional: nil disables the post-create trigger (e.g. in handler tests).
	Executor DeploymentExecutor
}

// DeploymentExecutor drives a persisted deployment forward. The Create handler
// fires it asynchronously after persisting RECEIVED; the :deploy route re-drives
// a deployment that stalled mid-flight.
type DeploymentExecutor interface {
	Execute(ctx context.Context, id deployment.DeploymentID) error
}

// Commands groups write use-case handlers.
type Commands struct {
	CreateDeployment *CreateDeploymentHandler
}

// Queries groups read use-case handlers.
type Queries struct {
	GetDeployment *GetDeploymentHandler
}

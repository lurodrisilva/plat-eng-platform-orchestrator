package deployment

import "context"

// Repository defines persistence operations for the Deployment aggregate.
// Defined in the domain layer — implemented by outbound adapters.
type Repository interface {
	Save(ctx context.Context, d *Deployment) error
	FindByID(ctx context.Context, id DeploymentID) (*Deployment, error)
	FindByApplication(ctx context.Context, appID, environment string, limit int) ([]*Deployment, error)
}

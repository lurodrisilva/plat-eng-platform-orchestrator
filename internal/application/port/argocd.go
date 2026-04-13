package port

import "context"

// ArgoAppSpec describes the desired Argo CD Application state.
type ArgoAppSpec struct {
	Name        string
	Namespace   string
	Project     string
	RepoURL     string
	Chart       string
	Version     string
	DestServer  string
	DestNS      string
	Labels      map[string]string
	Annotations map[string]string
}

// ArgoAppStatus holds the observed Argo CD Application state.
type ArgoAppStatus struct {
	Name         string
	SyncStatus   string
	HealthStatus string
	Message      string
}

// ArgoCD manages Argo CD Applications.
type ArgoCD interface {
	ValidateProject(ctx context.Context, project, sourceRepo, destServer, destNS string) error
	CreateOrUpdate(ctx context.Context, spec ArgoAppSpec) error
	Sync(ctx context.Context, appName string) error
	Status(ctx context.Context, appName string) (ArgoAppStatus, error)
	Delete(ctx context.Context, appName string) error
}

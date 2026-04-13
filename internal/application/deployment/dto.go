package deploymentapp

// Application aggregates all deployment use case handlers.
type Application struct {
	Commands Commands
	Queries  Queries
}

// Commands groups write use-case handlers.
type Commands struct {
	CreateDeployment *CreateDeploymentHandler
}

// Queries groups read use-case handlers.
type Queries struct {
	GetDeployment *GetDeploymentHandler
}

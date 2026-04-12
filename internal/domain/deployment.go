package domain

import "time"

type ApplicationInfo struct {
	ID   string `json:"id"   validate:"required"`
	Team string `json:"team" validate:"required"`
}

type ImageInfo struct {
	Repository string `json:"repository" validate:"required"`
	Tag        string `json:"tag"        validate:"required"`
	Digest     string `json:"digest"     validate:"required,startswith=sha256:"`
}

type ChartSourceInfo struct {
	Repository        string `json:"repository"        validate:"required"`
	Name              string `json:"name"              validate:"required"`
	VersionConstraint string `json:"versionConstraint"`
	AllowPrerelease   bool   `json:"allowPrerelease"`
}

type TargetInfo struct {
	Environment string `json:"environment" validate:"required,oneof=development staging production"`
	Cluster     string `json:"cluster"     validate:"required"`
	Namespace   string `json:"namespace"   validate:"required"`
	AppProject  string `json:"appProject"  validate:"required"`
}

type SourceInfo struct {
	GitSHA             string `json:"gitSha"              validate:"required,len=40"`
	GitRef             string `json:"gitRef"              validate:"required"`
	GitHubRunID        string `json:"githubRunId"         validate:"required"`
	GitHubRunAttempt   int    `json:"githubRunAttempt"    validate:"required,gte=1"`
	WorkflowName       string `json:"workflowName"        validate:"required"`
	Actor              string `json:"actor"               validate:"required"`
	RepositoryFullName string `json:"repositoryFullName"  validate:"required"`
}

type DeploymentRequest struct {
	Application   ApplicationInfo `json:"application"   validate:"required"`
	Image         ImageInfo       `json:"image"         validate:"required"`
	Chart         ChartSourceInfo `json:"chart"         validate:"required"`
	Target        TargetInfo      `json:"target"        validate:"required"`
	Values        map[string]any  `json:"values"`
	Source        SourceInfo      `json:"source"        validate:"required"`
	CorrelationID string          `json:"correlationId"`
}

type DeploymentMetadata struct {
	DeploymentID      string    `json:"deploymentId"`
	ComponentID       string    `json:"componentId"`
	DeploymentVersion string    `json:"deploymentVersion"`
	ArgoAppName       string    `json:"argoAppName"`
	ShortSHA          string    `json:"shortSha"`
	Timestamp         time.Time `json:"timestamp"`
	Labels            map[string]string
}

type ResolvedChart struct {
	SourceRepository string `json:"sourceRepository"`
	ChartName        string `json:"chartName"`
	ResolvedVersion  string `json:"resolvedVersion"`
	ResolvedTag      string `json:"resolvedTag"`
	ArchivePath      string `json:"archivePath"`
	ArchiveBytes     []byte `json:"archiveBytes"`
}

type ComposedChart struct {
	ChartName         string `json:"chartName"`
	DeploymentVersion string `json:"deploymentVersion"`
	PackagePath       string `json:"packagePath"`
	PackageBytes      []byte `json:"packageBytes"`
}

type PublishedArtifact struct {
	OCIReference string `json:"ociReference"`
	Digest       string `json:"digest"`
	Registry     string `json:"registry"`
	Repository   string `json:"repository"`
	Tag          string `json:"tag"`
}

type ArgoApplication struct {
	Name         string `json:"name"`
	Namespace    string `json:"namespace"`
	Project      string `json:"project"`
	SyncStatus   string `json:"syncStatus"`
	HealthStatus string `json:"healthStatus"`
}

type DeploymentHealth struct {
	SyncStatus   string    `json:"syncStatus"`
	HealthStatus string    `json:"healthStatus"`
	Message      string    `json:"message"`
	EvaluatedAt  time.Time `json:"evaluatedAt"`
}

type DeploymentResult struct {
	DeploymentID string          `json:"deploymentId"`
	Status       DeploymentState `json:"status"`
	ArgoAppName  string          `json:"argoAppName,omitempty"`
	OCIReference string          `json:"ociReference,omitempty"`
	ChartVersion string          `json:"chartVersion,omitempty"`
	Error        string          `json:"error,omitempty"`
	StartedAt    time.Time       `json:"startedAt"`
	CompletedAt  time.Time       `json:"completedAt,omitempty"`
	DurationMs   int64           `json:"durationMs,omitempty"`
}

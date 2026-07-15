package deployment

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// DeploymentID uniquely identifies a deployment.
type DeploymentID string

// NewDeploymentID creates a deterministic deployment ID from its components.
func NewDeploymentID(appID, environment, shortSHA string, ts time.Time) DeploymentID {
	return DeploymentID(fmt.Sprintf("dep-%s-%s-%s-%s",
		ts.UTC().Format("20060102"), appID, environment[:4], shortSHA))
}

func (id DeploymentID) String() string { return string(id) }

// IsZero reports whether the ID is empty.
func (id DeploymentID) IsZero() bool { return id == "" }

// ComponentID identifies a deployment component within the platform.
type ComponentID string

// NewComponentID creates a component ID.
func NewComponentID(appID, environment, cluster, shortSHA string) ComponentID {
	return ComponentID(fmt.Sprintf("%s-%s-%s-%s", appID, environment, cluster, shortSHA))
}

func (id ComponentID) String() string { return string(id) }

// DeploymentVersion represents an immutable, collision-free version string.
type DeploymentVersion string

// NewDeploymentVersion creates a deployment-specific version.
func NewDeploymentVersion(chartVersion, shortSHA string, ts time.Time) DeploymentVersion {
	return DeploymentVersion(fmt.Sprintf("%s-%s-%s",
		chartVersion, ts.UTC().Format("20060102150405"), shortSHA))
}

func (v DeploymentVersion) String() string { return string(v) }

// ArgoAppName represents the Argo CD Application name.
type ArgoAppName string

// NewArgoAppName creates a deterministic Argo app name.
func NewArgoAppName(appID, environment, namespace string) ArgoAppName {
	return ArgoAppName(fmt.Sprintf("%s-%s-%s", appID, environment, namespace))
}

func (n ArgoAppName) String() string { return string(n) }

// Image represents a container image with its digest for immutable reference.
type Image struct {
	repository string
	tag        string
	digest     string
}

// NewImage creates a validated Image.
func NewImage(repository, tag, digest string) (Image, error) {
	if repository == "" {
		return Image{}, errors.New("image repository is required")
	}
	if digest == "" {
		return Image{}, errors.New("image digest is required")
	}
	if !strings.HasPrefix(digest, "sha256:") {
		return Image{}, errors.New("image digest must start with a sha256: prefix")
	}
	return Image{repository: repository, tag: tag, digest: digest}, nil
}

func (i Image) Repository() string { return i.repository }
func (i Image) Tag() string        { return i.tag }
func (i Image) Digest() string     { return i.digest }

// Target represents the deployment destination.
type Target struct {
	environment string
	cluster     string
	namespace   string
	appProject  string
}

// NewTarget creates a validated deployment target.
func NewTarget(environment, cluster, namespace, appProject string) (Target, error) {
	if environment == "" {
		return Target{}, errors.New("environment is required")
	}
	if cluster == "" {
		return Target{}, errors.New("cluster is required")
	}
	if namespace == "" {
		return Target{}, errors.New("namespace is required")
	}
	if appProject == "" {
		return Target{}, errors.New("app project is required")
	}
	return Target{
		environment: environment,
		cluster:     cluster,
		namespace:   namespace,
		appProject:  appProject,
	}, nil
}

func (t Target) Environment() string { return t.environment }
func (t Target) Cluster() string     { return t.cluster }
func (t Target) Namespace() string   { return t.namespace }
func (t Target) AppProject() string  { return t.appProject }

// Source represents the CI source that triggered the deployment.
type Source struct {
	gitSHA           string
	gitRef           string
	githubRunID      string
	githubRunAttempt int
	workflowName     string
	actor            string
	repositoryFull   string
}

// NewSource creates a validated Source.
func NewSource(gitSHA, gitRef, githubRunID string, runAttempt int, workflow, actor, repo string) (Source, error) {
	if len(gitSHA) < 7 {
		return Source{}, errors.New("git SHA must be at least 7 characters")
	}
	if githubRunID == "" {
		return Source{}, errors.New("github run ID is required")
	}
	if runAttempt < 1 {
		return Source{}, errors.New("run attempt must be >= 1")
	}
	return Source{
		gitSHA:           gitSHA,
		gitRef:           gitRef,
		githubRunID:      githubRunID,
		githubRunAttempt: runAttempt,
		workflowName:     workflow,
		actor:            actor,
		repositoryFull:   repo,
	}, nil
}

func (s Source) GitSHA() string        { return s.gitSHA }
func (s Source) ShortSHA() string      { return s.gitSHA[:7] }
func (s Source) GitRef() string        { return s.gitRef }
func (s Source) GitHubRunID() string   { return s.githubRunID }
func (s Source) GitHubRunAttempt() int { return s.githubRunAttempt }
func (s Source) WorkflowName() string  { return s.workflowName }
func (s Source) Actor() string         { return s.actor }
func (s Source) RepositoryFull() string { return s.repositoryFull }

// ChartSource describes where to find the Helm chart.
type ChartSource struct {
	repository      string
	name            string
	versionConstraint string
	allowPrerelease bool
}

// NewChartSource creates a validated ChartSource.
func NewChartSource(repository, name, constraint string, allowPrerelease bool) (ChartSource, error) {
	if repository == "" {
		return ChartSource{}, errors.New("chart repository is required")
	}
	if name == "" {
		return ChartSource{}, errors.New("chart name is required")
	}
	return ChartSource{
		repository:        repository,
		name:              name,
		versionConstraint: constraint,
		allowPrerelease:   allowPrerelease,
	}, nil
}

func (c ChartSource) Repository() string      { return c.repository }
func (c ChartSource) Name() string             { return c.name }
func (c ChartSource) VersionConstraint() string { return c.versionConstraint }
func (c ChartSource) AllowPrerelease() bool     { return c.allowPrerelease }

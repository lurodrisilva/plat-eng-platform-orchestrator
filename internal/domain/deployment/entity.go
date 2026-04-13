package deployment

import (
	"fmt"
	"time"
)

// Deployment is the aggregate root for the deployment bounded context.
type Deployment struct {
	id            DeploymentID
	applicationID string
	team          string
	image         Image
	chartSource   ChartSource
	target        Target
	source        Source
	values        map[string]any
	correlationID string

	status    Status
	metadata  *Metadata
	artifact  *Artifact
	argoApp   *ArgoApp
	health    *Health
	errMsg    string

	startedAt   time.Time
	completedAt time.Time
	updatedAt   time.Time
}

// Metadata holds generated deployment metadata.
type Metadata struct {
	ComponentID       ComponentID
	DeploymentVersion DeploymentVersion
	ArgoAppName       ArgoAppName
	Labels            map[string]string
}

// Artifact holds the published OCI artifact details.
type Artifact struct {
	OCIReference string
	Digest       string
	Registry     string
	Repository   string
	Tag          string
}

// ArgoApp holds the created Argo CD application details.
type ArgoApp struct {
	Name      string
	Namespace string
	Project   string
}

// Health holds the deployment health evaluation result.
type Health struct {
	SyncStatus   string
	HealthStatus string
	Message      string
	EvaluatedAt  time.Time
}

// New creates a new Deployment aggregate. Validates all inputs.
func New(
	applicationID, team string,
	image Image,
	chart ChartSource,
	target Target,
	source Source,
	values map[string]any,
	correlationID string,
) (*Deployment, error) {
	if applicationID == "" {
		return nil, fmt.Errorf("new deployment: application ID is required")
	}
	if team == "" {
		return nil, fmt.Errorf("new deployment: team is required")
	}

	now := time.Now().UTC()
	shortSHA := source.ShortSHA()

	d := &Deployment{
		id:            NewDeploymentID(applicationID, target.Environment(), shortSHA, now),
		applicationID: applicationID,
		team:          team,
		image:         image,
		chartSource:   chart,
		target:        target,
		source:        source,
		values:        copyValues(values),
		correlationID: correlationID,
		status:        StatusReceived,
		startedAt:     now,
		updatedAt:     now,
	}

	return d, nil
}

// Reconstitute rebuilds a Deployment from persistence. No validation, no events.
func Reconstitute(
	id DeploymentID,
	applicationID, team string,
	image Image,
	chart ChartSource,
	target Target,
	source Source,
	values map[string]any,
	correlationID string,
	status Status,
	metadata *Metadata,
	artifact *Artifact,
	argoApp *ArgoApp,
	health *Health,
	errMsg string,
	startedAt, completedAt, updatedAt time.Time,
) *Deployment {
	return &Deployment{
		id:            id,
		applicationID: applicationID,
		team:          team,
		image:         image,
		chartSource:   chart,
		target:        target,
		source:        source,
		values:        values,
		correlationID: correlationID,
		status:        status,
		metadata:      metadata,
		artifact:      artifact,
		argoApp:       argoApp,
		health:        health,
		errMsg:        errMsg,
		startedAt:     startedAt,
		completedAt:   completedAt,
		updatedAt:     updatedAt,
	}
}

// --- Getters ---

func (d *Deployment) ID() DeploymentID        { return d.id }
func (d *Deployment) ApplicationID() string    { return d.applicationID }
func (d *Deployment) Team() string             { return d.team }
func (d *Deployment) Image() Image             { return d.image }
func (d *Deployment) ChartSource() ChartSource { return d.chartSource }
func (d *Deployment) Target() Target           { return d.target }
func (d *Deployment) Source() Source            { return d.source }
func (d *Deployment) Values() map[string]any   { return copyValues(d.values) }
func (d *Deployment) CorrelationID() string    { return d.correlationID }
func (d *Deployment) Status() Status           { return d.status }
func (d *Deployment) Metadata() *Metadata      { return d.metadata }
func (d *Deployment) ArtifactInfo() *Artifact   { return d.artifact }
func (d *Deployment) ArgoApp() *ArgoApp        { return d.argoApp }
func (d *Deployment) HealthInfo() *Health       { return d.health }
func (d *Deployment) Error() string            { return d.errMsg }
func (d *Deployment) StartedAt() time.Time     { return d.startedAt }
func (d *Deployment) CompletedAt() time.Time   { return d.completedAt }
func (d *Deployment) UpdatedAt() time.Time     { return d.updatedAt }
func (d *Deployment) DurationMs() int64 {
	if d.completedAt.IsZero() {
		return 0
	}
	return d.completedAt.Sub(d.startedAt).Milliseconds()
}

// --- State Transitions (enforce invariants) ---

// TransitionTo moves the deployment to a new status if the transition is allowed.
func (d *Deployment) TransitionTo(next Status) error {
	if !d.status.CanTransitionTo(next) {
		return fmt.Errorf("transition %s to %s: %w", d.status, next, ErrInvalidTransition)
	}
	d.status = next
	d.updatedAt = time.Now().UTC()
	return nil
}

// SetMetadata records the generated deployment metadata.
func (d *Deployment) SetMetadata(m Metadata) {
	d.metadata = &m
	d.id = NewDeploymentID(d.applicationID, d.target.Environment(), d.source.ShortSHA(), d.startedAt)
	d.updatedAt = time.Now().UTC()
}

// SetArtifact records the published OCI artifact.
func (d *Deployment) SetArtifact(a Artifact) {
	d.artifact = &a
	d.updatedAt = time.Now().UTC()
}

// SetArgoApp records the created Argo CD application.
func (d *Deployment) SetArgoApp(a ArgoApp) {
	d.argoApp = &a
	d.updatedAt = time.Now().UTC()
}

// SetHealth records the final health evaluation.
func (d *Deployment) SetHealth(h Health) {
	d.health = &h
	d.updatedAt = time.Now().UTC()
}

// Fail marks the deployment as failed with an error message.
func (d *Deployment) Fail(msg string) error {
	if err := d.TransitionTo(StatusFailed); err != nil {
		return err
	}
	d.errMsg = msg
	d.completedAt = time.Now().UTC()
	return nil
}

// Complete marks the deployment as successfully completed.
func (d *Deployment) Complete() error {
	if err := d.TransitionTo(StatusCompleted); err != nil {
		return err
	}
	d.completedAt = time.Now().UTC()
	return nil
}

// Reject marks the deployment as rejected by policy.
func (d *Deployment) Reject(reason string) error {
	if err := d.TransitionTo(StatusRejected); err != nil {
		return err
	}
	d.errMsg = reason
	d.completedAt = time.Now().UTC()
	return nil
}

// copyValues deep-copies a values map to prevent aliasing.
func copyValues(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		if sub, ok := v.(map[string]any); ok {
			dst[k] = copyValues(sub)
		} else {
			dst[k] = v
		}
	}
	return dst
}

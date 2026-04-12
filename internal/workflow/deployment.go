package workflow

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/myorg/platform-orchestrator/internal/activity"
	"github.com/myorg/platform-orchestrator/internal/domain"
	"github.com/myorg/platform-orchestrator/internal/state"
)

const (
	TaskQueue = "deployment-workers"

	QueryCurrentState      = "getCurrentState"
	QueryDeploymentDetails = "getDeploymentDetails"
)

// DeploymentWorkflowInput is the input to the DeploymentWorkflow.
type DeploymentWorkflowInput struct {
	Request         domain.DeploymentRequest
	TemporalRunID   string
	TaskQueue       string
}

// stateTracker keeps track of the current workflow state for queries.
type stateTracker struct {
	currentState domain.DeploymentState
	deploymentID string
	argoAppName  string
	ociReference string
	chartVersion string
	startedAt    time.Time
	error        string
}

// DeploymentWorkflow orchestrates the full deployment lifecycle.
func DeploymentWorkflow(ctx workflow.Context, input DeploymentWorkflowInput) (domain.DeploymentResult, error) {
	req := input.Request
	startedAt := workflow.Now(ctx)

	tracker := &stateTracker{
		currentState: domain.StateReceived,
		startedAt:    startedAt,
	}

	// Register queries
	if err := workflow.SetQueryHandler(ctx, QueryCurrentState, func() (domain.DeploymentState, error) {
		return tracker.currentState, nil
	}); err != nil {
		return domain.DeploymentResult{}, fmt.Errorf("registering query handler: %w", err)
	}

	if err := workflow.SetQueryHandler(ctx, QueryDeploymentDetails, func() (*stateTracker, error) {
		return tracker, nil
	}); err != nil {
		return domain.DeploymentResult{}, fmt.Errorf("registering details query: %w", err)
	}

	// Set search attributes
	_ = workflow.UpsertSearchAttributes(ctx, map[string]any{
		"Application": req.Application.ID,
		"Environment": req.Target.Environment,
		"Cluster":     req.Target.Cluster,
		"GitSha":      req.Source.GitSHA,
		"GitRef":      req.Source.GitRef,
		"GithubRunId": req.Source.GitHubRunID,
		"Actor":       req.Source.Actor,
		"Team":        req.Application.Team,
		"DeploymentStatus": string(domain.StateReceived),
	})

	var a *activity.Activities
	var compensations []func(workflow.Context) error

	// Helper to persist state transitions
	transition := func(ctx workflow.Context, newState domain.DeploymentState) {
		tracker.currentState = newState
		_ = workflow.UpsertSearchAttributes(ctx, map[string]any{
			"DeploymentStatus": string(newState),
		})
	}

	buildResult := func(errMsg string) domain.DeploymentResult {
		completedAt := workflow.Now(ctx)
		return domain.DeploymentResult{
			DeploymentID: tracker.deploymentID,
			Status:       tracker.currentState,
			ArgoAppName:  tracker.argoAppName,
			OCIReference: tracker.ociReference,
			ChartVersion: tracker.chartVersion,
			Error:        errMsg,
			StartedAt:    startedAt,
			CompletedAt:  completedAt,
			DurationMs:   completedAt.Sub(startedAt).Milliseconds(),
		}
	}

	// Persist failure state and return
	failWith := func(ctx workflow.Context, newState domain.DeploymentState, err error) (domain.DeploymentResult, error) {
		transition(ctx, newState)
		tracker.error = err.Error()
		persistFinalState(ctx, tracker, req, input)
		return buildResult(err.Error()), err
	}

	// --- Step 1: Validate ---
	transition(ctx, domain.StateValidating)
	validateCtx := withActivityOptions(ctx, 30*time.Second, shortRetry())
	if err := workflow.ExecuteActivity(validateCtx, a.ValidateDeployment, req).Get(ctx, nil); err != nil {
		return failWith(ctx, domain.StateRejected, err)
	}

	// --- Step 2: Resolve chart (needed for metadata) ---
	transition(ctx, domain.StateChartResolved)
	resolveCtx := withActivityOptions(ctx, 60*time.Second, externalRetry())
	var resolved domain.ResolvedChart
	if err := workflow.ExecuteActivity(resolveCtx, a.ResolveChart, req.Chart).Get(ctx, &resolved); err != nil {
		return failWith(ctx, domain.StateFailed, err)
	}

	// --- Step 3: Generate metadata ---
	transition(ctx, domain.StateMetadataGenerated)
	metaCtx := withActivityOptions(ctx, 5*time.Second, noRetry())
	var meta domain.DeploymentMetadata
	if err := workflow.ExecuteActivity(metaCtx, a.GenerateMetadata, req, resolved.ResolvedVersion).Get(ctx, &meta); err != nil {
		return failWith(ctx, domain.StateFailed, err)
	}

	tracker.deploymentID = meta.DeploymentID
	tracker.chartVersion = meta.DeploymentVersion
	_ = workflow.UpsertSearchAttributes(ctx, map[string]any{
		"DeploymentId": meta.DeploymentID,
		"ChartVersion": meta.DeploymentVersion,
	})

	// --- Step 4: Compose chart ---
	transition(ctx, domain.StateChartComposed)
	composeCtx := withActivityOptions(ctx, 30*time.Second, noRetry())
	var composed domain.ComposedChart
	if err := workflow.ExecuteActivity(composeCtx, a.ComposeChart, resolved, req, meta).Get(ctx, &composed); err != nil {
		return failWith(ctx, domain.StateFailed, err)
	}

	// --- Step 5: Publish artifact ---
	transition(ctx, domain.StateArtifactPublished)
	publishCtx := withActivityOptions(ctx, 120*time.Second, externalRetry())
	var artifact domain.PublishedArtifact
	if err := workflow.ExecuteActivity(publishCtx, a.PublishArtifact, composed, req.Application.ID).Get(ctx, &artifact); err != nil {
		return failWith(ctx, domain.StateFailed, err)
	}
	tracker.ociReference = artifact.OCIReference

	// Artifact published — register compensation
	compensations = append(compensations, func(ctx workflow.Context) error {
		// OCI artifacts are immutable; compensation is optional but we log it
		return nil
	})

	// --- Step 6: Create Argo CD Application ---
	transition(ctx, domain.StateArgoAppCreated)
	argoCtx := withActivityOptions(ctx, 60*time.Second, shortRetry())
	var argoApp domain.ArgoApplication
	if err := workflow.ExecuteActivity(argoCtx, a.CreateArgoApp, req, artifact, meta).Get(ctx, &argoApp); err != nil {
		runCompensations(ctx, compensations)
		return failWith(ctx, domain.StateFailed, err)
	}
	tracker.argoAppName = argoApp.Name

	// Argo app created — register compensation
	compensations = append(compensations, func(ctx workflow.Context) error {
		compCtx := withActivityOptions(ctx, 60*time.Second, externalRetry())
		return workflow.ExecuteActivity(compCtx, a.CompensateArgoApp, argoApp.Name).Get(ctx, nil)
	})

	// --- Step 7: Watch deployment health ---
	transition(ctx, domain.StateSyncing)
	watchCtx := withActivityOptions(ctx, 10*time.Minute, noRetry())
	var health domain.DeploymentHealth
	if err := workflow.ExecuteActivity(watchCtx, a.WatchDeployment, argoApp).Get(ctx, &health); err != nil {
		// Do NOT auto-rollback — mark for manual intervention
		transition(ctx, domain.StateDegraded)
		tracker.error = err.Error()
		persistFinalState(ctx, tracker, req, input)
		return buildResult(err.Error()), err
	}

	// --- Step 8: Success ---
	transition(ctx, domain.StateHealthy)

	// --- Step 9: Persist final state ---
	transition(ctx, domain.StateCompleted)
	persistFinalState(ctx, tracker, req, input)

	return buildResult(""), nil
}

func persistFinalState(ctx workflow.Context, tracker *stateTracker, req domain.DeploymentRequest, input DeploymentWorkflowInput) {
	var a *activity.Activities
	persistCtx := withActivityOptions(ctx, 30*time.Second, persistRetry())

	now := workflow.Now(ctx)
	doc := &state.DeploymentDocument{
		ID:            tracker.deploymentID,
		ApplicationID: req.Application.ID,
		Environment:   req.Target.Environment,
		Cluster:       req.Target.Cluster,
		Namespace:     req.Target.Namespace,
		Team:          req.Application.Team,
		Status:        tracker.currentState,
		Image: state.ImageDoc{
			Repository: req.Image.Repository,
			Tag:        req.Image.Tag,
			Digest:     req.Image.Digest,
		},
		Chart: state.ChartDoc{
			SourceRepository:  req.Chart.Repository,
			SourceName:        req.Chart.Name,
			DeploymentVersion: tracker.chartVersion,
			OCIReference:      tracker.ociReference,
		},
		Source: state.SourceDoc{
			GitSHA:           req.Source.GitSHA,
			GitRef:           req.Source.GitRef,
			GitHubRunID:      req.Source.GitHubRunID,
			GitHubRunAttempt: req.Source.GitHubRunAttempt,
			WorkflowName:     req.Source.WorkflowName,
			Actor:            req.Source.Actor,
			RepositoryFull:   req.Source.RepositoryFullName,
		},
		ArgoCD: state.ArgoCDDoc{
			ApplicationName: tracker.argoAppName,
		},
		Temporal: state.TemporalDoc{
			WorkflowID: workflow.GetInfo(ctx).WorkflowExecution.ID,
			RunID:      input.TemporalRunID,
			TaskQueue:  input.TaskQueue,
		},
		CorrelationID: req.CorrelationID,
		Error:         tracker.error,
		StartedAt:     tracker.startedAt,
		CompletedAt:   &now,
		DurationMs:    now.Sub(tracker.startedAt).Milliseconds(),
		CreatedAt:     tracker.startedAt,
		UpdatedAt:     now,
	}

	// Best-effort persist — if this fails, Temporal has the workflow state as backup
	_ = workflow.ExecuteActivity(persistCtx, a.PersistState, doc).Get(ctx, nil)
}

func runCompensations(ctx workflow.Context, compensations []func(workflow.Context) error) {
	// Run compensations in reverse order
	for i := len(compensations) - 1; i >= 0; i-- {
		_ = compensations[i](ctx)
	}
}

func withActivityOptions(ctx workflow.Context, timeout time.Duration, retryPolicy *temporal.RetryPolicy) workflow.Context {
	return workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: timeout,
		RetryPolicy:         retryPolicy,
	})
}

func noRetry() *temporal.RetryPolicy {
	return &temporal.RetryPolicy{
		MaximumAttempts: 1,
	}
}

func shortRetry() *temporal.RetryPolicy {
	return &temporal.RetryPolicy{
		InitialInterval:        5 * time.Second,
		BackoffCoefficient:     2.0,
		MaximumInterval:        30 * time.Second,
		MaximumAttempts:        2,
		NonRetryableErrorTypes: domain.NonRetryableErrorTypes(),
	}
}

func externalRetry() *temporal.RetryPolicy {
	return &temporal.RetryPolicy{
		InitialInterval:        5 * time.Second,
		BackoffCoefficient:     2.0,
		MaximumInterval:        60 * time.Second,
		MaximumAttempts:        3,
		NonRetryableErrorTypes: domain.NonRetryableErrorTypes(),
	}
}

func persistRetry() *temporal.RetryPolicy {
	return &temporal.RetryPolicy{
		InitialInterval:    2 * time.Second,
		BackoffCoefficient: 2.0,
		MaximumInterval:    30 * time.Second,
		MaximumAttempts:    5,
	}
}

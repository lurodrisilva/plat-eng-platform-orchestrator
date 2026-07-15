package deploymentapp

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/myorg/platform-orchestrator/internal/application/port"
	"github.com/myorg/platform-orchestrator/internal/domain/deployment"
)

// CreateDeploymentCommand represents the intent to start a deployment.
type CreateDeploymentCommand struct {
	ApplicationID   string
	Team            string
	ImageRepository string
	ImageTag        string
	ImageDigest     string
	ChartRepository string
	ChartName       string
	ChartConstraint string
	AllowPrerelease bool
	Environment     string
	Cluster         string
	Namespace       string
	AppProject      string
	Values          map[string]any
	GitSHA          string
	GitRef          string
	GitHubRunID     string
	GitHubRunAttempt int
	WorkflowName    string
	Actor           string
	RepositoryFull  string
	CorrelationID   string
}

// CreateDeploymentResult is the output of a successful deployment creation.
type CreateDeploymentResult struct {
	DeploymentID string
	Status       string
}

// CreateDeploymentHandler handles the CreateDeploymentCommand use case.
type CreateDeploymentHandler struct {
	repo      deployment.Repository
	policy    port.PolicyEvaluator
	allowlist port.TunableAllowlist
	logger    *slog.Logger
}

// NewCreateDeploymentHandler creates a handler with its dependencies. policy
// and allowlist are optional (nil = that gate is skipped); logger is optional
// (nil falls back to the slog default).
func NewCreateDeploymentHandler(repo deployment.Repository, policy port.PolicyEvaluator, allowlist port.TunableAllowlist, logger *slog.Logger) *CreateDeploymentHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &CreateDeploymentHandler{repo: repo, policy: policy, allowlist: allowlist, logger: logger}
}

// Handle validates and creates a new deployment aggregate.
func (h *CreateDeploymentHandler) Handle(ctx context.Context, cmd CreateDeploymentCommand) (CreateDeploymentResult, error) {
	// Branch/environment policy (optional; nil evaluator = no gate).
	if h.policy != nil {
		decision, err := h.policy.Evaluate(ctx, cmd.RepositoryFull, cmd.GitRef, cmd.Environment)
		if err != nil {
			return CreateDeploymentResult{}, fmt.Errorf("create deployment: evaluate policy: %w", err)
		}
		if !decision.Allowed {
			return CreateDeploymentResult{}, fmt.Errorf("create deployment: %w: %s", deployment.ErrPolicyViolation, decision.Reason)
		}
	}

	// J3 tunable allowlist: reject overrides of platform-locked knobs at the
	// API boundary (enforce mode); in audit mode, observe but don't block.
	if h.allowlist != nil {
		dec := h.allowlist.Validate(ctx, cmd.Values, cmd.Environment)
		if dec.Reject {
			return CreateDeploymentResult{}, &LockedKnobError{Keys: dec.Violations}
		}
		if len(dec.Violations) > 0 {
			h.logger.WarnContext(ctx, "tunable allowlist would-deny (audit)",
				slog.String("mode", dec.Mode),
				slog.String("environment", cmd.Environment),
				slog.Any("lockedKeys", dec.Violations),
			)
		}
	}

	// Build domain value objects
	image, err := deployment.NewImage(cmd.ImageRepository, cmd.ImageTag, cmd.ImageDigest)
	if err != nil {
		return CreateDeploymentResult{}, fmt.Errorf("create deployment: %w", err)
	}

	chart, err := deployment.NewChartSource(cmd.ChartRepository, cmd.ChartName, cmd.ChartConstraint, cmd.AllowPrerelease)
	if err != nil {
		return CreateDeploymentResult{}, fmt.Errorf("create deployment: %w", err)
	}

	target, err := deployment.NewTarget(cmd.Environment, cmd.Cluster, cmd.Namespace, cmd.AppProject)
	if err != nil {
		return CreateDeploymentResult{}, fmt.Errorf("create deployment: %w", err)
	}

	source, err := deployment.NewSource(
		cmd.GitSHA, cmd.GitRef, cmd.GitHubRunID, cmd.GitHubRunAttempt,
		cmd.WorkflowName, cmd.Actor, cmd.RepositoryFull,
	)
	if err != nil {
		return CreateDeploymentResult{}, fmt.Errorf("create deployment: %w", err)
	}

	// Create aggregate
	d, err := deployment.New(cmd.ApplicationID, cmd.Team, image, chart, target, source, cmd.Values, cmd.CorrelationID)
	if err != nil {
		return CreateDeploymentResult{}, fmt.Errorf("create deployment: %w", err)
	}

	// Persist initial state
	if err := h.repo.Save(ctx, d); err != nil {
		return CreateDeploymentResult{}, fmt.Errorf("create deployment: save: %w", err)
	}

	return CreateDeploymentResult{
		DeploymentID: d.ID().String(),
		Status:       d.Status().String(),
	}, nil
}

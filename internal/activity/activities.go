package activity

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"github.com/myorg/platform-orchestrator/internal/argocd"
	"github.com/myorg/platform-orchestrator/internal/domain"
	"github.com/myorg/platform-orchestrator/internal/helm"
	"github.com/myorg/platform-orchestrator/internal/oci"
	"github.com/myorg/platform-orchestrator/internal/policy"
	"github.com/myorg/platform-orchestrator/internal/state"
)

var tracer = otel.Tracer("platform-orchestrator/activities")

// Activities holds all Temporal activity implementations and their dependencies.
type Activities struct {
	ChartResolver   *helm.Resolver
	ChartComposer   *helm.Composer
	Publisher       *oci.Client
	ArgoClient      *argocd.Client
	HealthEvaluator *argocd.HealthEvaluator
	StateRepo       state.Repository
	PolicyEngine    *policy.Engine
	Logger          *slog.Logger
}

// ValidateDeployment runs all policy checks and AppProject validation.
func (a *Activities) ValidateDeployment(ctx context.Context, req domain.DeploymentRequest) error {
	ctx, span := tracer.Start(ctx, "ValidateDeployment")
	defer span.End()

	span.SetAttributes(
		attribute.String("application", req.Application.ID),
		attribute.String("environment", req.Target.Environment),
		attribute.String("namespace", req.Target.Namespace),
	)

	// Policy evaluation
	decision, err := a.PolicyEngine.Evaluate(ctx, req)
	if err != nil {
		return fmt.Errorf("evaluating policies: %w", err)
	}
	if !decision.Allowed {
		return domain.NewPolicyViolationError(decision.Reason)
	}

	// AppProject validation against Argo CD
	sourceRepo := fmt.Sprintf("oci://%s/%s/charts", a.Publisher.RegistryHost(), req.Application.ID)
	err = a.ArgoClient.ValidateAppProject(ctx,
		req.Target.AppProject,
		sourceRepo,
		req.Target.Cluster,
		req.Target.Namespace,
	)
	if err != nil {
		return domain.NewAppProjectMismatchError("appProject", req.Target.AppProject, nil)
	}

	a.Logger.InfoContext(ctx, "deployment validated",
		slog.String("application", req.Application.ID),
		slog.String("environment", req.Target.Environment),
	)

	return nil
}

// GenerateMetadata creates deployment IDs, versions, and labels.
func (a *Activities) GenerateMetadata(ctx context.Context, req domain.DeploymentRequest, chartVersion string) (domain.DeploymentMetadata, error) {
	ctx, span := tracer.Start(ctx, "GenerateMetadata")
	defer span.End()

	meta := domain.GenerateDeploymentMetadata(req, chartVersion, time.Now())

	span.SetAttributes(
		attribute.String("deployment_id", meta.DeploymentID),
		attribute.String("deployment_version", meta.DeploymentVersion),
		attribute.String("argo_app_name", meta.ArgoAppName),
	)

	a.Logger.InfoContext(ctx, "generated metadata",
		slog.String("deploymentId", meta.DeploymentID),
		slog.String("version", meta.DeploymentVersion),
	)

	return meta, nil
}

// ResolveChart fetches the chart from GitHub using deterministic SemVer tag selection.
func (a *Activities) ResolveChart(ctx context.Context, chart domain.ChartSourceInfo) (domain.ResolvedChart, error) {
	ctx, span := tracer.Start(ctx, "ResolveChart")
	defer span.End()

	span.SetAttributes(
		attribute.String("chart_repo", chart.Repository),
		attribute.String("chart_name", chart.Name),
		attribute.String("constraint", chart.VersionConstraint),
	)

	resolved, err := a.ChartResolver.Resolve(ctx, chart)
	if err != nil {
		span.RecordError(err)
		return domain.ResolvedChart{}, err
	}

	span.SetAttributes(
		attribute.String("resolved_version", resolved.ResolvedVersion),
		attribute.String("resolved_tag", resolved.ResolvedTag),
	)

	return resolved, nil
}

// ComposeChart loads, enriches, and packages the Helm chart.
func (a *Activities) ComposeChart(ctx context.Context, resolved domain.ResolvedChart, req domain.DeploymentRequest, meta domain.DeploymentMetadata) (domain.ComposedChart, error) {
	ctx, span := tracer.Start(ctx, "ComposeChart")
	defer span.End()

	composed, err := a.ChartComposer.Compose(ctx, resolved, req, meta)
	if err != nil {
		span.RecordError(err)
		return domain.ComposedChart{}, err
	}

	span.SetAttributes(
		attribute.String("chart_name", composed.ChartName),
		attribute.String("deployment_version", composed.DeploymentVersion),
		attribute.Int("package_bytes", len(composed.PackageBytes)),
	)

	return composed, nil
}

// PublishArtifact pushes the packaged chart to the OCI registry.
func (a *Activities) PublishArtifact(ctx context.Context, composed domain.ComposedChart, appID string) (domain.PublishedArtifact, error) {
	ctx, span := tracer.Start(ctx, "PublishArtifact")
	defer span.End()

	ref, digest, err := a.Publisher.Push(ctx, composed.PackageBytes, appID, composed.ChartName, composed.DeploymentVersion)
	if err != nil {
		span.RecordError(err)
		return domain.PublishedArtifact{}, fmt.Errorf("publishing artifact: %w", err)
	}

	// Verify the push
	if err := a.Publisher.Verify(ctx, ref, digest); err != nil {
		span.RecordError(err)
		return domain.PublishedArtifact{}, fmt.Errorf("verifying artifact: %w", err)
	}

	artifact := domain.PublishedArtifact{
		OCIReference: ref,
		Digest:       digest,
		Registry:     a.Publisher.RegistryHost(),
		Repository:   fmt.Sprintf("%s/charts/%s", appID, composed.ChartName),
		Tag:          composed.DeploymentVersion,
	}

	span.SetAttributes(
		attribute.String("oci_reference", ref),
		attribute.String("digest", digest),
	)

	return artifact, nil
}

// CreateArgoApp creates the Argo CD Application and triggers a sync.
func (a *Activities) CreateArgoApp(ctx context.Context, req domain.DeploymentRequest, artifact domain.PublishedArtifact, meta domain.DeploymentMetadata) (domain.ArgoApplication, error) {
	ctx, span := tracer.Start(ctx, "CreateArgoApp")
	defer span.End()

	app := &argocd.Application{
		Metadata: argocd.AppMetadata{
			Name:      meta.ArgoAppName,
			Namespace: "argocd",
			Labels:    meta.Labels,
			Annotations: map[string]string{
				"platform.example.com/git-sha":       req.Source.GitSHA,
				"platform.example.com/chart-version": meta.DeploymentVersion,
				"platform.example.com/deployed-by":   req.Source.Actor,
			},
		},
		Spec: argocd.AppSpec{
			Project: req.Target.AppProject,
			Source: argocd.AppSource{
				RepoURL:        fmt.Sprintf("oci://%s/%s/charts", artifact.Registry, req.Application.ID),
				Chart:          meta.ArgoAppName,
				TargetRevision: meta.DeploymentVersion,
			},
			Destination: argocd.Destination{
				Server:    req.Target.Cluster,
				Namespace: req.Target.Namespace,
			},
			SyncPolicy: &argocd.SyncPolicy{
				Automated: &argocd.AutomatedSync{
					Prune:    true,
					SelfHeal: false,
				},
				SyncOptions: []string{
					"CreateNamespace=false",
					"PruneLast=true",
					"ApplyOutOfSyncOnly=true",
				},
				Retry: &argocd.RetryPolicy{
					Limit: 2,
					Backoff: argocd.RetryBackoff{
						Duration:    "10s",
						Factor:      2,
						MaxDuration: "60s",
					},
				},
			},
		},
	}

	// Try create; if exists, update
	created, err := a.ArgoClient.CreateApplication(ctx, app)
	if err != nil {
		// Attempt update if creation fails (app may already exist)
		updated, updateErr := a.ArgoClient.UpdateApplication(ctx, app)
		if updateErr != nil {
			span.RecordError(err)
			return domain.ArgoApplication{}, fmt.Errorf("creating/updating Argo app: create=%w, update=%w", err, updateErr)
		}
		created = updated
	}

	// Trigger sync
	if err := a.ArgoClient.SyncApplication(ctx, meta.ArgoAppName); err != nil {
		span.RecordError(err)
		return domain.ArgoApplication{}, fmt.Errorf("triggering sync: %w", err)
	}

	span.SetAttributes(
		attribute.String("argo_app", created.Metadata.Name),
		attribute.String("project", created.Spec.Project),
	)

	return domain.ArgoApplication{
		Name:      created.Metadata.Name,
		Namespace: created.Metadata.Namespace,
		Project:   created.Spec.Project,
	}, nil
}

// WatchDeployment polls Argo CD until the deployment reaches a terminal health state.
func (a *Activities) WatchDeployment(ctx context.Context, argoApp domain.ArgoApplication) (domain.DeploymentHealth, error) {
	ctx, span := tracer.Start(ctx, "WatchDeployment")
	defer span.End()

	health, err := a.HealthEvaluator.WaitForHealthy(ctx, argoApp.Name)
	if err != nil {
		span.RecordError(err)
		span.SetAttributes(
			attribute.String("final_sync_status", health.SyncStatus),
			attribute.String("final_health_status", health.HealthStatus),
		)
		return health, err
	}

	span.SetAttributes(
		attribute.String("final_sync_status", health.SyncStatus),
		attribute.String("final_health_status", health.HealthStatus),
	)

	return health, nil
}

// PersistState writes the deployment state to DocumentDB.
func (a *Activities) PersistState(ctx context.Context, doc *state.DeploymentDocument) error {
	ctx, span := tracer.Start(ctx, "PersistState")
	defer span.End()

	if err := a.StateRepo.UpsertDeployment(ctx, doc); err != nil {
		span.RecordError(err)
		return fmt.Errorf("persisting state: %w", err)
	}

	span.SetAttributes(
		attribute.String("deployment_id", doc.ID),
		attribute.String("status", string(doc.Status)),
	)

	return nil
}

// PersistEvent writes a state transition event to the audit trail.
func (a *Activities) PersistEvent(ctx context.Context, event *state.EventDocument) error {
	ctx, span := tracer.Start(ctx, "PersistEvent")
	defer span.End()

	if err := a.StateRepo.AppendEvent(ctx, event); err != nil {
		span.RecordError(err)
		return fmt.Errorf("persisting event: %w", err)
	}

	return nil
}

// CompensateArgoApp deletes an Argo CD Application as part of rollback.
func (a *Activities) CompensateArgoApp(ctx context.Context, appName string) error {
	ctx, span := tracer.Start(ctx, "CompensateArgoApp")
	defer span.End()

	a.Logger.WarnContext(ctx, "compensating: deleting Argo CD application",
		slog.String("app", appName),
	)

	if err := a.ArgoClient.DeleteApplication(ctx, appName); err != nil {
		span.RecordError(err)
		return fmt.Errorf("deleting Argo app %s: %w", appName, err)
	}

	return nil
}

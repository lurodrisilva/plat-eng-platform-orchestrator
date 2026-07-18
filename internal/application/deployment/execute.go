package deploymentapp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"log/slog"

	"github.com/myorg/platform-orchestrator/internal/application/port"
	"github.com/myorg/platform-orchestrator/internal/domain/deployment"
)

// ExecutionConfig carries the timing + placement knobs the executor needs,
// mapped from infrastructure config so this layer stays free of it.
type ExecutionConfig struct {
	PollInterval       time.Duration // how often to poll ArgoCD health
	ConvergenceTimeout time.Duration // give up waiting for HEALTHY after this
	ArgoAppNamespace   string        // namespace the ArgoCD Application object lives in
	DestServer         string        // ArgoCD destination cluster (in-cluster API)
}

// DeployExecutionHandler drives a persisted deployment through the pipeline:
// resolve → compose → publish → ArgoCD CreateOrUpdate → Sync → poll to HEALTHY,
// transitioning the aggregate and persisting after each step. It replaces the
// removed Temporal worker (ADR-0016) with an in-process executor; a mid-flight
// restart is re-driven from persisted state via the :deploy route (ArgoCD is
// declarative, so the Application still converges).
type DeployExecutionHandler struct {
	repo      deployment.Repository
	resolver  port.ChartResolver
	composer  port.ChartComposer
	publisher port.ArtifactPublisher
	argocd    port.ArgoCD
	cfg       ExecutionConfig
	logger    *slog.Logger
}

// NewDeployExecutionHandler wires the executor. logger is optional.
func NewDeployExecutionHandler(
	repo deployment.Repository,
	resolver port.ChartResolver,
	composer port.ChartComposer,
	publisher port.ArtifactPublisher,
	argocd port.ArgoCD,
	cfg ExecutionConfig,
	logger *slog.Logger,
) *DeployExecutionHandler {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 10 * time.Second
	}
	if cfg.ConvergenceTimeout <= 0 {
		cfg.ConvergenceTimeout = 5 * time.Minute
	}
	if cfg.ArgoAppNamespace == "" {
		cfg.ArgoAppNamespace = "argocd"
	}
	if cfg.DestServer == "" {
		cfg.DestServer = "https://kubernetes.default.svc"
	}
	return &DeployExecutionHandler{
		repo: repo, resolver: resolver, composer: composer,
		publisher: publisher, argocd: argocd, cfg: cfg, logger: logger,
	}
}

// Execute loads a deployment and drives it forward. Terminal deployments are a
// no-op. On any step error the aggregate is marked FAILED and persisted, and
// the error is returned (callers run this in a goroutine and only log it).
func (h *DeployExecutionHandler) Execute(ctx context.Context, id deployment.DeploymentID) error {
	d, err := h.repo.FindByID(ctx, id)
	if err != nil {
		return fmt.Errorf("execute: load %s: %w", id, err)
	}
	if d.Status().IsTerminal() {
		h.logger.InfoContext(ctx, "deployment already terminal; nothing to drive",
			slog.String("id", id.String()), slog.String("status", d.Status().String()))
		return nil
	}

	if err := h.run(ctx, d); err != nil {
		h.logger.ErrorContext(ctx, "deploy pipeline failed",
			slog.String("id", id.String()),
			slog.String("status", d.Status().String()),
			slog.String("error", err.Error()))
		if failErr := d.Fail(err.Error()); failErr != nil {
			// Can't transition to FAILED from here (e.g. still RECEIVED); persist
			// the error context we have without masking the original failure.
			h.logger.WarnContext(ctx, "could not mark deployment FAILED",
				slog.String("id", id.String()), slog.String("error", failErr.Error()))
		}
		_ = h.repo.Save(ctx, d)
		return err
	}
	return nil
}

// run advances the aggregate one full forward pass. Each stage is guarded on the
// current status, so a re-drive resumes where it left off. Transient chart data
// (archive) is lazily (re-)resolved; post-publish stages read persisted state.
func (h *DeployExecutionHandler) run(ctx context.Context, d *deployment.Deployment) error {
	// RECEIVED → VALIDATING
	if d.Status() == deployment.StatusReceived {
		if err := h.step(ctx, d, deployment.StatusValidating); err != nil {
			return err
		}
	}

	// VALIDATING → METADATA_GENERATED (component id, argo app name, labels)
	if d.Status() == deployment.StatusValidating {
		d.SetMetadata(baseMetadata(d))
		if err := h.step(ctx, d, deployment.StatusMetadataGenerated); err != nil {
			return err
		}
	}

	// Lazily resolve the umbrella; needed for compose and to stamp the version.
	var archive []byte
	ensureArchive := func() error {
		if archive != nil {
			return nil
		}
		cs := d.ChartSource()
		resolved, err := h.resolver.Resolve(ctx, cs.Repository(), cs.Name(), cs.VersionConstraint(), cs.AllowPrerelease())
		if err != nil {
			return fmt.Errorf("resolve chart: %w", err)
		}
		archive = resolved.ArchiveBytes
		// Stamp the deployment version now the resolved chart version is known.
		md := *d.Metadata()
		md.DeploymentVersion = deployment.NewDeploymentVersion(resolved.ResolvedVersion, d.Source().ShortSHA(), d.StartedAt())
		d.SetMetadata(md)
		return nil
	}

	// METADATA_GENERATED → CHART_RESOLVED
	if d.Status() == deployment.StatusMetadataGenerated {
		if err := ensureArchive(); err != nil {
			return err
		}
		if err := h.step(ctx, d, deployment.StatusChartResolved); err != nil {
			return err
		}
	}

	// CHART_RESOLVED → CHART_COMPOSED → ARTIFACT_PUBLISHED (compose + publish)
	if d.Status() == deployment.StatusChartResolved {
		if err := ensureArchive(); err != nil {
			return err
		}
		version := d.Metadata().DeploymentVersion.String()
		composed, err := h.composer.Compose(ctx, archive, d.Values(), platformValues(d), version, d.Source().ShortSHA(), annotations(d))
		if err != nil {
			return fmt.Errorf("compose chart: %w", err)
		}
		if err := h.step(ctx, d, deployment.StatusChartComposed); err != nil {
			return err
		}
		art, err := h.publisher.Publish(ctx, composed.PackageBytes, d.ApplicationID(), composed.ChartName, version)
		if err != nil {
			return fmt.Errorf("publish artifact: %w", err)
		}
		d.SetArtifact(deployment.Artifact{
			OCIReference: art.OCIReference,
			Digest:       art.Digest,
			Registry:     art.Registry,
			Repository:   art.Repository,
			Tag:          art.Tag,
		})
		if err := h.step(ctx, d, deployment.StatusArtifactPublished); err != nil {
			return err
		}
	}

	// ARTIFACT_PUBLISHED → ARGO_APP_CREATED (validate project + create/update app)
	if d.Status() == deployment.StatusArtifactPublished {
		art := d.ArtifactInfo()
		if art == nil {
			return fmt.Errorf("create argo app: no published artifact on deployment")
		}
		spec := h.argoSpec(d, *art)
		if err := h.argocd.ValidateProject(ctx, spec.Project, spec.RepoURL, spec.DestServer, spec.DestNS); err != nil {
			return fmt.Errorf("validate argo project: %w", err)
		}
		if err := h.argocd.CreateOrUpdate(ctx, spec); err != nil {
			return fmt.Errorf("create/update argo app: %w", err)
		}
		d.SetArgoApp(deployment.ArgoApp{Name: spec.Name, Namespace: spec.DestNS, Project: spec.Project})
		if err := h.step(ctx, d, deployment.StatusArgoAppCreated); err != nil {
			return err
		}
	}

	// ARGO_APP_CREATED → SYNCING
	if d.Status() == deployment.StatusArgoAppCreated {
		if d.ArgoApp() == nil {
			return fmt.Errorf("sync: no argo app on deployment")
		}
		if err := h.argocd.Sync(ctx, d.Metadata().ArgoAppName.String()); err != nil {
			return fmt.Errorf("sync argo app: %w", err)
		}
		if err := h.step(ctx, d, deployment.StatusSyncing); err != nil {
			return err
		}
	}

	// SYNCING → HEALTHY | DEGRADED (poll ArgoCD until convergence or timeout)
	if d.Status() == deployment.StatusSyncing {
		healthy, err := h.pollHealth(ctx, d)
		if err != nil {
			return err
		}
		if healthy {
			if err := h.step(ctx, d, deployment.StatusHealthy); err != nil {
				return err
			}
		} else {
			if err := h.step(ctx, d, deployment.StatusDegraded); err != nil {
				return err
			}
			return fmt.Errorf("deployment degraded: %s", d.HealthInfo().Message)
		}
	}

	// HEALTHY → COMPLETED
	if d.Status() == deployment.StatusHealthy {
		if err := d.Complete(); err != nil {
			return fmt.Errorf("complete: %w", err)
		}
		if err := h.repo.Save(ctx, d); err != nil {
			return fmt.Errorf("save completed: %w", err)
		}
		h.logger.InfoContext(ctx, "deployment completed", slog.String("id", d.ID().String()))
	}

	return nil
}

// pollHealth polls ArgoCD until the app is Healthy (true) or Degraded (false),
// recording the last observed health. It is bounded by ConvergenceTimeout.
func (h *DeployExecutionHandler) pollHealth(ctx context.Context, d *deployment.Deployment) (bool, error) {
	appName := d.Metadata().ArgoAppName.String()
	deadline := time.Now().Add(h.cfg.ConvergenceTimeout)
	ticker := time.NewTicker(h.cfg.PollInterval)
	defer ticker.Stop()

	for {
		st, err := h.argocd.Status(ctx, appName)
		if err != nil {
			return false, fmt.Errorf("poll argo status: %w", err)
		}
		d.SetHealth(deployment.Health{
			SyncStatus:   st.SyncStatus,
			HealthStatus: st.HealthStatus,
			Message:      st.Message,
			EvaluatedAt:  time.Now().UTC(),
		})
		_ = h.repo.Save(ctx, d)

		switch st.HealthStatus {
		case "Healthy":
			return true, nil
		case "Degraded", "Missing":
			return false, nil
		}
		if time.Now().After(deadline) {
			return false, nil // timed out short of convergence → treat as degraded
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-ticker.C:
		}
	}
}

// step transitions the aggregate and persists it.
func (h *DeployExecutionHandler) step(ctx context.Context, d *deployment.Deployment, next deployment.Status) error {
	if err := d.TransitionTo(next); err != nil {
		return fmt.Errorf("transition to %s: %w", next, err)
	}
	if err := h.repo.Save(ctx, d); err != nil {
		return fmt.Errorf("save at %s: %w", next, err)
	}
	h.logger.InfoContext(ctx, "deploy step",
		slog.String("id", d.ID().String()), slog.String("status", next.String()))
	return nil
}

// argoSpec builds the ArgoCD Application spec from the published artifact.
// ArgoCD's OCI Helm source wants repoURL = registry host, chart = the path after
// the host, targetRevision = the version tag; derive them from the OCI reference.
func (h *DeployExecutionHandler) argoSpec(d *deployment.Deployment, art deployment.Artifact) port.ArgoAppSpec {
	full := strings.TrimPrefix(art.OCIReference, "oci://")
	full = strings.TrimSuffix(full, ":"+art.Tag)
	chartPath := strings.TrimPrefix(full, art.Registry+"/")

	return port.ArgoAppSpec{
		Name:       d.Metadata().ArgoAppName.String(),
		Namespace:  h.cfg.ArgoAppNamespace,
		Project:    d.Target().AppProject(),
		RepoURL:    art.Registry,
		Chart:      chartPath,
		Version:    art.Tag,
		DestServer: h.cfg.DestServer,
		DestNS:     d.Target().Namespace(),
		Labels:     baseMetadata(d).Labels,
		Annotations: map[string]string{
			"platform.myorg.io/deployment-id":  d.ID().String(),
			"platform.myorg.io/correlation-id": d.CorrelationID(),
		},
	}
}

// baseMetadata computes the component id, argo app name, and labels. The
// deployment version is stamped later, once the chart is resolved.
func baseMetadata(d *deployment.Deployment) deployment.Metadata {
	t := d.Target()
	return deployment.Metadata{
		ComponentID: deployment.NewComponentID(d.ApplicationID(), t.Environment(), t.Cluster(), d.Source().ShortSHA()),
		ArgoAppName: deployment.NewArgoAppName(d.ApplicationID(), t.Environment(), t.Namespace()),
		Labels: map[string]string{
			"app.kubernetes.io/instance":    d.ApplicationID(),
			"app.kubernetes.io/managed-by":  "platform-orchestrator",
			"platform.myorg.io/team":        d.Team(),
			"platform.myorg.io/environment": t.Environment(),
		},
	}
}

// platformValues are values the platform enforces on every deployment,
// overriding any user overlay (they win in the composer merge). The J3 spine
// enforces its locks at the create boundary (ADR-0006), so this is currently
// empty — it is the seam where platform-forced chart values (e.g. a hardened
// securityContext) attach without touching the composer.
func platformValues(_ *deployment.Deployment) map[string]any { return nil }

// annotations carries deployment provenance onto the composed chart.
func annotations(d *deployment.Deployment) map[string]string {
	a := map[string]string{
		"platform.myorg.io/deployment-id": d.ID().String(),
		"platform.myorg.io/component-id":  d.Metadata().ComponentID.String(),
	}
	if d.CorrelationID() != "" {
		a["platform.myorg.io/correlation-id"] = d.CorrelationID()
	}
	return a
}

// compile-time proof the executor satisfies the inbound trigger port.
var _ DeploymentExecutor = (*DeployExecutionHandler)(nil)

package deploymentapp

import (
	"context"
	"fmt"
	"strings"
	"sync"
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

	// inflight guards against two goroutines driving the same deployment at once
	// (e.g. the Create trigger racing a :deploy re-drive, or a double re-drive).
	// Each Execute upserts the aggregate, so concurrent drives would last-write-win
	// and lose transitions. This is a single-process guard; across replicas the
	// persistence layer still needs optimistic concurrency — see the note on
	// Execute — but it closes the common same-pod window cheaply.
	mu       sync.Mutex
	inflight map[deployment.DeploymentID]struct{}
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
		inflight: map[deployment.DeploymentID]struct{}{},
	}
}

// acquire marks a deployment as being driven. It returns false if another
// goroutine in this process is already driving it.
func (h *DeployExecutionHandler) acquire(id deployment.DeploymentID) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, busy := h.inflight[id]; busy {
		return false
	}
	h.inflight[id] = struct{}{}
	return true
}

func (h *DeployExecutionHandler) release(id deployment.DeploymentID) {
	h.mu.Lock()
	delete(h.inflight, id)
	h.mu.Unlock()
}

// Execute loads a deployment and drives it forward. Terminal deployments are a
// no-op. On any step error the aggregate is marked FAILED and persisted, and
// the error is returned (callers run this in a goroutine and only log it).
//
// Concurrency: a per-deployment in-process lock (acquire/release) prevents two
// goroutines in this process from driving the same deployment simultaneously.
// It does NOT coordinate across replicas — a horizontally-scaled deployment
// needs optimistic concurrency (a version/updatedAt CAS) in the repository Save
// to be fully safe; that is a persistence-layer follow-up.
func (h *DeployExecutionHandler) Execute(ctx context.Context, id deployment.DeploymentID) error {
	if !h.acquire(id) {
		h.logger.InfoContext(ctx, "deployment already being driven; skipping concurrent execution",
			slog.String("id", id.String()))
		return nil
	}
	defer h.release(id)

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
	// Invariant: metadata is set on the VALIDATING→METADATA_GENERATED step, so any
	// state at or beyond METADATA_GENERATED must carry it. Guard the invariant here
	// rather than deref a nil Metadata() downstream — this runs in a detached
	// goroutine, so a nil panic would take the process down instead of failing the
	// one deployment. A persisted state past metadata with nil metadata means a
	// corrupt/partial record; fail it cleanly.
	if d.Status() != deployment.StatusReceived &&
		d.Status() != deployment.StatusValidating &&
		d.Metadata() == nil {
		return fmt.Errorf("run: deployment at %s has no metadata (corrupt state)", d.Status())
	}

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
	// appValuesKey comes from the resolved chart, so platform values address the
	// application subchart by the name it actually has rather than the template's.
	var appValuesKey string
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
		appValuesKey = resolved.AppValuesKey
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
		pv, err := platformValues(d, appValuesKey)
		if err != nil {
			return fmt.Errorf("compose chart: %w", err)
		}
		composed, err := h.composer.Compose(ctx, archive, d.Values(), pv, version, d.Source().ShortSHA(), annotations(d))
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
			msg := "convergence timeout"
			if hl := d.HealthInfo(); hl != nil {
				msg = hl.Message
			}
			return fmt.Errorf("deployment degraded: %s", msg)
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
		case "Degraded":
			return false, nil
		}
		// "Missing"/"Progressing"/"" are transient while sync-waves roll and a
		// Crossplane-provisioned resource (e.g. an Azure Flexible Server, ~5-10m)
		// converges — keep polling until the deadline rather than degrading early.
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
// overriding any user overlay (they win in the composer merge).
//
// This is the ONE place the declared-dependency chart shape exists in code. A
// deployment that declares no resources still gets nil, so nothing changes for
// the deployments that came before this existed.
//
// Why it belongs here rather than at create: the umbrella is the deploy unit
// (ADR-0008), so the database and the app are installed as one Helm release and
// ordered by ArgoCD sync-wave. Emitting the building block's values into that
// release is what makes "create the app AND its Azure resources" a single
// convergence rather than two.
//
// Everything emitted here is platform-owned. `engine` in particular never comes
// from the caller: the composer merges defaults < user < platform, so these keys
// cannot be reached from the request's `values` overlay, and the create boundary
// additionally refuses an overlay that tries (see reservedValuePaths).
//
// appValuesKey is the umbrella key the application subchart hangs under, taken
// from the RESOLVED CHART rather than assumed. It used to be the literal
// `hex-scaffold`, which is the template's name and no scaffolded app's: the
// scaffolder substitutes that token for the app's own when it renders. Helm
// discards values written under a key no subchart claims — without an error, a
// warning, or a diff — so the effect was that the platform renamed the database
// XR through the stable `sqldatabase` alias while the application's bind to it
// silently kept whatever the repository had baked in. The two agreed only
// because both usually derive from the same app name; an `application.id` that
// differs from the scaffolded app's name made them disagree, and ADR-0023's
// decision 2 claims no input can do that.
//
// Verified by rendering a real scaffold: `--set
// hex-scaffold.postgres.bindBuildingBlock.instanceName=X` left the bind on the
// repository's value, while the same set under the app's own key moved it.
//
// An empty appValuesKey therefore REFUSES rather than guesses. Composing the
// database without a bind we can address is the silent disagreement this exists
// to prevent, so it returns an error and fails the deployment instead.
func platformValues(d *deployment.Deployment, appValuesKey string) (map[string]any, error) {
	resources := d.Resources()
	if len(resources) == 0 {
		return nil, nil
	}
	if appValuesKey == "" {
		return nil, fmt.Errorf(
			"cannot determine the application subchart's values key from chart %q, so the database bind cannot be addressed; "+
				"the umbrella must declare the application as a file:// dependency",
			d.ChartSource().Name())
	}

	var sqlDatabases []any
	for _, r := range resources {
		if r.Type() != deployment.ResourceTypePostgres {
			continue
		}
		sqlDatabases = append(sqlDatabases, map[string]any{
			"name": r.Name(),
			"azureFlexibleServer": map[string]any{
				"size":         r.Size(),
				"version":      r.Version(),
				"storageMb":    r.StorageMb(),
				"databaseName": r.DatabaseName(),
			},
		})
	}
	if len(sqlDatabases) == 0 {
		return nil, nil
	}

	// The umbrella's SQL building block, aliased `sqldatabase` in its Chart.yaml.
	//
	// databases.sql is a LIST, and the composer's deep-merge recurses into maps
	// only — a list replaces wholesale. That is load-bearing: the chart ships a
	// default entry, and this substitutes it rather than half-merging with it.
	vals := map[string]any{
		"sqldatabase": map[string]any{
			"enabled": true,
			// ADR-0020's paved road, and the reason this deployment bills. Not a
			// caller knob: an app cannot ask for a different provisioner.
			"engine":      "azure-flexibleserver",
			"team":        d.Team(),
			"environment": d.Target().Environment(),
			"databases": map[string]any{
				"sql": sqlDatabases,
			},
		},
	}

	// The app's half of the contract. instanceName is the SAME derived name, and
	// hex-scaffold derives all four secretKeyRefs from it (slice S1), so "which
	// database" is one value rather than five copies of one. Both halves are
	// written from the same Resource here, which is what makes them unable to
	// disagree — the failure this slice exists to close.
	//
	// The alias is the subchart's name in the umbrella's Chart.yaml. It is
	// deliberately NOT the deployment's application id: the umbrella is rendered
	// from the net-hexagonal template, and the template's subchart is named
	// hex-scaffold whatever the app scaffolded from it is called.
	if bind := bindValues(resources); bind != nil {
		vals[appValuesKey] = bind
	}
	return vals, nil
}

// bindValues builds the application's bind to the composed connection Secrets.
// Exactly one postgres instance can be bound, which the aggregate already
// guarantees (see validateResourceSet) — this reads the first and does not
// invent a policy of its own.
func bindValues(resources []deployment.Resource) map[string]any {
	for _, r := range resources {
		if r.Type() != deployment.ResourceTypePostgres {
			continue
		}
		return map[string]any{
			"postgres": map[string]any{
				"bindBuildingBlock": map[string]any{
					"enabled":      true,
					"instanceName": r.Name(),
				},
			},
		}
	}
	return nil
}

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

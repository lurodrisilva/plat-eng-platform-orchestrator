package deploymentapp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/myorg/platform-orchestrator/internal/application/port"
	"github.com/myorg/platform-orchestrator/internal/domain/deployment"
)

// --- fakes (hand-written; the repo uses no mocking library) ---

// storeRepo is an in-memory deployment.Repository keyed by ID.
type storeRepo struct {
	byID  map[string]*deployment.Deployment
	saves int
}

func newStoreRepo(ds ...*deployment.Deployment) *storeRepo {
	r := &storeRepo{byID: map[string]*deployment.Deployment{}}
	for _, d := range ds {
		r.byID[d.ID().String()] = d
	}
	return r
}

func (r *storeRepo) Save(ctx context.Context, d *deployment.Deployment) error {
	r.saves++
	r.byID[d.ID().String()] = d
	return nil
}
func (r *storeRepo) FindByID(ctx context.Context, id deployment.DeploymentID) (*deployment.Deployment, error) {
	d, ok := r.byID[id.String()]
	if !ok {
		return nil, deployment.ErrNotFound
	}
	return d, nil
}
func (r *storeRepo) FindByApplication(ctx context.Context, appID, environment string, limit int) ([]*deployment.Deployment, error) {
	return nil, nil
}

type fakeResolver struct {
	called int
	err    error
}

func (f *fakeResolver) Resolve(ctx context.Context, repository, name, constraint string, allowPrerelease bool) (port.ResolvedChart, error) {
	f.called++
	if f.err != nil {
		return port.ResolvedChart{}, f.err
	}
	return port.ResolvedChart{
		SourceRepository: repository,
		ChartName:        name,
		ResolvedVersion:  "1.2.3",
		ResolvedTag:      "1.2.3",
		ArchiveBytes:     []byte("umbrella-archive"),
	}, nil
}

type fakeComposer struct {
	called int
	err    error
}

func (f *fakeComposer) Compose(ctx context.Context, archive []byte, values, platformValues map[string]any, version, appVersion string, annotations map[string]string) (port.ComposedChart, error) {
	f.called++
	if f.err != nil {
		return port.ComposedChart{}, f.err
	}
	return port.ComposedChart{
		ChartName:         "myapp",
		DeploymentVersion: version,
		PackageBytes:      []byte("composed-package"),
	}, nil
}

type fakePublisher struct {
	called int
	err    error
}

func (f *fakePublisher) Publish(ctx context.Context, chartBytes []byte, appID, chartName, version string) (port.PublishedArtifact, error) {
	f.called++
	if f.err != nil {
		return port.PublishedArtifact{}, f.err
	}
	return port.PublishedArtifact{
		OCIReference: "oci://acr.example.com/" + appID + "/charts/" + chartName + ":" + version,
		Digest:       "sha256:deadbeef",
		Registry:     "acr.example.com",
		Repository:   appID + "/charts/" + chartName,
		Tag:          version,
	}, nil
}

// fakeArgoCD records calls and returns a canned status for polling.
type fakeArgoCD struct {
	validate, create, sync, status int
	statusResp                     port.ArgoAppStatus
	statusErr                      error
	lastSpec                       port.ArgoAppSpec
}

func (f *fakeArgoCD) ValidateProject(ctx context.Context, project, sourceRepo, destServer, destNS string) error {
	f.validate++
	return nil
}
func (f *fakeArgoCD) CreateOrUpdate(ctx context.Context, spec port.ArgoAppSpec) error {
	f.create++
	f.lastSpec = spec
	return nil
}
func (f *fakeArgoCD) Sync(ctx context.Context, appName string) error { f.sync++; return nil }
func (f *fakeArgoCD) Status(ctx context.Context, appName string) (port.ArgoAppStatus, error) {
	f.status++
	if f.statusErr != nil {
		return port.ArgoAppStatus{}, f.statusErr
	}
	return f.statusResp, nil
}
func (f *fakeArgoCD) Delete(ctx context.Context, appName string) error { return nil }

// newReceivedDeployment builds a valid RECEIVED deployment for the executor.
func newReceivedDeployment(t *testing.T) *deployment.Deployment {
	t.Helper()
	image, err := deployment.NewImage("registry.example.com/app", "v1.0.0", "sha256:abc123def456")
	if err != nil {
		t.Fatalf("image: %v", err)
	}
	chart, err := deployment.NewChartSource("myorg/charts", "myapp", "^1.0.0", false)
	if err != nil {
		t.Fatalf("chart: %v", err)
	}
	target, err := deployment.NewTarget("production", "aks-prod", "default", "myproject")
	if err != nil {
		t.Fatalf("target: %v", err)
	}
	source, err := deployment.NewSource("abc123def456789012345678901234567890abcd", "refs/heads/main", "12345", 1, "deploy", "johndoe", "myorg/app")
	if err != nil {
		t.Fatalf("source: %v", err)
	}
	d, err := deployment.New("payment-service", "payments", image, chart, target, source, nil, "corr-1")
	if err != nil {
		t.Fatalf("new deployment: %v", err)
	}
	return d
}

// fastCfg keeps the poll interval tiny so degraded/timeout paths don't stall.
func fastCfg() ExecutionConfig {
	return ExecutionConfig{PollInterval: time.Millisecond, ConvergenceTimeout: 50 * time.Millisecond}
}

func TestExecute_HappyPath_DrivesToCompleted(t *testing.T) {
	d := newReceivedDeployment(t)
	repo := newStoreRepo(d)
	res, comp, pub := &fakeResolver{}, &fakeComposer{}, &fakePublisher{}
	argo := &fakeArgoCD{statusResp: port.ArgoAppStatus{SyncStatus: "Synced", HealthStatus: "Healthy"}}

	h := NewDeployExecutionHandler(repo, res, comp, pub, argo, fastCfg(), nil)
	if err := h.Execute(context.Background(), d.ID()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if got := d.Status(); got != deployment.StatusCompleted {
		t.Fatalf("final status = %s, want COMPLETED", got)
	}
	if res.called != 1 {
		t.Errorf("resolver called %d times, want 1", res.called)
	}
	if comp.called != 1 || pub.called != 1 {
		t.Errorf("compose=%d publish=%d, want 1/1", comp.called, pub.called)
	}
	if argo.validate != 1 || argo.create != 1 || argo.sync != 1 {
		t.Errorf("argo validate=%d create=%d sync=%d, want 1/1/1", argo.validate, argo.create, argo.sync)
	}
	// Metadata + artifact must be stamped for GET to read.
	if d.Metadata() == nil || d.Metadata().DeploymentVersion.String() == "" {
		t.Error("deployment version not stamped")
	}
	if d.ArtifactInfo() == nil || d.ArtifactInfo().Tag == "" {
		t.Error("artifact not recorded")
	}
	// ArgoCD OCI source: repoURL = registry host, chart = path after host.
	if argo.lastSpec.RepoURL != "acr.example.com" {
		t.Errorf("argo repoURL = %q, want registry host", argo.lastSpec.RepoURL)
	}
	if argo.lastSpec.Chart != "payment-service/charts/myapp" {
		t.Errorf("argo chart = %q, want path after host", argo.lastSpec.Chart)
	}
}

func TestExecute_Degraded_MarksFailed(t *testing.T) {
	d := newReceivedDeployment(t)
	repo := newStoreRepo(d)
	argo := &fakeArgoCD{statusResp: port.ArgoAppStatus{SyncStatus: "OutOfSync", HealthStatus: "Degraded", Message: "pod crashloop"}}

	h := NewDeployExecutionHandler(repo, &fakeResolver{}, &fakeComposer{}, &fakePublisher{}, argo, fastCfg(), nil)
	err := h.Execute(context.Background(), d.ID())
	if err == nil {
		t.Fatal("expected degraded error, got nil")
	}
	// Execute wraps the degraded run in Fail(): terminal FAILED, health recorded.
	if got := d.Status(); got != deployment.StatusFailed {
		t.Fatalf("status = %s, want FAILED", got)
	}
	if d.HealthInfo() == nil || d.HealthInfo().HealthStatus != "Degraded" {
		t.Error("degraded health not recorded")
	}
}

func TestExecute_Timeout_MarksFailed(t *testing.T) {
	d := newReceivedDeployment(t)
	repo := newStoreRepo(d)
	// Never Healthy/Degraded → pollHealth exhausts ConvergenceTimeout → degraded.
	argo := &fakeArgoCD{statusResp: port.ArgoAppStatus{SyncStatus: "Synced", HealthStatus: "Progressing"}}

	h := NewDeployExecutionHandler(repo, &fakeResolver{}, &fakeComposer{}, &fakePublisher{}, argo, fastCfg(), nil)
	if err := h.Execute(context.Background(), d.ID()); err == nil {
		t.Fatal("expected timeout→degraded error, got nil")
	}
	if got := d.Status(); got != deployment.StatusFailed {
		t.Fatalf("status = %s, want FAILED", got)
	}
	if argo.status < 2 {
		t.Errorf("expected repeated polling, status calls = %d", argo.status)
	}
}

func TestExecute_TerminalDeployment_NoOp(t *testing.T) {
	// A COMPLETED deployment must not be re-driven.
	base := newReceivedDeployment(t)
	now := time.Now().UTC()
	done := deployment.Reconstitute(
		base.ID(), base.ApplicationID(), base.Team(),
		base.Image(), base.ChartSource(), base.Target(), base.Source(),
		nil, base.CorrelationID(),
		deployment.StatusCompleted,
		nil, nil, nil, nil,
		"", now, now, now,
	)
	repo := newStoreRepo(done)
	res, comp, pub := &fakeResolver{}, &fakeComposer{}, &fakePublisher{}
	argo := &fakeArgoCD{}

	h := NewDeployExecutionHandler(repo, res, comp, pub, argo, fastCfg(), nil)
	if err := h.Execute(context.Background(), base.ID()); err != nil {
		t.Fatalf("terminal Execute should be a no-op, got %v", err)
	}
	if res.called+comp.called+pub.called+argo.create != 0 {
		t.Error("no adapter should be called for a terminal deployment")
	}
}

func TestExecute_ReDriveFromMidFlight_Completes(t *testing.T) {
	// Simulate a restart: the deployment is persisted at ARTIFACT_PUBLISHED with
	// its metadata + artifact intact. The re-drive must resume from there, skip
	// resolve/compose/publish, and converge to COMPLETED.
	base := newReceivedDeployment(t)
	now := time.Now().UTC()
	md := &deployment.Metadata{
		ComponentID:       deployment.NewComponentID("payment-service", "production", "aks-prod", base.Source().ShortSHA()),
		DeploymentVersion: deployment.NewDeploymentVersion("1.2.3", base.Source().ShortSHA(), now),
		ArgoAppName:       deployment.NewArgoAppName("payment-service", "production", "default"),
		Labels:            map[string]string{"app.kubernetes.io/instance": "payment-service"},
	}
	art := &deployment.Artifact{
		OCIReference: "oci://acr.example.com/payment-service/charts/myapp:" + md.DeploymentVersion.String(),
		Digest:       "sha256:deadbeef",
		Registry:     "acr.example.com",
		Repository:   "payment-service/charts/myapp",
		Tag:          md.DeploymentVersion.String(),
	}
	mid := deployment.Reconstitute(
		base.ID(), base.ApplicationID(), base.Team(),
		base.Image(), base.ChartSource(), base.Target(), base.Source(),
		nil, base.CorrelationID(),
		deployment.StatusArtifactPublished,
		md, art, nil, nil,
		"", now, time.Time{}, now,
	)
	repo := newStoreRepo(mid)
	res, comp, pub := &fakeResolver{}, &fakeComposer{}, &fakePublisher{}
	argo := &fakeArgoCD{statusResp: port.ArgoAppStatus{SyncStatus: "Synced", HealthStatus: "Healthy"}}

	h := NewDeployExecutionHandler(repo, res, comp, pub, argo, fastCfg(), nil)
	if err := h.Execute(context.Background(), base.ID()); err != nil {
		t.Fatalf("re-drive: %v", err)
	}
	if got := mid.Status(); got != deployment.StatusCompleted {
		t.Fatalf("status = %s, want COMPLETED", got)
	}
	// Pre-publish stages must be skipped on a re-drive from ARTIFACT_PUBLISHED.
	if res.called != 0 || comp.called != 0 || pub.called != 0 {
		t.Errorf("pre-publish stages must be skipped: resolve=%d compose=%d publish=%d", res.called, comp.called, pub.called)
	}
	if argo.create != 1 || argo.sync != 1 {
		t.Errorf("argo create=%d sync=%d, want 1/1", argo.create, argo.sync)
	}
}

// compile-time proof the executor still satisfies the trigger port.
var _ DeploymentExecutor = (*DeployExecutionHandler)(nil)

func TestExecute_LoadError_Propagates(t *testing.T) {
	repo := newStoreRepo() // empty
	h := NewDeployExecutionHandler(repo, &fakeResolver{}, &fakeComposer{}, &fakePublisher{}, &fakeArgoCD{}, fastCfg(), nil)
	err := h.Execute(context.Background(), deployment.DeploymentID("missing"))
	if err == nil || !errors.Is(err, deployment.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestExecute_InflightGuard_BlocksConcurrentDrive(t *testing.T) {
	h := NewDeployExecutionHandler(newStoreRepo(), &fakeResolver{}, &fakeComposer{}, &fakePublisher{}, &fakeArgoCD{}, fastCfg(), nil)
	id := deployment.DeploymentID("dep-1")

	if !h.acquire(id) {
		t.Fatal("first acquire should succeed")
	}
	if h.acquire(id) {
		t.Fatal("second acquire must fail while in-flight")
	}
	if !h.acquire(deployment.DeploymentID("dep-2")) {
		t.Fatal("a different deployment must acquire independently")
	}
	h.release(id)
	if !h.acquire(id) {
		t.Fatal("acquire must succeed again after release")
	}
}

func TestExecute_CorruptState_NoMetadata_FailsCleanly(t *testing.T) {
	// A record persisted past METADATA_GENERATED but with nil metadata is corrupt.
	// The executor must fail it cleanly (not panic in the detached goroutine).
	base := newReceivedDeployment(t)
	now := time.Now().UTC()
	corrupt := deployment.Reconstitute(
		base.ID(), base.ApplicationID(), base.Team(),
		base.Image(), base.ChartSource(), base.Target(), base.Source(),
		nil, base.CorrelationID(),
		deployment.StatusChartResolved,
		nil, nil, nil, nil, // metadata nil despite CHART_RESOLVED
		"", now, time.Time{}, now,
	)
	repo := newStoreRepo(corrupt)
	h := NewDeployExecutionHandler(repo, &fakeResolver{}, &fakeComposer{}, &fakePublisher{}, &fakeArgoCD{}, fastCfg(), nil)

	err := h.Execute(context.Background(), base.ID())
	if err == nil {
		t.Fatal("expected corrupt-state error, got nil")
	}
	// Fail() records the error before the (valid) CHART_RESOLVED→FAILED transition.
	if got := corrupt.Status(); got != deployment.StatusFailed {
		t.Fatalf("status = %s, want FAILED", got)
	}
	if corrupt.Error() == "" {
		t.Error("error message must be persisted on the failed deployment")
	}
}

package deploymentapp

import (
	"context"
	"errors"
	"testing"

	"github.com/myorg/platform-orchestrator/internal/application/port"
	"github.com/myorg/platform-orchestrator/internal/domain/deployment"
)

// fakeRepo is a hand-written fake (the repo uses no mocking library).
type fakeRepo struct{ saved int }

func (f *fakeRepo) Save(ctx context.Context, d *deployment.Deployment) error {
	f.saved++
	return nil
}
func (f *fakeRepo) FindByID(ctx context.Context, id deployment.DeploymentID) (*deployment.Deployment, error) {
	return nil, deployment.ErrNotFound
}
func (f *fakeRepo) FindByApplication(ctx context.Context, appID, environment string, limit int) ([]*deployment.Deployment, error) {
	return nil, nil
}

// fakeAllowlist returns a canned decision.
type fakeAllowlist struct{ dec port.AllowlistDecision }

func (f fakeAllowlist) Validate(ctx context.Context, values map[string]any, environment string) port.AllowlistDecision {
	return f.dec
}

func validCommand() CreateDeploymentCommand {
	return CreateDeploymentCommand{
		ApplicationID:    "payment-service",
		Team:             "payments",
		ImageRepository:  "registry.example.com/app",
		ImageTag:         "v1.0.0",
		ImageDigest:      "sha256:abc123def456",
		ChartRepository:  "myorg/charts",
		ChartName:        "myapp",
		Environment:      "production",
		Cluster:          "aks-prod",
		Namespace:        "default",
		AppProject:       "myproject",
		GitSHA:           "abc123def456789012345678901234567890abcd",
		GitRef:           "refs/heads/main",
		GitHubRunID:      "12345",
		GitHubRunAttempt: 1,
		WorkflowName:     "deploy",
		Actor:            "johndoe",
		RepositoryFull:   "myorg/app",
		Values:           map[string]any{"securityContext": map[string]any{"runAsNonRoot": false}},
	}
}

func TestCreateDeployment_LockedKnob_Rejected_Enforce(t *testing.T) {
	repo := &fakeRepo{}
	al := fakeAllowlist{dec: port.AllowlistDecision{
		Reject:     true,
		Violations: []string{"securityContext.runAsNonRoot"},
		Mode:       "enforce",
	}}
	h := NewCreateDeploymentHandler(repo, nil, al, nil)

	_, err := h.Handle(context.Background(), validCommand())
	if err == nil {
		t.Fatal("expected LockedKnobError, got nil")
	}
	var locked *LockedKnobError
	if !errors.As(err, &locked) {
		t.Fatalf("error = %v, want *LockedKnobError", err)
	}
	if !errors.Is(err, deployment.ErrTunableLocked) {
		t.Error("LockedKnobError must wrap ErrTunableLocked")
	}
	if repo.saved != 0 {
		t.Errorf("deployment must NOT be persisted on reject, saved = %d", repo.saved)
	}
}

func TestCreateDeployment_LockedKnob_Audited_NotBlocked(t *testing.T) {
	repo := &fakeRepo{}
	al := fakeAllowlist{dec: port.AllowlistDecision{
		Reject:     false,
		Violations: []string{"securityContext.runAsNonRoot"},
		Mode:       "audit",
	}}
	h := NewCreateDeploymentHandler(repo, nil, al, nil)

	res, err := h.Handle(context.Background(), validCommand())
	if err != nil {
		t.Fatalf("audit mode must not block: %v", err)
	}
	if repo.saved != 1 {
		t.Errorf("deployment should persist in audit mode, saved = %d", repo.saved)
	}
	if res.DeploymentID == "" {
		t.Error("expected a deployment ID")
	}
}

func TestCreateDeployment_NoAllowlist_Passes(t *testing.T) {
	repo := &fakeRepo{}
	h := NewCreateDeploymentHandler(repo, nil, nil, nil)

	if _, err := h.Handle(context.Background(), validCommand()); err != nil {
		t.Fatalf("nil allowlist should skip the gate: %v", err)
	}
	if repo.saved != 1 {
		t.Errorf("expected persist with no allowlist, saved = %d", repo.saved)
	}
}

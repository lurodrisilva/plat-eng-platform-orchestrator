package deploymentapp

import (
	"context"
	"errors"
	"strings"
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
	h := NewCreateDeploymentHandler(repo, nil, al, nil, nil)

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
	h := NewCreateDeploymentHandler(repo, nil, al, nil, nil)

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
	h := NewCreateDeploymentHandler(repo, nil, nil, nil, nil)

	if _, err := h.Handle(context.Background(), validCommand()); err != nil {
		t.Fatalf("nil allowlist should skip the gate: %v", err)
	}
	if repo.saved != 1 {
		t.Errorf("expected persist with no allowlist, saved = %d", repo.saved)
	}
}

// --- declared application dependencies (S4, ADR-0023) ------------------------

// The end-to-end create path for a legitimate request: the resource reaches the
// aggregate, named after the application, and is persisted with it.
func TestCreateDeployment_AcceptsAndRecordsADeclaredResource(t *testing.T) {
	repo := &fakeRepo{}
	h := NewCreateDeploymentHandler(repo, nil, nil, allowPolicy{}, nil)

	cmd := validCommand()
	cmd.Values = nil
	cmd.Environment = "development"
	cmd.Resources = []ResourceSpec{{Type: "postgres", Size: "small", Version: "16"}}

	if _, err := h.Handle(context.Background(), cmd); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if repo.saved != 1 {
		t.Fatalf("saved %d deployments, want 1", repo.saved)
	}
}

func TestCreateDeployment_RefusedResourceIsItsOwnError(t *testing.T) {
	h := NewCreateDeploymentHandler(&fakeRepo{}, nil, nil, denyPolicy{"postgres is not allowed in production"}, nil)

	cmd := validCommand()
	cmd.Values = nil
	cmd.Resources = []ResourceSpec{{Type: "postgres", Size: "small", Version: "16"}}

	_, err := h.Handle(context.Background(), cmd)
	var notAllowed *ResourceNotAllowedError
	if !errors.As(err, &notAllowed) {
		t.Fatalf("err = %v, want a ResourceNotAllowedError (maps to 422 RESOURCE_NOT_ALLOWED)", err)
	}
}

// A structurally invalid resource must not be reported as a policy refusal:
// the caller can fix this one by editing the request.
func TestCreateDeployment_StructurallyInvalidResourceIsNotAPolicyRefusal(t *testing.T) {
	h := NewCreateDeploymentHandler(&fakeRepo{}, nil, nil, allowPolicy{}, nil)

	cmd := validCommand()
	cmd.Values = nil
	cmd.Resources = []ResourceSpec{{Type: "postgres", Size: "enormous"}}

	_, err := h.Handle(context.Background(), cmd)
	if err == nil {
		t.Fatal("expected an error")
	}
	var notAllowed *ResourceNotAllowedError
	if errors.As(err, &notAllowed) {
		t.Error("a bad size must not surface as a resource-policy refusal")
	}
}

// The bypass. `values.sqldatabase` reaches the building block directly, so it is
// refused whatever the allowlist mode says — here the allowlist is in audit mode
// and reports nothing, which is exactly the configuration that would otherwise
// let a billed server through unauthorized.
func TestCreateDeployment_ReservedValuePathRefusedEvenInAuditMode(t *testing.T) {
	al := fakeAllowlist{dec: port.AllowlistDecision{Reject: false, Mode: "audit"}}
	h := NewCreateDeploymentHandler(&fakeRepo{}, nil, al, allowPolicy{}, nil)

	cmd := validCommand()
	cmd.Values = map[string]any{"sqldatabase": map[string]any{"enabled": true}}

	_, err := h.Handle(context.Background(), cmd)
	var locked *LockedKnobError
	if !errors.As(err, &locked) {
		t.Fatalf("err = %v, want a LockedKnobError", err)
	}
	if !errors.Is(err, deployment.ErrTunableLocked) {
		t.Error("the error must unwrap to the domain sentinel")
	}
	if !strings.Contains(err.Error(), "sqldatabase") {
		t.Errorf("error = %q, want it to name the reserved path", err.Error())
	}
}

// Nothing is saved when a resource is refused: a rejected deployment must not
// leave a record that a re-drive could pick up and provision from.
func TestCreateDeployment_RefusalPersistsNothing(t *testing.T) {
	repo := &fakeRepo{}
	h := NewCreateDeploymentHandler(repo, nil, nil, denyPolicy{"nope"}, nil)

	cmd := validCommand()
	cmd.Values = nil
	cmd.Resources = []ResourceSpec{{Type: "postgres"}}

	if _, err := h.Handle(context.Background(), cmd); err == nil {
		t.Fatal("expected a refusal")
	}
	if repo.saved != 0 {
		t.Errorf("saved %d deployments, want 0", repo.saved)
	}
}

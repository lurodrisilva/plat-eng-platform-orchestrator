package deployment

import (
	"testing"
)

func validImage(t *testing.T) Image {
	t.Helper()
	img, err := NewImage("registry.example.com/app", "v1.0.0", "sha256:abc123def456")
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func validChart(t *testing.T) ChartSource {
	t.Helper()
	cs, err := NewChartSource("myorg/charts", "myapp", "", false)
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

func validTarget(t *testing.T) Target {
	t.Helper()
	tgt, err := NewTarget("production", "aks-prod", "default", "myproject")
	if err != nil {
		t.Fatal(err)
	}
	return tgt
}

func validSource(t *testing.T) Source {
	t.Helper()
	src, err := NewSource("abc123def456789012345678901234567890abcd", "refs/heads/main", "12345", 1, "deploy", "johndoe", "myorg/app")
	if err != nil {
		t.Fatal(err)
	}
	return src
}

func TestNew_ValidDeployment(t *testing.T) {
	d, err := New("payment-service", "payments", validImage(t), validChart(t), validTarget(t), validSource(t), nil, "")
	if err != nil {
		t.Fatalf("New() returned error: %v", err)
	}

	if d.ID().IsZero() {
		t.Error("deployment ID should not be zero")
	}
	if d.Status() != StatusReceived {
		t.Errorf("initial status = %s, want RECEIVED", d.Status())
	}
	if d.ApplicationID() != "payment-service" {
		t.Errorf("applicationID = %q, want %q", d.ApplicationID(), "payment-service")
	}
	if d.StartedAt().IsZero() {
		t.Error("startedAt should be set")
	}
}

func TestNew_MissingApplicationID(t *testing.T) {
	_, err := New("", "payments", validImage(t), validChart(t), validTarget(t), validSource(t), nil, "")
	if err == nil {
		t.Fatal("expected error for empty application ID")
	}
}

func TestNew_MissingTeam(t *testing.T) {
	_, err := New("app", "", validImage(t), validChart(t), validTarget(t), validSource(t), nil, "")
	if err == nil {
		t.Fatal("expected error for empty team")
	}
}

func TestDeployment_TransitionTo_Valid(t *testing.T) {
	d, _ := New("app", "team", validImage(t), validChart(t), validTarget(t), validSource(t), nil, "")

	if err := d.TransitionTo(StatusValidating); err != nil {
		t.Fatalf("transition to VALIDATING: %v", err)
	}
	if d.Status() != StatusValidating {
		t.Errorf("status = %s, want VALIDATING", d.Status())
	}
}

func TestDeployment_TransitionTo_Invalid(t *testing.T) {
	d, _ := New("app", "team", validImage(t), validChart(t), validTarget(t), validSource(t), nil, "")

	if err := d.TransitionTo(StatusCompleted); err == nil {
		t.Fatal("expected error for invalid transition RECEIVED → COMPLETED")
	}
}

func TestDeployment_Fail(t *testing.T) {
	d, _ := New("app", "team", validImage(t), validChart(t), validTarget(t), validSource(t), nil, "")
	_ = d.TransitionTo(StatusValidating)

	if err := d.Fail("chart not found"); err != nil {
		t.Fatalf("Fail(): %v", err)
	}
	if d.Status() != StatusFailed {
		t.Errorf("status = %s, want FAILED", d.Status())
	}
	if d.Error() != "chart not found" {
		t.Errorf("error = %q, want %q", d.Error(), "chart not found")
	}
	if d.CompletedAt().IsZero() {
		t.Error("completedAt should be set after failure")
	}
}

func TestDeployment_Complete(t *testing.T) {
	d, _ := New("app", "team", validImage(t), validChart(t), validTarget(t), validSource(t), nil, "")
	_ = d.TransitionTo(StatusValidating)
	_ = d.TransitionTo(StatusMetadataGenerated)
	_ = d.TransitionTo(StatusChartResolved)
	_ = d.TransitionTo(StatusChartComposed)
	_ = d.TransitionTo(StatusArtifactPublished)
	_ = d.TransitionTo(StatusArgoAppCreated)
	_ = d.TransitionTo(StatusSyncing)
	_ = d.TransitionTo(StatusHealthy)

	if err := d.Complete(); err != nil {
		t.Fatalf("Complete(): %v", err)
	}
	if !d.Status().IsSuccess() {
		t.Error("status should be success after Complete()")
	}
}

func TestDeployment_Values_CopyProtection(t *testing.T) {
	original := map[string]any{"key": "value"}
	d, _ := New("app", "team", validImage(t), validChart(t), validTarget(t), validSource(t), original, "")

	// Mutating original should not affect deployment
	original["key"] = "mutated"

	got := d.Values()
	if got["key"] != "value" {
		t.Error("values should be copied on construction, mutation leaked")
	}

	// Mutating returned values should not affect deployment
	got["key"] = "mutated-again"
	if d.Values()["key"] != "value" {
		t.Error("values should be copied on retrieval, mutation leaked")
	}
}

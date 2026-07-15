package policy

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// seedKeys mirrors the slice-1 allowlist in policies/default.yaml.
var seedKeys = []string{
	"replicaCount",
	"resources.requests.cpu",
	"resources.requests.memory",
	"resources.limits.cpu",
	"resources.limits.memory",
	"autoscaling.minReplicas",
	"autoscaling.maxReplicas",
	"autoscaling.targetCPUUtilizationPercentage",
}

func mustAllowlist(t *testing.T, mode string, keys []string) *TunableAllowlist {
	t.Helper()
	a, err := NewTunableAllowlist(mode, keys)
	if err != nil {
		t.Fatalf("NewTunableAllowlist(%q): %v", mode, err)
	}
	return a
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestTunableAllowlist_Validate(t *testing.T) {
	tests := []struct {
		name           string
		mode           string
		values         map[string]any
		wantViolations []string
		wantReject     bool
	}{
		{
			name: "allowed sizing knobs pass",
			mode: ModeEnforce,
			values: map[string]any{
				"replicaCount": 3,
				"resources": map[string]any{
					"requests": map[string]any{"cpu": "500m", "memory": "1Gi"},
					"limits":   map[string]any{"cpu": "1", "memory": "2Gi"},
				},
				"autoscaling": map[string]any{"minReplicas": 2, "maxReplicas": 10},
			},
			wantViolations: nil,
			wantReject:     false,
		},
		{
			name: "locked securityContext override rejected (enforce)",
			mode: ModeEnforce,
			values: map[string]any{
				"replicaCount":    2,
				"securityContext": map[string]any{"runAsNonRoot": false},
			},
			wantViolations: []string{"securityContext.runAsNonRoot"},
			wantReject:     true,
		},
		{
			name: "same override only audited in audit mode",
			mode: ModeAudit,
			values: map[string]any{
				"securityContext": map[string]any{"runAsNonRoot": false},
			},
			wantViolations: []string{"securityContext.runAsNonRoot"},
			wantReject:     false,
		},
		{
			name: "empty-map locked override still seen",
			mode: ModeEnforce,
			values: map[string]any{
				"podSecurityContext": map[string]any{},
			},
			wantViolations: []string{"podSecurityContext"},
			wantReject:     true,
		},
		{
			name: "unlisted sibling under an allowed parent is locked",
			mode: ModeEnforce,
			values: map[string]any{
				"resources": map[string]any{
					"requests": map[string]any{"hugepages-2Mi": "128Mi"},
				},
			},
			wantViolations: []string{"resources.requests.hugepages-2Mi"},
			wantReject:     true,
		},
		{
			name: "multiple locked knobs reported sorted",
			mode: ModeEnforce,
			values: map[string]any{
				"image":  map[string]any{"tag": "latest"},
				"labels": map[string]any{"owner": "team-x"},
			},
			wantViolations: []string{"image.tag", "labels.owner"},
			wantReject:     true,
		},
		{
			name:           "nil values are clean",
			mode:           ModeEnforce,
			values:         nil,
			wantViolations: nil,
			wantReject:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := mustAllowlist(t, tt.mode, seedKeys)
			got := a.Validate(context.Background(), tt.values)
			if got.Reject != tt.wantReject {
				t.Errorf("Reject = %v, want %v", got.Reject, tt.wantReject)
			}
			if !equalStrings(got.Violations, tt.wantViolations) {
				t.Errorf("Violations = %v, want %v", got.Violations, tt.wantViolations)
			}
			if got.Mode != tt.mode {
				t.Errorf("Mode = %q, want %q", got.Mode, tt.mode)
			}
		})
	}
}

func TestNewTunableAllowlist_Mode(t *testing.T) {
	tests := []struct {
		name    string
		mode    string
		want    string
		wantErr bool
	}{
		{"empty defaults to audit", "", ModeAudit, false},
		{"trims and lowercases", "  ENFORCE ", ModeEnforce, false},
		{"invalid mode errors", "block", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, err := NewTunableAllowlist(tt.mode, seedKeys)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if a.Mode() != tt.want {
				t.Errorf("Mode() = %q, want %q", a.Mode(), tt.want)
			}
		})
	}
}

func TestLoadTunableAllowlist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.yaml")
	yaml := "tunableAllowlist:\n  mode: enforce\n  allowedKeys:\n    - replicaCount\n"
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	a, err := LoadTunableAllowlist(path)
	if err != nil {
		t.Fatalf("LoadTunableAllowlist: %v", err)
	}
	if a.Mode() != ModeEnforce {
		t.Errorf("Mode() = %q, want enforce", a.Mode())
	}

	dec := a.Validate(context.Background(), map[string]any{"replicaCount": 3})
	if dec.Reject || len(dec.Violations) != 0 {
		t.Errorf("allowed knob should pass, got %+v", dec)
	}
	dec = a.Validate(context.Background(), map[string]any{"image": map[string]any{"tag": "latest"}})
	if !dec.Reject {
		t.Error("locked image.tag override should reject in enforce mode")
	}
}

func TestLoadTunableAllowlist_MissingFile(t *testing.T) {
	if _, err := LoadTunableAllowlist(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Fatal("expected error for missing config file")
	}
}

package policy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/myorg/platform-orchestrator/internal/application/port"
)

func pg(size, version string, storageMb int) port.ResourceRequest {
	return port.ResourceRequest{Type: "postgres", Size: size, Version: version, StorageMb: storageMb}
}

// testPolicy mirrors the shape shipped in policies/default.yaml.
func testPolicy(t *testing.T, mode string) *ResourcePolicy {
	t.Helper()
	p, err := NewResourcePolicy(mode,
		resourceRules{
			AllowedTypes: []string{"postgres"},
			Postgres: &postgresRules{
				AllowedSizes:              []string{"small"},
				AllowedVersions:           []string{"16"},
				MaxStorageMb:              32768,
				MaxInstancesPerDeployment: 1,
			},
		},
		map[string]resourceRules{
			"development": {
				AllowedTypes: []string{"postgres"},
				Postgres: &postgresRules{
					AllowedSizes:              []string{"small", "medium"},
					AllowedVersions:           []string{"15", "16"},
					MaxStorageMb:              131072,
					MaxInstancesPerDeployment: 2,
				},
			},
			"production": {AllowedTypes: []string{}},
		})
	if err != nil {
		t.Fatalf("build policy: %v", err)
	}
	return p
}

func TestResourcePolicy_AllowsALegitimateDevelopmentRequest(t *testing.T) {
	dec := testPolicy(t, ModeEnforce).Evaluate(context.Background(),
		[]port.ResourceRequest{pg("medium", "16", 65536)}, "development")
	if dec.Reject {
		t.Fatalf("expected allowed, got rejected: %v", dec.Violations)
	}
	if len(dec.Violations) != 0 {
		t.Errorf("violations = %v, want none", dec.Violations)
	}
}

// The whole point of the production entry: an environment listed with an empty
// allowedTypes denies everything. If key PRESENCE ever stops deciding and
// CONTENT starts deciding, production silently falls back to `default` and
// permits exactly what ADR-0010 forbids. This test is that guard.
func TestResourcePolicy_ProductionDeniesPostgres(t *testing.T) {
	dec := testPolicy(t, ModeEnforce).Evaluate(context.Background(),
		[]port.ResourceRequest{pg("small", "16", 32768)}, "production")
	if !dec.Reject {
		t.Fatal("production must refuse postgres")
	}
	if !strings.Contains(strings.Join(dec.Violations, " "), "allowed: none") {
		t.Errorf("violations = %v, want the denial to read as a decision (allowed: none)", dec.Violations)
	}
}

func TestResourcePolicy_UnlistedEnvironmentFallsBackToDefault(t *testing.T) {
	p := testPolicy(t, ModeEnforce)
	// staging has no entry, so the default set applies: small/16 only.
	if dec := p.Evaluate(context.Background(), []port.ResourceRequest{pg("small", "16", 32768)}, "staging"); dec.Reject {
		t.Fatalf("small/16 must be allowed under the default set: %v", dec.Violations)
	}
	if dec := p.Evaluate(context.Background(), []port.ResourceRequest{pg("medium", "16", 32768)}, "staging"); !dec.Reject {
		t.Fatal("medium must be refused under the default set")
	}
}

func TestResourcePolicy_LimitViolations(t *testing.T) {
	tests := []struct {
		name        string
		environment string
		resources   []port.ResourceRequest
		wantIn      string
	}{
		{"size not allowed", "development", []port.ResourceRequest{pg("large", "16", 32768)}, `size "large" is not allowed`},
		{"version not allowed", "development", []port.ResourceRequest{pg("small", "14", 32768)}, `version "14" is not allowed`},
		{"storage over the ceiling", "development", []port.ResourceRequest{pg("small", "16", 262144)}, "exceeds the 131072 allowed"},
		{
			"too many instances",
			"development",
			[]port.ResourceRequest{pg("small", "16", 32768), pg("small", "16", 32768), pg("small", "16", 32768)},
			"at most 2 per deployment",
		},
		{
			"unimplemented type",
			"development",
			[]port.ResourceRequest{{Type: "cache"}},
			`type "cache" is not allowed`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dec := testPolicy(t, ModeEnforce).Evaluate(context.Background(), tc.resources, tc.environment)
			if !dec.Reject {
				t.Fatalf("expected refusal, got allowed")
			}
			if !strings.Contains(strings.Join(dec.Violations, " | "), tc.wantIn) {
				t.Errorf("violations = %v, want one containing %q", dec.Violations, tc.wantIn)
			}
		})
	}
}

// Every violation in one round trip: a caller fixing a bad request should not
// have to submit it three times to learn all three things wrong with it.
func TestResourcePolicy_ReportsEveryViolation(t *testing.T) {
	dec := testPolicy(t, ModeEnforce).Evaluate(context.Background(),
		[]port.ResourceRequest{pg("large", "14", 999999)}, "development")
	if len(dec.Violations) != 3 {
		t.Fatalf("violations = %v, want 3 (size, version, storage)", dec.Violations)
	}
}

func TestResourcePolicy_NoResourcesIsAlwaysAllowed(t *testing.T) {
	// Including under an unconfigured policy — the fail-closed posture must cost
	// nothing until someone asks the platform to spend money.
	unconfigured := &ResourcePolicy{mode: ModeEnforce, configured: false}
	for _, p := range []*ResourcePolicy{testPolicy(t, ModeEnforce), unconfigured} {
		if dec := p.Evaluate(context.Background(), nil, "production"); dec.Reject {
			t.Errorf("a deployment declaring no resources must never be refused: %v", dec.Violations)
		}
	}
}

func TestResourcePolicy_UnconfiguredRefusesEveryResourceRequest(t *testing.T) {
	dec := (&ResourcePolicy{mode: ModeEnforce, configured: false}).
		Evaluate(context.Background(), []port.ResourceRequest{pg("small", "16", 32768)}, "development")
	if !dec.Reject {
		t.Fatal("an unconfigured resource policy must refuse, not permit")
	}
}

// Postgres allowed but with no limits configured is a misconfiguration. It must
// refuse rather than treat "no ceilings" as "no ceiling".
func TestResourcePolicy_AllowedTypeWithNoLimitsRefuses(t *testing.T) {
	p, err := NewResourcePolicy(ModeEnforce, resourceRules{AllowedTypes: []string{"postgres"}}, nil)
	if err != nil {
		t.Fatalf("build policy: %v", err)
	}
	dec := p.Evaluate(context.Background(), []port.ResourceRequest{pg("small", "16", 32768)}, "development")
	if !dec.Reject {
		t.Fatal("postgres allowed with no postgres limits must refuse")
	}
	if !strings.Contains(strings.Join(dec.Violations, " "), "no postgres limits") {
		t.Errorf("violations = %v, want the misconfiguration named", dec.Violations)
	}
}

func TestResourcePolicy_AuditModeObservesWithoutBlocking(t *testing.T) {
	dec := testPolicy(t, ModeAudit).Evaluate(context.Background(),
		[]port.ResourceRequest{pg("large", "16", 32768)}, "development")
	if dec.Reject {
		t.Fatal("audit mode must not block")
	}
	if len(dec.Violations) == 0 {
		t.Fatal("audit mode must still report the would-deny")
	}
	if dec.Mode != ModeAudit {
		t.Errorf("Mode = %q, want %q", dec.Mode, ModeAudit)
	}
}

// Unlike the tunable allowlist, an unset mode here means ENFORCE. Flipping this
// default would turn every misconfigured deployment into unbudgeted spend.
func TestResourcePolicy_EmptyModeDefaultsToEnforce(t *testing.T) {
	p, err := NewResourcePolicy("", resourceRules{}, nil)
	if err != nil {
		t.Fatalf("build policy: %v", err)
	}
	if p.Mode() != ModeEnforce {
		t.Errorf("Mode() = %q, want %q", p.Mode(), ModeEnforce)
	}
	if _, err := NewResourcePolicy("observe", resourceRules{}, nil); err == nil {
		t.Error("an unrecognised mode must be an error, not a silent default")
	}
}

// The shipped policy file is the artifact that actually runs. Parse it and
// assert the two decisions that cost money if they regress.
func TestLoadResourcePolicy_ShippedDefaultFile(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", "policies", "default.yaml")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("policies/default.yaml not reachable from the test: %v", err)
	}
	p, err := LoadResourcePolicy(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !p.Configured() {
		t.Fatal("the shipped policy file must carry a resourcePolicy section")
	}
	if p.Mode() != ModeEnforce {
		t.Errorf("shipped mode = %q, want %q", p.Mode(), ModeEnforce)
	}
	if dec := p.Evaluate(context.Background(), []port.ResourceRequest{pg("small", "16", 32768)}, "production"); !dec.Reject {
		t.Error("the shipped policy must deny postgres in production (ADR-0010)")
	}
	if dec := p.Evaluate(context.Background(), []port.ResourceRequest{pg("medium", "16", 32768)}, "development"); dec.Reject {
		t.Errorf("the shipped policy must allow a medium development database: %v", dec.Violations)
	}
}

func TestLoadResourcePolicy_MissingSectionLoadsButRefuses(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policies.yaml")
	if err := os.WriteFile(path, []byte("tunableAllowlist:\n  mode: audit\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	p, err := LoadResourcePolicy(path)
	if err != nil {
		t.Fatalf("a file without a resourcePolicy section must still load: %v", err)
	}
	if p.Configured() {
		t.Error("Configured() must report the section is absent")
	}
	if dec := p.Evaluate(context.Background(), []port.ResourceRequest{pg("small", "16", 32768)}, "development"); !dec.Reject {
		t.Error("a policy with no resourcePolicy section must refuse resource requests")
	}
}

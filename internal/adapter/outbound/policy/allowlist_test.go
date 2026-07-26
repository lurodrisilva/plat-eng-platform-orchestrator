package policy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedKeys is a fixture for the MECHANISM tests below (matching, flattening,
// per-environment resolution). It no longer mirrors policies/default.yaml —
// slice S7 re-scoped the shipped keys to the umbrella's subchart, and they are
// now prefixed with its `app` ALIAS.
// Left root-scoped on purpose: these tests are about how a key set is applied,
// not which keys ship, and TestShippedAllowlist_* covers the real file.
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
	a, err := NewTunableAllowlist(mode, keys, nil)
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
			// Empty environment resolves to the default (seed) key set.
			got := a.Validate(context.Background(), tt.values, "")
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
			a, err := NewTunableAllowlist(tt.mode, seedKeys, nil)
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
	yaml := "tunableAllowlist:\n" +
		"  mode: enforce\n" +
		"  default:\n    allowedKeys:\n      - replicaCount\n" +
		"  environments:\n" +
		"    production:\n      allowedKeys:\n        - resources.requests.cpu\n"
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

	// Default set: replicaCount allowed, image.tag locked.
	dec := a.Validate(context.Background(), map[string]any{"replicaCount": 3}, "")
	if dec.Reject || len(dec.Violations) != 0 {
		t.Errorf("allowed knob should pass, got %+v", dec)
	}
	dec = a.Validate(context.Background(), map[string]any{"image": map[string]any{"tag": "latest"}}, "")
	if !dec.Reject {
		t.Error("locked image.tag override should reject in enforce mode")
	}

	// production replaces the default set: replicaCount is NOT in the prod set,
	// so it becomes locked; resources.requests.cpu is allowed.
	dec = a.Validate(context.Background(), map[string]any{"replicaCount": 3}, "production")
	if !dec.Reject || len(dec.Violations) != 1 || dec.Violations[0] != "replicaCount" {
		t.Errorf("replicaCount should be locked in production (full replacement), got %+v", dec)
	}
	dec = a.Validate(context.Background(), map[string]any{"resources": map[string]any{"requests": map[string]any{"cpu": "500m"}}}, "production")
	if dec.Reject || len(dec.Violations) != 0 {
		t.Errorf("resources.requests.cpu should pass in production, got %+v", dec)
	}
}

// TestTunableAllowlist_PerEnvironment is the core per-env DoD: autoscaling is a
// developer knob but platform-locked in production; resources.requests is
// tunable in both; securityContext is locked everywhere; an unconfigured
// environment falls back to the default set.
func TestTunableAllowlist_PerEnvironment(t *testing.T) {
	prodKeys := []string{"resources.requests.cpu", "resources.requests.memory"}
	devKeys := []string{
		"resources.requests.cpu", "resources.requests.memory",
		"autoscaling.minReplicas", "autoscaling.maxReplicas",
	}
	a := mustPerEnvAllowlist(t, ModeEnforce, seedKeys, map[string][]string{
		"production":  prodKeys,
		"development": devKeys,
	})

	autoscale := map[string]any{"autoscaling": map[string]any{"maxReplicas": 20}}
	requests := map[string]any{"resources": map[string]any{"requests": map[string]any{"cpu": "500m"}}}
	secctx := map[string]any{"securityContext": map[string]any{"runAsNonRoot": false}}

	cases := []struct {
		name       string
		values     map[string]any
		env        string
		wantReject bool
	}{
		{"autoscaling locked in production", autoscale, "production", true},
		{"autoscaling tunable in development", autoscale, "development", false},
		{"requests tunable in production", requests, "production", false},
		{"requests tunable in development", requests, "development", false},
		{"securityContext locked in production", secctx, "production", true},
		{"securityContext locked in development", secctx, "development", true},
		{"unconfigured env falls back to default (autoscaling allowed)", autoscale, "staging", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := a.Validate(context.Background(), tc.values, tc.env)
			if got.Reject != tc.wantReject {
				t.Errorf("Reject = %v, want %v (violations=%v)", got.Reject, tc.wantReject, got.Violations)
			}
		})
	}
}

func mustPerEnvAllowlist(t *testing.T, mode string, defaultKeys []string, envKeys map[string][]string) *TunableAllowlist {
	t.Helper()
	a, err := NewTunableAllowlist(mode, defaultKeys, envKeys)
	if err != nil {
		t.Fatalf("NewTunableAllowlist: %v", err)
	}
	return a
}

func TestLoadTunableAllowlist_MissingFile(t *testing.T) {
	if _, err := LoadTunableAllowlist(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Fatal("expected error for missing config file")
	}
}

// --- the shipped allowlist is alias-scoped (S7, defect D3) -------------------

// shippedAllowlist parses the real policies/default.yaml. The file is the
// artifact that runs; a test over a hand-built key set would pass while the
// deployed config stayed wrong, which is precisely the failure D3 describes.
func shippedAllowlist(t *testing.T) *TunableAllowlist {
	t.Helper()
	path := filepath.Join("..", "..", "..", "..", "policies", "default.yaml")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("policies/default.yaml not reachable from the test: %v", err)
	}
	al, err := LoadTunableAllowlist(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return al
}

// Every allowlisted key must address the app through its umbrella alias. The
// umbrella has no root `replicaCount` or `resources` key, so a root-scoped
// entry validates a path the chart never reads — it renders nothing and errors
// nowhere.
func TestShippedAllowlist_EveryKeyIsAliasScoped(t *testing.T) {
	al := shippedAllowlist(t)

	sets := map[string][]string{"<default>": al.defaultKeys}
	for env, keys := range al.envKeys {
		sets[env] = keys
	}
	for env, keys := range sets {
		if len(keys) == 0 {
			t.Errorf("%s: no allowlisted keys at all", env)
		}
		for _, k := range keys {
			// `app.` is the umbrella's dependency ALIAS, which is the only prefix
			// that can be static here. A chart NAME cannot: the scaffolder rewrites
			// it, so `hex-scaffold.` (what S7 first shipped) matched the template
			// chart and no application built from it — and Helm discards values
			// addressed to a subchart key that does not exist, silently.
			if !strings.HasPrefix(k, "app.") {
				t.Errorf("%s: key %q is not scoped to the umbrella's subchart alias", env, k)
			}
			// Guard against re-introducing a chart-name prefix.
			if strings.HasPrefix(k, "hex-scaffold") {
				t.Errorf("%s: key %q is prefixed with a chart NAME; the scaffolder rewrites it", env, k)
			}
			// A bare alias entry would make the whole subchart tunable, including
			// the bind that decides which database's credentials the app reads.
			if k == "app" {
				t.Errorf("%s: a blanket %q entry makes every subchart value tunable", env, k)
			}
		}
	}
}

// The S7 DoD, against the shipped file: a legitimate development overlay is not
// blocked, and a platform-locked knob is. `blocked` is only ever true in
// enforce mode, so the verdict is computed in enforce here — the file itself
// ships `audit` deliberately, on two counts: the portal still sends root-scoped
// keys until S6, and an app that has not re-published since umbrella 0.4.0
// aliased its subchart still keys values after itself. Enforcing before both are
// resolved turns every legitimate overlay into a 422.
func TestShippedAllowlist_DoD_LegitimateOverlayVersusLockedKnob(t *testing.T) {
	shipped := shippedAllowlist(t)
	if shipped.Mode() != ModeAudit {
		t.Errorf("shipped mode = %q, want %q until slice S6 re-scopes the portal overlay",
			shipped.Mode(), ModeAudit)
	}

	enforcing, err := NewTunableAllowlist(ModeEnforce, shipped.defaultKeys, shipped.envKeys)
	if err != nil {
		t.Fatalf("rebuild in enforce mode: %v", err)
	}

	legit := map[string]any{
		"app": map[string]any{
			"resources": map[string]any{
				"requests": map[string]any{"cpu": "100m", "memory": "256Mi"},
				"limits":   map[string]any{"cpu": "500m", "memory": "512Mi"},
			},
			"autoscaling": map[string]any{"minReplicas": 2, "maxReplicas": 4},
		},
	}
	if dec := enforcing.Validate(context.Background(), legit, "development"); dec.Reject {
		t.Errorf("a legitimate development overlay must not be blocked: %v", dec.Violations)
	}

	locked := map[string]any{
		"app": map[string]any{"securityContext": map[string]any{"runAsNonRoot": false}},
	}
	dec := enforcing.Validate(context.Background(), locked, "development")
	if !dec.Reject {
		t.Fatal("securityContext.runAsNonRoot is guardrail G4 and must be blocked")
	}
	if len(dec.Violations) != 1 || dec.Violations[0] != "app.securityContext.runAsNonRoot" {
		t.Errorf("violations = %v, want exactly the alias-scoped locked key", dec.Violations)
	}
}

// The D3 detector. The portal still emits root-scoped keys until S6, and those
// overlays are silently discarded by Helm today. After the re-key they surface
// as violations — logged rather than blocked, because the file ships audit —
// which turns a silent failure into an observable one.
func TestShippedAllowlist_RootScopedOverlayIsReportedNotSilentlyAccepted(t *testing.T) {
	al := shippedAllowlist(t)

	dec := al.Validate(context.Background(),
		map[string]any{"resources": map[string]any{"requests": map[string]any{"cpu": "100m"}}},
		"development")

	if len(dec.Violations) == 0 {
		t.Fatal("a root-scoped overlay reaches no template; it must be reported, not accepted")
	}
	if dec.Violations[0] != "resources.requests.cpu" {
		t.Errorf("violations = %v, want the root-scoped path named verbatim", dec.Violations)
	}
	if dec.Reject {
		t.Error("audit mode must log this, not block it — the portal cannot be re-scoped until S6")
	}
}

// Production stays narrower than development, alias or not: only requests are
// tunable, so the platform keeps ownership of scaling.
func TestShippedAllowlist_ProductionStaysNarrowerThanDevelopment(t *testing.T) {
	al := shippedAllowlist(t)
	overlay := map[string]any{
		"app": map[string]any{
			"autoscaling": map[string]any{"maxReplicas": 20},
		},
	}
	if dec := al.Validate(context.Background(), overlay, "production"); len(dec.Violations) != 1 {
		t.Errorf("production violations = %v, want autoscaling reported as locked", dec.Violations)
	}
	if dec := al.Validate(context.Background(), overlay, "development"); len(dec.Violations) != 0 {
		t.Errorf("development violations = %v, want autoscaling tunable", dec.Violations)
	}
}

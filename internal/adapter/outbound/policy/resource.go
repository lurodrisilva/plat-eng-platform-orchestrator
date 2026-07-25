package policy

import (
	"context"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/myorg/platform-orchestrator/internal/application/port"
)

// resourceRules is one rule set in the config — the default set, or a single
// environment's set. A nil Postgres block means postgres is unconfigured, which
// with postgres in AllowedTypes is a misconfiguration rather than a permission:
// see resolve, which refuses rather than allowing an unbounded request.
type resourceRules struct {
	AllowedTypes []string       `yaml:"allowedTypes"`
	Postgres     *postgresRules `yaml:"postgres"`
}

type postgresRules struct {
	AllowedSizes              []string `yaml:"allowedSizes"`
	AllowedVersions           []string `yaml:"allowedVersions"`
	MaxStorageMb              int      `yaml:"maxStorageMb"`
	MaxInstancesPerDeployment int      `yaml:"maxInstancesPerDeployment"`
}

// resourcePolicyFile is the on-disk schema — the `resourcePolicy` section of
// the policies YAML (config.policies.configPath), alongside tunableAllowlist.
type resourcePolicyFile struct {
	ResourcePolicy *struct {
		Mode         string                   `yaml:"mode"`
		Default      resourceRules            `yaml:"default"`
		Environments map[string]resourceRules `yaml:"environments"`
	} `yaml:"resourcePolicy"`
}

// ResourcePolicy evaluates declared application dependencies (journey J3,
// ADR-0023) against per-environment rules.
//
// It is deny-by-default in two distinct ways, and both matter:
//
//   - A type absent from allowedTypes is refused. An environment listed with
//     `allowedTypes: []` therefore denies everything — that is how production
//     denies postgres. Presence of the environment key means "use this set",
//     never "this set is empty, fall back to the default"; conflating the two
//     would invert the production gate into a permission.
//   - A missing resourcePolicy section refuses every resource request. A
//     deployment that declares no resources is unaffected, so the fail-closed
//     posture costs nothing until someone asks the platform to spend money.
type ResourcePolicy struct {
	mode         string
	configured   bool
	defaultRules resourceRules
	envRules     map[string]resourceRules
}

// LoadResourcePolicy reads the resource policy from a policies YAML file. A
// file without a `resourcePolicy` section loads successfully but refuses every
// resource request — see the type comment.
func LoadResourcePolicy(path string) (*ResourcePolicy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read resource policy config: %w", err)
	}
	var f resourcePolicyFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse resource policy config: %w", err)
	}
	if f.ResourcePolicy == nil {
		return &ResourcePolicy{mode: ModeEnforce, configured: false}, nil
	}
	return NewResourcePolicy(f.ResourcePolicy.Mode, f.ResourcePolicy.Default, f.ResourcePolicy.Environments)
}

// NewResourcePolicy constructs an evaluator. An empty mode defaults to
// ENFORCE — deliberately the opposite of the tunable allowlist's audit-first
// default, because the thing being gated is spend rather than tuning. An
// unrecognised mode is an error.
func NewResourcePolicy(mode string, defaultRules resourceRules, envRules map[string]resourceRules) (*ResourcePolicy, error) {
	m := strings.ToLower(strings.TrimSpace(mode))
	if m == "" {
		m = ModeEnforce
	}
	if m != ModeAudit && m != ModeEnforce {
		return nil, fmt.Errorf("invalid resourcePolicy.mode %q (want %q|%q)", mode, ModeAudit, ModeEnforce)
	}
	envs := make(map[string]resourceRules, len(envRules))
	for env, r := range envRules {
		envs[strings.TrimSpace(env)] = r
	}
	return &ResourcePolicy{mode: m, configured: true, defaultRules: defaultRules, envRules: envs}, nil
}

// Mode returns the active enforcement mode.
func (p *ResourcePolicy) Mode() string { return p.mode }

// Configured reports whether a resourcePolicy section was present. Callers log
// this at startup so an unconfigured policy is visible before the first refusal
// rather than diagnosed from one.
func (p *ResourcePolicy) Configured() bool { return p.configured }

// rulesFor resolves the rule set for an environment. Membership decides, not
// content: an environment present with empty rules denies everything.
func (p *ResourcePolicy) rulesFor(environment string) resourceRules {
	if r, ok := p.envRules[environment]; ok {
		return r
	}
	return p.defaultRules
}

// Evaluate judges every declared resource against the environment's rules and
// returns all violations, not just the first — a caller fixing a request should
// see everything wrong with it in one round trip.
func (p *ResourcePolicy) Evaluate(_ context.Context, resources []port.ResourceRequest, environment string) port.ResourceDecision {
	if len(resources) == 0 {
		return port.ResourceDecision{Mode: p.mode}
	}

	if !p.configured {
		return port.ResourceDecision{
			Reject: p.mode == ModeEnforce,
			Mode:   p.mode,
			Violations: []string{
				"the platform has no resourcePolicy configured, so no application dependency can be authorized",
			},
		}
	}

	rules := p.rulesFor(environment)
	var violations []string
	counts := map[string]int{}

	for _, r := range resources {
		rType := strings.TrimSpace(r.Type)
		counts[rType]++

		if !contains(rules.AllowedTypes, rType) {
			violations = append(violations, fmt.Sprintf(
				"resource type %q is not allowed in environment %q (allowed: %s)",
				rType, environment, allowedList(rules.AllowedTypes)))
			continue
		}

		if rType == "postgres" {
			violations = append(violations, p.checkPostgres(r, rules.Postgres, environment)...)
		}
	}

	// Instance-count ceilings are per type across the whole request, so they are
	// checked once the loop has seen every resource.
	if rules.Postgres != nil && counts["postgres"] > 0 &&
		contains(rules.AllowedTypes, "postgres") &&
		rules.Postgres.MaxInstancesPerDeployment > 0 &&
		counts["postgres"] > rules.Postgres.MaxInstancesPerDeployment {
		violations = append(violations, fmt.Sprintf(
			"%d postgres instances requested but environment %q allows at most %d per deployment",
			counts["postgres"], environment, rules.Postgres.MaxInstancesPerDeployment))
	}

	return port.ResourceDecision{
		Reject:     p.mode == ModeEnforce && len(violations) > 0,
		Violations: violations,
		Mode:       p.mode,
	}
}

// checkPostgres judges one postgres request against the environment's postgres
// rules. Rules present but with a limit unset mean that limit is unenforced;
// rules ABSENT while postgres is an allowed type is a misconfiguration, and it
// refuses rather than granting an unbounded request.
func (p *ResourcePolicy) checkPostgres(r port.ResourceRequest, rules *postgresRules, environment string) []string {
	if rules == nil {
		return []string{fmt.Sprintf(
			"environment %q allows postgres but configures no postgres limits; refusing rather than provisioning unbounded",
			environment)}
	}

	var violations []string
	if len(rules.AllowedSizes) > 0 && !contains(rules.AllowedSizes, r.Size) {
		violations = append(violations, fmt.Sprintf(
			"postgres size %q is not allowed in environment %q (allowed: %s)",
			r.Size, environment, allowedList(rules.AllowedSizes)))
	}
	if len(rules.AllowedVersions) > 0 && !contains(rules.AllowedVersions, r.Version) {
		violations = append(violations, fmt.Sprintf(
			"postgres version %q is not allowed in environment %q (allowed: %s)",
			r.Version, environment, allowedList(rules.AllowedVersions)))
	}
	if rules.MaxStorageMb > 0 && r.StorageMb > rules.MaxStorageMb {
		violations = append(violations, fmt.Sprintf(
			"postgres storageMb %d exceeds the %d allowed in environment %q",
			r.StorageMb, rules.MaxStorageMb, environment))
	}
	return violations
}

// contains reports exact membership after trimming, so a stray space in the
// config does not silently create an unmatchable entry.
func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if strings.TrimSpace(h) == needle {
			return true
		}
	}
	return false
}

// allowedList renders a rule set for an error message, naming the empty case
// explicitly so a denial reads as a decision rather than a missing config.
func allowedList(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ", ")
}

var _ port.ResourcePolicyEvaluator = (*ResourcePolicy)(nil)

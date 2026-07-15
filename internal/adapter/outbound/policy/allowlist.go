// Package policy contains driven adapters that load declarative platform
// policy (branch/env rules, the J3 tunable allowlist) from configuration and
// evaluate it. It is the concrete home the ARCHITECTURE.md "PolicyEvaluator"
// gap points at (internal/adapter/outbound/policy/).
package policy

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/myorg/platform-orchestrator/internal/application/port"
)

// Enforcement modes for the tunable allowlist.
const (
	ModeAudit   = "audit"   // observe would-denies, never block (default)
	ModeEnforce = "enforce" // reject locked-knob overrides at the boundary
)

// allowlistFile is the on-disk schema — a section of the policies YAML
// (config.policies.configPath, e.g. policies/default.yaml).
type allowlistFile struct {
	TunableAllowlist struct {
		Mode        string   `yaml:"mode"`
		AllowedKeys []string `yaml:"allowedKeys"`
	} `yaml:"tunableAllowlist"`
}

// TunableAllowlist enforces the J3 tunable allowlist over a values overlay.
// It is chart-agnostic (slice-1): one global set of overridable value-key
// paths; everything else is platform-locked.
type TunableAllowlist struct {
	mode    string
	allowed []string // dotted allowlist key paths
}

// LoadTunableAllowlist reads the tunable-allowlist config from a policies YAML
// file. This is the orchestrator's first real policy loader — until now the
// policies configPath was declared but nothing parsed it.
func LoadTunableAllowlist(path string) (*TunableAllowlist, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read allowlist config: %w", err)
	}
	var f allowlistFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse allowlist config: %w", err)
	}
	return NewTunableAllowlist(f.TunableAllowlist.Mode, f.TunableAllowlist.AllowedKeys)
}

// NewTunableAllowlist constructs a validator from an explicit mode + allowlist.
// An empty mode defaults to audit (audit-first, per the guardrails rollout
// doctrine). An unrecognised mode is an error.
func NewTunableAllowlist(mode string, allowedKeys []string) (*TunableAllowlist, error) {
	m := strings.ToLower(strings.TrimSpace(mode))
	if m == "" {
		m = ModeAudit
	}
	if m != ModeAudit && m != ModeEnforce {
		return nil, fmt.Errorf("invalid tunableAllowlist.mode %q (want %q|%q)", mode, ModeAudit, ModeEnforce)
	}
	allowed := make([]string, 0, len(allowedKeys))
	for _, k := range allowedKeys {
		if k = strings.TrimSpace(k); k != "" {
			allowed = append(allowed, k)
		}
	}
	return &TunableAllowlist{mode: m, allowed: allowed}, nil
}

// Mode returns the active enforcement mode.
func (a *TunableAllowlist) Mode() string { return a.mode }

// Validate flattens the values overlay to leaf key paths and reports any that
// are not covered by the allowlist (platform-locked knobs). Reject is set only
// in enforce mode.
func (a *TunableAllowlist) Validate(_ context.Context, values map[string]any) port.AllowlistDecision {
	var violations []string
	for _, leaf := range flattenKeys(values) {
		if !a.covered(leaf) {
			violations = append(violations, leaf)
		}
	}
	sort.Strings(violations)
	return port.AllowlistDecision{
		Reject:     a.mode == ModeEnforce && len(violations) > 0,
		Violations: violations,
		Mode:       a.mode,
	}
}

// covered reports whether a leaf path is permitted: it equals an allowlist
// entry exactly, or is nested beneath one (the entry is a parent segment).
func (a *TunableAllowlist) covered(leaf string) bool {
	for _, entry := range a.allowed {
		if leaf == entry || strings.HasPrefix(leaf, entry+".") {
			return true
		}
	}
	return false
}

// flattenKeys returns the dotted paths of every leaf (scalar, slice, or empty
// map) in a nested values map. An empty map at a non-root path is itself a
// leaf, so an override like `securityContext: {}` is still seen and judged.
func flattenKeys(values map[string]any) []string {
	var out []string
	var walk func(prefix string, m map[string]any)
	walk = func(prefix string, m map[string]any) {
		if len(m) == 0 && prefix != "" {
			out = append(out, prefix)
			return
		}
		for k, v := range m {
			path := k
			if prefix != "" {
				path = prefix + "." + k
			}
			if child, ok := v.(map[string]any); ok {
				walk(path, child)
			} else {
				out = append(out, path)
			}
		}
	}
	walk("", values)
	return out
}

var _ port.TunableAllowlist = (*TunableAllowlist)(nil)

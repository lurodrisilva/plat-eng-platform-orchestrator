package port

import "context"

// AllowlistDecision is the outcome of validating a caller-supplied values
// overlay against the tunable allowlist (J3).
type AllowlistDecision struct {
	// Reject is true only when the validator is in enforce mode AND the
	// overlay overrides one or more platform-locked knobs. In audit mode it
	// is always false (observe, don't block).
	Reject bool
	// Violations are the dotted value-key paths the request tried to override
	// that are NOT on the tunable allowlist (i.e. platform-locked knobs).
	Violations []string
	// Mode is the active enforcement mode ("audit" | "enforce"), surfaced so
	// the use case can log audit-mode would-denies.
	Mode string
}

// TunableAllowlist validates the caller-supplied Helm values overlay,
// enforcing that only allowlisted knobs are overridden (journey J3,
// on-road custom-tuning). Deny-by-default: any leaf key not covered by the
// allowlist is a platform-locked knob. Governance stays server-side
// (ADR-0006); this is the API-boundary complement to Kyverno admission
// (ADR-0011).
type TunableAllowlist interface {
	Validate(ctx context.Context, values map[string]any) AllowlistDecision
}

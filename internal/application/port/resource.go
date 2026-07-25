package port

import "context"

// ResourceRequest is one declared application dependency, reduced to the facts
// policy judges. The name is deliberately absent: it is derived from the
// application id inside the domain and is not a policy input (ADR-0023).
type ResourceRequest struct {
	Type      string
	Size      string
	Version   string
	StorageMb int
}

// ResourceDecision is the outcome of evaluating declared dependencies against
// the resource policy.
type ResourceDecision struct {
	// Reject is true when the request is refused. Unlike AllowlistDecision this
	// is NOT gated on an enforce mode by default: a resource request creates
	// billed Azure infrastructure, so the policy defaults to enforce and an
	// audit mode has to be asked for explicitly.
	Reject bool
	// Violations describe, in caller-readable terms, why the request was
	// refused — one entry per offending resource or limit.
	Violations []string
	// Mode is the active enforcement mode ("enforce" | "audit").
	Mode string
}

// ResourcePolicyEvaluator judges declared application dependencies against the
// platform's resource policy, per target environment (journey J3).
//
// This is the governance counterpart to TunableAllowlist and is deliberately
// stricter. An unallowed TUNING value mistunes a workload and can be logged and
// allowed; an unallowed RESOURCE request spends money and creates real Azure
// infrastructure, so it is refused. Governance stays server-side (ADR-0006):
// the browser may not decide what a team may provision.
//
// An environment with an explicit entry in the policy uses it in full, even
// when that entry allows nothing — that is how production denies postgres
// (ADR-0010's dev/non-production gate, made executable). An environment with no
// entry falls back to the default rules.
type ResourcePolicyEvaluator interface {
	Evaluate(ctx context.Context, resources []ResourceRequest, environment string) ResourceDecision
}

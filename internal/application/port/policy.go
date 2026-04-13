package port

import "context"

// PolicyDecision represents the outcome of a policy evaluation.
type PolicyDecision struct {
	Allowed bool
	Reason  string
}

// PolicyEvaluator evaluates deployment policies.
type PolicyEvaluator interface {
	Evaluate(ctx context.Context, repo, gitRef, environment string) (PolicyDecision, error)
}

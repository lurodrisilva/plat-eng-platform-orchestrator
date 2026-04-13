package port

import "context"

// OIDCClaims holds validated claims from a GitHub Actions OIDC token.
type OIDCClaims struct {
	Repository      string
	RepositoryOwner string
	Ref             string
	SHA             string
	Actor           string
	Workflow        string
	JobWorkflowRef  string
	RunID           string
	RunAttempt      string
}

// TokenValidator validates OIDC bearer tokens.
type TokenValidator interface {
	Validate(ctx context.Context, token string) (OIDCClaims, error)
}

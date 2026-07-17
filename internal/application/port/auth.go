package port

import "context"

// OIDCClaims holds validated claims from an OIDC bearer token.
//
// The orchestrator authenticates two issuers over its lifetime (ADR-0015): a
// human via Microsoft Entra ID in v1, and GitHub Actions in the CI fast-follow.
// The two carry different claims, so this struct is the union and the Issuer
// field says which set is populated. Principal() collapses them to one subject
// string so callers do not branch on issuer.
type OIDCClaims struct {
	// Issuer is the verified `iss`; it discriminates which fields below are set.
	Issuer   string
	Subject  string // verified `sub`
	Audience string // the verified `aud` this token was minted for

	// --- Entra ID (v1) -----------------------------------------------------
	TenantID          string   // `tid`
	ObjectID          string   // `oid` — the stable per-tenant principal id
	PreferredUsername string   // `preferred_username` (usually the UPN/email)
	Name              string   // `name`
	Roles             []string // `roles` — app roles assigned in the app registration

	// --- GitHub Actions (CI fast-follow) -----------------------------------
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

// Principal returns a stable identifier for the authenticated caller,
// issuer-independent, for logging and attribution. Entra: the object id
// (falling back to the UPN, then the subject). GitHub: owner/repo. Empty only
// for a zero-value claims set.
func (c OIDCClaims) Principal() string {
	if c.Repository != "" {
		return c.Repository
	}
	if c.ObjectID != "" {
		return c.ObjectID
	}
	if c.PreferredUsername != "" {
		return c.PreferredUsername
	}
	return c.Subject
}

// TokenValidator validates OIDC bearer tokens.
type TokenValidator interface {
	Validate(ctx context.Context, token string) (OIDCClaims, error)
}

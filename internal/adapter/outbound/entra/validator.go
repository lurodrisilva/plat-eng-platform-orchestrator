// Package entra implements port.TokenValidator against Microsoft Entra ID
// (ADR-0015). It verifies an OIDC bearer token's signature against the tenant
// JWKS and its iss / aud / exp, then maps the standard Entra claims to
// port.OIDCClaims.
//
// It holds no credential. Verification is public-key only — the JWKS is fetched
// from the tenant's OIDC discovery document and cached by go-oidc. Nothing here
// mints a token or stores a secret.
package entra

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/myorg/platform-orchestrator/internal/application/port"
)

// Validator verifies Entra ID tokens. Construct it with New; the zero value is
// not usable.
type Validator struct {
	verifier *oidc.IDTokenVerifier
	issuer   string
	logger   *slog.Logger
}

// entraClaims mirrors the subset of the Entra v2.0 token payload we consume.
// go-oidc verifies the envelope (signature, iss, aud, exp); this only unpacks
// the already-trusted body.
type entraClaims struct {
	TenantID          string   `json:"tid"`
	ObjectID          string   `json:"oid"`
	PreferredUsername string   `json:"preferred_username"`
	Name              string   `json:"name"`
	Roles             []string `json:"roles"`
}

// New builds a Validator for one Entra tenant.
//
//	issuer   the tenant's token issuer, e.g.
//	         https://login.microsoftonline.com/<tenant-id>/v2.0
//	audience the orchestrator app registration's identifier (its client id or
//	         Application ID URI) — the aud every accepted token must carry.
//
// New performs network I/O: it fetches the issuer's OIDC discovery document to
// locate the JWKS. Call it once at startup. A failure here means the tenant is
// unreachable or misconfigured, and the service should refuse to start rather
// than accept unverifiable tokens.
func New(ctx context.Context, issuer, audience string, logger *slog.Logger) (*Validator, error) {
	if issuer == "" {
		return nil, errors.New("entra: issuer is required")
	}
	if audience == "" {
		// Refuse rather than default to SkipClientIDCheck — an unset audience
		// would accept any token minted for any app in the tenant.
		return nil, errors.New("entra: audience is required (would otherwise accept tokens for any app in the tenant)")
	}

	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("entra: discover issuer %q: %w", issuer, err)
	}

	verifier := provider.Verifier(&oidc.Config{
		ClientID: audience,
		// Pin RS256. Entra signs with RS256; naming the allowed algs explicitly
		// forecloses an alg-confusion downgrade rather than trusting whatever
		// the discovery document advertises.
		SupportedSigningAlgs: []string{oidc.RS256},
	})

	return &Validator{verifier: verifier, issuer: issuer, logger: logger}, nil
}

// Validate verifies the token and maps its claims. A returned error means the
// token is not to be trusted; the caller must answer 401 and must not read the
// claims.
func (v *Validator) Validate(ctx context.Context, rawToken string) (port.OIDCClaims, error) {
	idToken, err := v.verifier.Verify(ctx, rawToken)
	if err != nil {
		// Deliberately not wrapping the library message into the HTTP response
		// upstream — a verification failure should read as "unauthenticated",
		// not leak which check failed. Logged at the handler.
		return port.OIDCClaims{}, fmt.Errorf("entra: verify token: %w", err)
	}

	var ec entraClaims
	if err := idToken.Claims(&ec); err != nil {
		return port.OIDCClaims{}, fmt.Errorf("entra: decode claims: %w", err)
	}

	var audience string
	if len(idToken.Audience) > 0 {
		audience = idToken.Audience[0]
	}

	return port.OIDCClaims{
		Issuer:            idToken.Issuer,
		Subject:           idToken.Subject,
		Audience:          audience,
		TenantID:          ec.TenantID,
		ObjectID:          ec.ObjectID,
		PreferredUsername: ec.PreferredUsername,
		Name:              ec.Name,
		Roles:             ec.Roles,
	}, nil
}

// compile-time proof the adapter satisfies the port.
var _ port.TokenValidator = (*Validator)(nil)

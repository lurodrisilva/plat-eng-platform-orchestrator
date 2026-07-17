package entra

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

// oidcTestProvider stands in for an Entra tenant: it serves an OIDC discovery
// document and a JWKS, and mints tokens signed with the matching private key.
type oidcTestProvider struct {
	server *httptest.Server
	key    *rsa.PrivateKey
	kid    string
	issuer string
}

func newOIDCTestProvider(t *testing.T) *oidcTestProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	p := &oidcTestProvider{key: key, kid: "test-key-1"}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                p.issuer,
			"jwks_uri":                              p.issuer + "/keys",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		jwks := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
			Key:       key.Public(),
			KeyID:     p.kid,
			Algorithm: "RS256",
			Use:       "sig",
		}}}
		_ = json.NewEncoder(w).Encode(jwks)
	})

	p.server = httptest.NewServer(mux)
	p.issuer = p.server.URL // discovery `issuer` must equal the URL go-oidc was given
	t.Cleanup(p.server.Close)
	return p
}

// mint signs a JWT with the given claims. A nil override leaves the default
// valid claim set intact; fields set on override replace the defaults.
func (p *oidcTestProvider) mint(t *testing.T, claims map[string]any) string {
	t.Helper()
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: p.key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", p.kid),
	)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	obj, err := signer.Sign(payload)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	tok, err := obj.CompactSerialize()
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	return tok
}

// validClaims returns a fresh, fully-valid Entra-shaped claim set for the given
// provider and audience. Tests mutate a copy to exercise each failure.
func validClaims(iss, aud string) map[string]any {
	now := time.Now()
	return map[string]any{
		"iss":                iss,
		"aud":                aud,
		"sub":                "sub-abc",
		"exp":                now.Add(1 * time.Hour).Unix(),
		"iat":                now.Add(-1 * time.Minute).Unix(),
		"nbf":                now.Add(-1 * time.Minute).Unix(),
		"tid":                "tenant-xyz",
		"oid":                "object-123",
		"preferred_username": "dev@example.com",
		"name":               "Dev Eloper",
		"roles":              []string{"Deployer"},
	}
}

const testAudience = "api://orchestrator"

func newValidatorForTest(t *testing.T, p *oidcTestProvider, aud string) *Validator {
	t.Helper()
	v, err := New(context.Background(), p.issuer, aud, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return v
}

func TestValidate_ValidToken_MapsClaims(t *testing.T) {
	p := newOIDCTestProvider(t)
	v := newValidatorForTest(t, p, testAudience)

	tok := p.mint(t, validClaims(p.issuer, testAudience))
	claims, err := v.Validate(context.Background(), tok)
	if err != nil {
		t.Fatalf("Validate: unexpected error: %v", err)
	}

	if claims.Subject != "sub-abc" {
		t.Errorf("Subject = %q, want sub-abc", claims.Subject)
	}
	if claims.TenantID != "tenant-xyz" {
		t.Errorf("TenantID = %q, want tenant-xyz", claims.TenantID)
	}
	if claims.ObjectID != "object-123" {
		t.Errorf("ObjectID = %q, want object-123", claims.ObjectID)
	}
	if claims.PreferredUsername != "dev@example.com" {
		t.Errorf("PreferredUsername = %q", claims.PreferredUsername)
	}
	if claims.Audience != testAudience {
		t.Errorf("Audience = %q, want %s", claims.Audience, testAudience)
	}
	if len(claims.Roles) != 1 || claims.Roles[0] != "Deployer" {
		t.Errorf("Roles = %v, want [Deployer]", claims.Roles)
	}
	// Principal prefers the object id for an Entra token.
	if claims.Principal() != "object-123" {
		t.Errorf("Principal() = %q, want object-123", claims.Principal())
	}
}

func TestValidate_WrongAudience_Rejected(t *testing.T) {
	p := newOIDCTestProvider(t)
	v := newValidatorForTest(t, p, testAudience)

	// Token minted for a different app registration.
	tok := p.mint(t, validClaims(p.issuer, "api://someone-else"))
	if _, err := v.Validate(context.Background(), tok); err == nil {
		t.Fatal("expected error for wrong audience, got nil")
	}
}

func TestValidate_Expired_Rejected(t *testing.T) {
	p := newOIDCTestProvider(t)
	v := newValidatorForTest(t, p, testAudience)

	c := validClaims(p.issuer, testAudience)
	c["exp"] = time.Now().Add(-1 * time.Hour).Unix()
	tok := p.mint(t, c)
	if _, err := v.Validate(context.Background(), tok); err == nil {
		t.Fatal("expected error for expired token, got nil")
	}
}

func TestValidate_WrongIssuer_Rejected(t *testing.T) {
	p := newOIDCTestProvider(t)
	v := newValidatorForTest(t, p, testAudience)

	c := validClaims("https://evil.example.com/v2.0", testAudience)
	tok := p.mint(t, c)
	if _, err := v.Validate(context.Background(), tok); err == nil {
		t.Fatal("expected error for issuer mismatch, got nil")
	}
}

func TestValidate_BadSignature_Rejected(t *testing.T) {
	p := newOIDCTestProvider(t)
	v := newValidatorForTest(t, p, testAudience)

	// A token signed by a DIFFERENT key than the one in the JWKS.
	other := newOIDCTestProvider(t)
	other.issuer = p.issuer // same claims, foreign signature
	tok := other.mint(t, validClaims(p.issuer, testAudience))
	if _, err := v.Validate(context.Background(), tok); err == nil {
		t.Fatal("expected error for bad signature, got nil")
	}
}

func TestNew_RequiresIssuerAndAudience(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := New(context.Background(), "", testAudience, log); err == nil {
		t.Error("expected error for empty issuer, got nil")
	}
	p := newOIDCTestProvider(t)
	if _, err := New(context.Background(), p.issuer, "", log); err == nil {
		t.Error("expected error for empty audience (would accept any app's tokens), got nil")
	}
}

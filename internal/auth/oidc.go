package auth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/myorg/platform-orchestrator/internal/config"
)

// Claims represents the validated claims from a GitHub Actions OIDC token.
type Claims struct {
	Repository        string `json:"repository"`
	RepositoryOwner   string `json:"repository_owner"`
	Ref               string `json:"ref"`
	SHA               string `json:"sha"`
	Actor             string `json:"actor"`
	Workflow          string `json:"workflow"`
	JobWorkflowRef    string `json:"job_workflow_ref"`
	RunnerEnvironment string `json:"runner_environment"`
	RunID             string `json:"run_id"`
	RunAttempt        string `json:"run_attempt"`
	jwt.RegisteredClaims
}

// Validator validates GitHub Actions OIDC JWTs.
type Validator struct {
	issuer       string
	audience     string
	allowedRepos []string
	jwksURL      string
	keys         map[string]*rsa.PublicKey
	keysMu       sync.RWMutex
	keysExpiry   time.Time
	cacheTTL     time.Duration
	httpClient   *http.Client
}

func NewValidator(cfg config.AuthConfig) *Validator {
	return &Validator{
		issuer:       cfg.OIDC.Issuer,
		audience:     cfg.OIDC.Audience,
		allowedRepos: cfg.AllowedRepositories,
		jwksURL:      cfg.OIDC.Issuer + "/.well-known/jwks",
		keys:         make(map[string]*rsa.PublicKey),
		cacheTTL:     cfg.OIDC.JWKSCacheTTL,
		httpClient:   &http.Client{Timeout: 10 * time.Second},
	}
}

// ValidateToken validates a GitHub OIDC JWT and returns its claims.
func (v *Validator) ValidateToken(ctx context.Context, tokenString string) (*Claims, error) {
	if err := v.refreshKeysIfNeeded(ctx); err != nil {
		return nil, fmt.Errorf("refreshing JWKS: %w", err)
	}

	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, v.keyFunc,
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
		jwt.WithLeeway(30*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("parsing token: %w", err)
	}
	if !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}

	if err := v.validateClaims(claims); err != nil {
		return nil, err
	}

	return claims, nil
}

func (v *Validator) keyFunc(token *jwt.Token) (any, error) {
	if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
		return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
	}
	kid, ok := token.Header["kid"].(string)
	if !ok {
		return nil, fmt.Errorf("missing kid in token header")
	}
	v.keysMu.RLock()
	key, exists := v.keys[kid]
	v.keysMu.RUnlock()
	if !exists {
		return nil, fmt.Errorf("unknown key ID: %s", kid)
	}
	return key, nil
}

func (v *Validator) validateClaims(claims *Claims) error {
	if len(v.allowedRepos) > 0 {
		allowed := false
		for _, repo := range v.allowedRepos {
			if claims.Repository == repo {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("repository %q not in allowlist", claims.Repository)
		}
	}
	return nil
}

func (v *Validator) refreshKeysIfNeeded(ctx context.Context) error {
	v.keysMu.RLock()
	if time.Now().Before(v.keysExpiry) && len(v.keys) > 0 {
		v.keysMu.RUnlock()
		return nil
	}
	v.keysMu.RUnlock()

	return v.fetchKeys(ctx)
}

func (v *Validator) fetchKeys(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.jwksURL, nil)
	if err != nil {
		return err
	}
	resp, err := v.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetching JWKS: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("JWKS endpoint returned %d", resp.StatusCode)
	}

	var jwks struct {
		Keys []jwkKey `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return fmt.Errorf("decoding JWKS: %w", err)
	}

	newKeys := make(map[string]*rsa.PublicKey, len(jwks.Keys))
	for _, k := range jwks.Keys {
		if k.Kty != "RSA" || k.Use != "sig" {
			continue
		}
		pub, err := parseRSAPublicKey(k)
		if err != nil {
			continue
		}
		newKeys[k.Kid] = pub
	}

	v.keysMu.Lock()
	v.keys = newKeys
	v.keysExpiry = time.Now().Add(v.cacheTTL)
	v.keysMu.Unlock()

	return nil
}

type jwkKey struct {
	Kty string `json:"kty"`
	Use string `json:"use"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
}

func parseRSAPublicKey(k jwkKey) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, err
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, err
	}

	n := new(big.Int).SetBytes(nBytes)
	e := int(new(big.Int).SetBytes(eBytes).Int64())

	return &rsa.PublicKey{N: n, E: e}, nil
}

// ExtractBearerToken extracts the token from an Authorization header.
func ExtractBearerToken(header string) (string, error) {
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return "", fmt.Errorf("invalid authorization header format")
	}
	return parts[1], nil
}

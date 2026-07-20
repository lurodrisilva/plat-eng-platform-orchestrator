package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// AppAuth mints GitHub App installation access tokens.
//
// It holds the App's private key and exchanges a short-lived RS256 App JWT for
// an installation token via the GitHub API. It holds NO other credential — no
// PAT, no OAuth token. The installation token is cached in memory until shortly
// before it expires and re-minted on demand.
type AppAuth struct {
	appID          int64
	installationID int64
	privateKeyPEM  string
	httpClient     *http.Client
	baseURL        string

	mu       sync.Mutex
	token    string
	tokenExp time.Time
}

// installationTokenResponse is the shape of POST .../access_tokens.
type installationTokenResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// appJWT builds and signs the short-lived App JWT used to authenticate as the
// GitHub App itself (not the installation). iss is the App ID as a string, iat
// is backdated 60s to tolerate clock skew, exp is 9 minutes out (under the
// 10-minute ceiling GitHub enforces).
func (a *AppAuth) appJWT(now time.Time) (string, error) {
	key, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(a.privateKeyPEM))
	if err != nil {
		return "", fmt.Errorf("parse app private key: %w", err)
	}
	claims := jwt.RegisteredClaims{
		Issuer:    fmt.Sprintf("%d", a.appID),
		IssuedAt:  jwt.NewNumericDate(now.Add(-60 * time.Second)),
		ExpiresAt: jwt.NewNumericDate(now.Add(9 * time.Minute)),
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(key)
	if err != nil {
		return "", fmt.Errorf("sign app jwt: %w", err)
	}
	return signed, nil
}

// installationToken returns a valid installation access token, minting a fresh
// one when the cache is empty or within ~1 minute of expiry. Best-effort cache:
// a mutex plus an expiry timestamp, no background refresh.
func (a *AppAuth) installationToken(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := time.Now()
	if a.token != "" && now.Before(a.tokenExp.Add(-1*time.Minute)) {
		return a.token, nil
	}

	appJWT, err := a.appJWT(now)
	if err != nil {
		return "", err
	}

	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", a.baseURL, a.installationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", fmt.Errorf("create installation token request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+appJWT)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("mint installation token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read installation token response: %w", err)
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("installation token returned %d: %s", resp.StatusCode, string(body))
	}

	var out installationTokenResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("decode installation token: %w", err)
	}
	if out.Token == "" {
		return "", fmt.Errorf("installation token response had empty token")
	}

	a.token = out.Token
	// GitHub installation tokens live ~1h; fall back to a conservative expiry if
	// the response omits expires_at so the cache still turns over.
	if out.ExpiresAt.IsZero() {
		a.tokenExp = now.Add(50 * time.Minute)
	} else {
		a.tokenExp = out.ExpiresAt
	}
	return a.token, nil
}

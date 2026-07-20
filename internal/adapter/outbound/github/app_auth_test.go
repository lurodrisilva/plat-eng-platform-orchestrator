package github

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// testPrivateKeyPEM generates a throwaway RSA key and returns it PEM-encoded
// (PKCS#1) alongside the key, so a test can verify the JWT the adapter signs.
func testPrivateKeyPEM(t *testing.T) (string, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	return string(pemBytes), key
}

func newTestAppAuth(t *testing.T, baseURL, keyPEM string) *AppAuth {
	t.Helper()
	return &AppAuth{
		appID:          12345,
		installationID: 67890,
		privateKeyPEM:  keyPEM,
		httpClient:     &http.Client{Timeout: 5 * time.Second},
		baseURL:        baseURL,
	}
}

func TestInstallationToken_MintsAndSendsJWT(t *testing.T) {
	keyPEM, key := testPrivateKeyPEM(t)

	var gotAuth, gotAccept, gotAPIVersion, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		gotAPIVersion = r.Header.Get("X-GitHub-Api-Version")
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "ghs_installationtoken",
			"expires_at": time.Now().Add(1 * time.Hour),
		})
	}))
	defer srv.Close()

	a := newTestAppAuth(t, srv.URL, keyPEM)
	tok, err := a.installationToken(context.Background())
	if err != nil {
		t.Fatalf("installationToken: %v", err)
	}
	if tok != "ghs_installationtoken" {
		t.Errorf("token = %q, want ghs_installationtoken", tok)
	}
	if gotPath != "/app/installations/67890/access_tokens" {
		t.Errorf("path = %q, want /app/installations/67890/access_tokens", gotPath)
	}
	if gotAccept != "application/vnd.github+json" {
		t.Errorf("Accept = %q", gotAccept)
	}
	if gotAPIVersion != "2022-11-28" {
		t.Errorf("X-GitHub-Api-Version = %q", gotAPIVersion)
	}

	// The Authorization header must carry a Bearer App JWT with iss = appID,
	// verifiable against the matching public key.
	if !strings.HasPrefix(gotAuth, "Bearer ") {
		t.Fatalf("Authorization = %q, want Bearer <jwt>", gotAuth)
	}
	raw := strings.TrimPrefix(gotAuth, "Bearer ")
	parsed, err := jwt.Parse(raw, func(tok *jwt.Token) (any, error) {
		if _, ok := tok.Method.(*jwt.SigningMethodRSA); !ok {
			t.Errorf("signing method = %v, want RSA", tok.Header["alg"])
		}
		return &key.PublicKey, nil
	})
	if err != nil {
		t.Fatalf("parse app jwt: %v", err)
	}
	iss, err := parsed.Claims.GetIssuer()
	if err != nil {
		t.Fatalf("get issuer: %v", err)
	}
	if iss != "12345" {
		t.Errorf("iss = %q, want 12345 (app id as string)", iss)
	}
}

func TestInstallationToken_CachesUntilExpiry(t *testing.T) {
	keyPEM, _ := testPrivateKeyPEM(t)

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "ghs_cached",
			"expires_at": time.Now().Add(1 * time.Hour),
		})
	}))
	defer srv.Close()

	a := newTestAppAuth(t, srv.URL, keyPEM)
	for i := 0; i < 3; i++ {
		if _, err := a.installationToken(context.Background()); err != nil {
			t.Fatalf("installationToken #%d: %v", i, err)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("server calls = %d, want 1 (token should be cached)", got)
	}
}

func TestInstallationToken_ReMintsWhenExpired(t *testing.T) {
	keyPEM, _ := testPrivateKeyPEM(t)

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusCreated)
		// Expires in the past → cache is never fresh, so each call re-mints.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "ghs_stale",
			"expires_at": time.Now().Add(-1 * time.Hour),
		})
	}))
	defer srv.Close()

	a := newTestAppAuth(t, srv.URL, keyPEM)
	for i := 0; i < 2; i++ {
		if _, err := a.installationToken(context.Background()); err != nil {
			t.Fatalf("installationToken #%d: %v", i, err)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("server calls = %d, want 2 (expired token must re-mint)", got)
	}
}

func TestInstallationToken_ErrorStatus(t *testing.T) {
	keyPEM, _ := testPrivateKeyPEM(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"bad creds"}`))
	}))
	defer srv.Close()

	a := newTestAppAuth(t, srv.URL, keyPEM)
	if _, err := a.installationToken(context.Background()); err == nil {
		t.Fatal("expected error for 401 installation token response, got nil")
	}
}

func TestInstallationToken_BadPEM(t *testing.T) {
	a := newTestAppAuth(t, "https://api.github.com", "not a pem")
	if _, err := a.installationToken(context.Background()); err == nil {
		t.Fatal("expected error for malformed private key PEM, got nil")
	}
}

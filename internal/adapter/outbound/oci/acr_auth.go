package oci

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// CredentialFunc supplies registry basic-auth credentials for a push. Returning
// an error aborts the push before any bytes are sent.
type CredentialFunc func(ctx context.Context) (username, password string, err error)

// ACR token exchange constants. The registry accepts an AAD access token scoped
// to the ACR resource and returns an ACR refresh token; docker/helm basic auth
// then carries that refresh token as the password under a well-known null GUID
// username (the ACR convention for token-based auth).
const (
	acrTokenScope   = "https://containerregistry.azure.net/.default"
	acrExchangeUser = "00000000-0000-0000-0000-000000000000"
)

// ACRWorkloadIdentityCredential returns a CredentialFunc that authenticates to
// an Azure Container Registry with workload identity — no stored credential
// (ADR-0021 gate 3, ADR-0020 §3). It acquires an AAD token via the ambient
// federated credential (azidentity reads AZURE_CLIENT_ID / AZURE_TENANT_ID /
// AZURE_FEDERATED_TOKEN_FILE, injected by the workload-identity webhook),
// exchanges it for an ACR refresh token, and returns it as the basic-auth
// password. Credentials are acquired per push, so token expiry is a non-issue.
func ACRWorkloadIdentityCredential(registryHost string) CredentialFunc {
	return func(ctx context.Context) (string, string, error) {
		cred, err := azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			return "", "", fmt.Errorf("default azure credential: %w", err)
		}
		tok, err := cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{acrTokenScope}})
		if err != nil {
			return "", "", fmt.Errorf("acquire AAD token for ACR: %w", err)
		}
		refresh, err := exchangeACRRefreshToken(ctx, registryHost, tok.Token)
		if err != nil {
			return "", "", err
		}
		return acrExchangeUser, refresh, nil
	}
}

// exchangeACRRefreshToken trades an AAD access token for an ACR refresh token
// via the registry's /oauth2/exchange endpoint.
func exchangeACRRefreshToken(ctx context.Context, registryHost, aadToken string) (string, error) {
	tenant := os.Getenv("AZURE_TENANT_ID")
	if tenant == "" {
		return "", fmt.Errorf("AZURE_TENANT_ID is not set (workload-identity env missing); cannot exchange ACR token")
	}
	form := url.Values{
		"grant_type":   {"access_token"},
		"service":      {registryHost},
		"tenant":       {tenant},
		"access_token": {aadToken},
	}
	endpoint := fmt.Sprintf("https://%s/oauth2/exchange", registryHost)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("build ACR exchange request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", fmt.Errorf("ACR token exchange: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("ACR token exchange: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var out struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode ACR exchange response: %w", err)
	}
	if out.RefreshToken == "" {
		return "", fmt.Errorf("ACR token exchange returned an empty refresh_token")
	}
	return out.RefreshToken, nil
}

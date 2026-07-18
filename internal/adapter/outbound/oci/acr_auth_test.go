package oci

import (
	"context"
	"strings"
	"testing"
)

func TestACRWorkloadIdentityCredential_ReturnsFunc(t *testing.T) {
	if ACRWorkloadIdentityCredential("acr.example.azurecr.io") == nil {
		t.Fatal("expected a non-nil CredentialFunc")
	}
}

func TestExchangeACRRefreshToken_MissingTenant(t *testing.T) {
	t.Setenv("AZURE_TENANT_ID", "")
	_, err := exchangeACRRefreshToken(context.Background(), "acr.example.azurecr.io", "aad-token")
	if err == nil {
		t.Fatal("expected error when AZURE_TENANT_ID is unset")
	}
	if !strings.Contains(err.Error(), "AZURE_TENANT_ID") {
		t.Errorf("error should name the missing env var, got: %v", err)
	}
}

func TestWithCredential_SetsCredential(t *testing.T) {
	called := false
	cred := func(ctx context.Context) (string, string, error) {
		called = true
		return "u", "p", nil
	}
	p := NewPublisher("acr.example.azurecr.io", "", nil, WithCredential(cred))
	if p.credential == nil {
		t.Fatal("WithCredential did not set the credential func")
	}
	_, _, _ = p.credential(context.Background())
	if !called {
		t.Error("credential func was not the one provided")
	}
}

func TestNewPublisher_NoCredentialByDefault(t *testing.T) {
	p := NewPublisher("ghcr.io", "", nil)
	if p.credential != nil {
		t.Error("publisher must be anonymous (nil credential) without WithCredential")
	}
}

package domain

import (
	"strings"
	"testing"
	"time"
)

func TestGenerateDeploymentMetadata(t *testing.T) {
	req := DeploymentRequest{
		Application: ApplicationInfo{ID: "payment-service", Team: "payments"},
		Target:      TargetInfo{Environment: "production", Cluster: "aks-prod", Namespace: "payments"},
		Source:      SourceInfo{GitSHA: "abc123def456789012345678901234567890abcd", Actor: "johndoe", GitHubRunID: "12345"},
	}
	chartVersion := "1.5.2"
	now := time.Date(2026, 4, 12, 10, 30, 0, 0, time.UTC)

	meta := GenerateDeploymentMetadata(req, chartVersion, now)

	// Deployment ID
	if !strings.HasPrefix(meta.DeploymentID, "dep-20260412-payment-service-prod-abc123d") {
		t.Errorf("unexpected deploymentId: %s", meta.DeploymentID)
	}

	// Component ID
	if meta.ComponentID != "payment-service-production-aks-prod-abc123d" {
		t.Errorf("unexpected componentId: %s", meta.ComponentID)
	}

	// Deployment version
	if meta.DeploymentVersion != "1.5.2-20260412103000-abc123d" {
		t.Errorf("unexpected deploymentVersion: %s", meta.DeploymentVersion)
	}

	// Argo app name
	if meta.ArgoAppName != "payment-service-production-payments" {
		t.Errorf("unexpected argoAppName: %s", meta.ArgoAppName)
	}

	// Short SHA
	if meta.ShortSHA != "abc123d" {
		t.Errorf("unexpected shortSha: %s", meta.ShortSHA)
	}

	// Labels
	if meta.Labels["platform.example.com/application"] != "payment-service" {
		t.Error("missing application label")
	}
	if meta.Labels["platform.example.com/managed-by"] != "platform-orchestrator" {
		t.Error("missing managed-by label")
	}
}

func TestGenerateDeploymentMetadata_DifferentSHAProducesDifferentVersions(t *testing.T) {
	req1 := DeploymentRequest{
		Application: ApplicationInfo{ID: "app1", Team: "team1"},
		Target:      TargetInfo{Environment: "staging", Cluster: "c1", Namespace: "ns1"},
		Source:      SourceInfo{GitSHA: "1111111222222233333334444444555555566666"},
	}
	req2 := DeploymentRequest{
		Application: ApplicationInfo{ID: "app1", Team: "team1"},
		Target:      TargetInfo{Environment: "staging", Cluster: "c1", Namespace: "ns1"},
		Source:      SourceInfo{GitSHA: "aaaaaaa2222222333333344444445555555666666"},
	}

	now := time.Now()
	meta1 := GenerateDeploymentMetadata(req1, "1.0.0", now)
	meta2 := GenerateDeploymentMetadata(req2, "1.0.0", now)

	if meta1.DeploymentVersion == meta2.DeploymentVersion {
		t.Error("different SHAs should produce different deployment versions")
	}
}

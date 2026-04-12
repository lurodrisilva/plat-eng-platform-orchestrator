package domain

import (
	"fmt"
	"time"
)

// GenerateDeploymentMetadata creates deterministic deployment metadata from a request.
func GenerateDeploymentMetadata(req DeploymentRequest, chartVersion string, now time.Time) DeploymentMetadata {
	shortSHA := req.Source.GitSHA[:7]
	ts := now.UTC().Format("20060102150405")

	deploymentID := fmt.Sprintf("dep-%s-%s-%s-%s",
		now.UTC().Format("20060102"),
		req.Application.ID,
		req.Target.Environment[:4],
		shortSHA,
	)

	componentID := fmt.Sprintf("%s-%s-%s-%s",
		req.Application.ID,
		req.Target.Environment,
		req.Target.Cluster,
		shortSHA,
	)

	deploymentVersion := fmt.Sprintf("%s-%s-%s", chartVersion, ts, shortSHA)

	argoAppName := fmt.Sprintf("%s-%s-%s",
		req.Application.ID,
		req.Target.Environment,
		req.Target.Namespace,
	)

	labels := map[string]string{
		"platform.example.com/application":   req.Application.ID,
		"platform.example.com/team":          req.Application.Team,
		"platform.example.com/environment":   req.Target.Environment,
		"platform.example.com/cluster":       req.Target.Cluster,
		"platform.example.com/namespace":     req.Target.Namespace,
		"platform.example.com/deployment-id": deploymentID,
		"platform.example.com/git-sha":       shortSHA,
		"platform.example.com/managed-by":    "platform-orchestrator",
	}

	return DeploymentMetadata{
		DeploymentID:      deploymentID,
		ComponentID:       componentID,
		DeploymentVersion: deploymentVersion,
		ArgoAppName:       argoAppName,
		ShortSHA:          shortSHA,
		Timestamp:         now,
		Labels:            labels,
	}
}

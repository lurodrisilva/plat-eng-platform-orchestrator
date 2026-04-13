package port

import "context"

// PublishedArtifact holds OCI artifact publication details.
type PublishedArtifact struct {
	OCIReference string
	Digest       string
	Registry     string
	Repository   string
	Tag          string
}

// ArtifactPublisher publishes Helm charts to an OCI registry.
type ArtifactPublisher interface {
	Publish(ctx context.Context, chartBytes []byte, appID, chartName, version string) (PublishedArtifact, error)
}

package oci

import (
	"context"
	"fmt"
	"log/slog"

	"helm.sh/helm/v3/pkg/registry"

	"github.com/myorg/platform-orchestrator/internal/config"
)

// Client publishes Helm charts to OCI registries.
type Client struct {
	registry         string
	repositoryPrefix string
	logger           *slog.Logger
}

func NewClient(cfg config.OCIConfig, logger *slog.Logger) *Client {
	return &Client{
		registry:         cfg.Registry,
		repositoryPrefix: cfg.RepositoryPrefix,
		logger:           logger,
	}
}

// Push publishes a packaged chart to the OCI registry.
func (c *Client) Push(ctx context.Context, chartBytes []byte, appID, chartName, version string) (reference string, digest string, err error) {
	registryClient, err := registry.NewClient()
	if err != nil {
		return "", "", fmt.Errorf("creating registry client: %w", err)
	}

	ref := c.buildReference(appID, chartName, version)

	result, err := registryClient.Push(chartBytes, ref)
	if err != nil {
		return "", "", fmt.Errorf("pushing chart to %s: %w", ref, err)
	}

	c.logger.InfoContext(ctx, "published OCI artifact",
		slog.String("reference", ref),
		slog.String("digest", result.Manifest.Digest),
	)

	return ref, result.Manifest.Digest, nil
}

// Verify checks that an artifact exists at the given reference and has the expected digest.
func (c *Client) Verify(ctx context.Context, ref, expectedDigest string) error {
	registryClient, err := registry.NewClient()
	if err != nil {
		return fmt.Errorf("creating registry client: %w", err)
	}

	result, err := registryClient.Pull(ref)
	if err != nil {
		return fmt.Errorf("pulling manifest for verification: %w", err)
	}

	if result.Manifest.Digest != expectedDigest {
		return fmt.Errorf("digest mismatch: expected %s, got %s", expectedDigest, result.Manifest.Digest)
	}

	c.logger.DebugContext(ctx, "verified OCI artifact",
		slog.String("reference", ref),
		slog.String("digest", expectedDigest),
	)

	return nil
}

// BuildReference constructs the full OCI reference for a chart.
func (c *Client) BuildReference(appID, chartName, version string) string {
	return c.buildReference(appID, chartName, version)
}

func (c *Client) buildReference(appID, chartName, version string) string {
	base := fmt.Sprintf("oci://%s", c.registry)
	if c.repositoryPrefix != "" {
		base = fmt.Sprintf("%s/%s", base, c.repositoryPrefix)
	}
	return fmt.Sprintf("%s/%s/charts/%s:%s", base, appID, chartName, version)
}

// RegistryHost returns the configured registry hostname.
func (c *Client) RegistryHost() string {
	return c.registry
}


package oci

import (
	"context"
	"fmt"
	"log/slog"

	"helm.sh/helm/v3/pkg/registry"

	"github.com/myorg/platform-orchestrator/internal/application/port"
)

// Publisher implements port.ArtifactPublisher using an OCI registry.
type Publisher struct {
	registryHost     string
	repositoryPrefix string
	credential       CredentialFunc // optional; nil = anonymous (no login)
	logger           *slog.Logger
}

// Option configures a Publisher.
type Option func(*Publisher)

// WithCredential makes the publisher log in to the registry before each push
// using the supplied credential source (e.g. ACR workload identity). Without it
// the publisher pushes anonymously (public registries, tests).
func WithCredential(c CredentialFunc) Option {
	return func(p *Publisher) { p.credential = c }
}

// NewPublisher creates an OCI artifact publisher.
func NewPublisher(registryHost, repoPrefix string, logger *slog.Logger, opts ...Option) *Publisher {
	p := &Publisher{
		registryHost:     registryHost,
		repositoryPrefix: repoPrefix,
		logger:           logger,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

func (p *Publisher) Publish(ctx context.Context, chartBytes []byte, appID, chartName, version string) (port.PublishedArtifact, error) {
	client, err := registry.NewClient()
	if err != nil {
		return port.PublishedArtifact{}, fmt.Errorf("create registry client: %w", err)
	}

	// Log in when a credential source is configured (secretless ACR push via
	// workload identity — ADR-0021 gate 3). Anonymous otherwise.
	if p.credential != nil {
		user, pass, err := p.credential(ctx)
		if err != nil {
			return port.PublishedArtifact{}, fmt.Errorf("acquire registry credential: %w", err)
		}
		if err := client.Login(p.registryHost, registry.LoginOptBasicAuth(user, pass)); err != nil {
			return port.PublishedArtifact{}, fmt.Errorf("registry login to %s: %w", p.registryHost, err)
		}
	}

	ref := p.buildRef(appID, chartName, version)

	result, err := client.Push(chartBytes, ref)
	if err != nil {
		return port.PublishedArtifact{}, fmt.Errorf("push chart to %s: %w", ref, err)
	}

	p.logger.InfoContext(ctx, "published OCI artifact",
		slog.String("reference", ref),
		slog.String("digest", result.Manifest.Digest),
	)

	return port.PublishedArtifact{
		OCIReference: ref,
		Digest:       result.Manifest.Digest,
		Registry:     p.registryHost,
		Repository:   fmt.Sprintf("%s/charts/%s", appID, chartName),
		Tag:          version,
	}, nil
}

// RegistryHost returns the configured registry host.
func (p *Publisher) RegistryHost() string { return p.registryHost }

func (p *Publisher) buildRef(appID, chartName, version string) string {
	base := fmt.Sprintf("oci://%s", p.registryHost)
	if p.repositoryPrefix != "" {
		base = fmt.Sprintf("%s/%s", base, p.repositoryPrefix)
	}
	return fmt.Sprintf("%s/%s/charts/%s:%s", base, appID, chartName, version)
}

// Compile-time port compliance check.
var _ port.ArtifactPublisher = (*Publisher)(nil)

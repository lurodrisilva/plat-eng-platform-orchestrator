package observability

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"

	"github.com/myorg/platform-orchestrator/internal/config"
)

// Metrics holds all application-level metrics instruments.
type Metrics struct {
	DeploymentRequests     metric.Int64Counter
	DeploymentDuration     metric.Float64Histogram
	DeploymentTransitions  metric.Int64Counter
	DeploymentActive       metric.Int64UpDownCounter
	ChartResolutionDuration metric.Float64Histogram
	ArtifactPublishDuration metric.Float64Histogram
	ArgoSyncDuration       metric.Float64Histogram
	ArgoPollCount          metric.Int64Histogram
	DocumentDBDuration     metric.Float64Histogram
	OIDCValidationDuration metric.Float64Histogram
	CompensationExecutions metric.Int64Counter
	StatePersistFailures   metric.Int64Counter
}

// InitMetrics initializes the OpenTelemetry meter provider and returns application metrics.
func InitMetrics(ctx context.Context, cfg config.OTelConfig) (*sdkmetric.MeterProvider, *Metrics, error) {
	opts := []otlpmetricgrpc.Option{
		otlpmetricgrpc.WithEndpoint(cfg.OTLPEndpoint),
	}
	if cfg.Insecure {
		opts = append(opts, otlpmetricgrpc.WithInsecure())
	}

	exporter, err := otlpmetricgrpc.New(ctx, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("creating metrics exporter: %w", err)
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(cfg.ServiceName),
		),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("creating resource: %w", err)
	}

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter)),
		sdkmetric.WithResource(res),
	)

	otel.SetMeterProvider(mp)

	meter := mp.Meter("platform-orchestrator")
	m, err := createMetrics(meter)
	if err != nil {
		return nil, nil, fmt.Errorf("creating metrics instruments: %w", err)
	}

	return mp, m, nil
}

func createMetrics(meter metric.Meter) (*Metrics, error) {
	m := &Metrics{}
	var err error

	m.DeploymentRequests, err = meter.Int64Counter("deployment_requests_total",
		metric.WithDescription("Total deployment requests received"))
	if err != nil {
		return nil, err
	}

	m.DeploymentDuration, err = meter.Float64Histogram("deployment_duration_seconds",
		metric.WithDescription("End-to-end deployment duration"),
		metric.WithUnit("s"))
	if err != nil {
		return nil, err
	}

	m.DeploymentTransitions, err = meter.Int64Counter("deployment_status_transitions_total",
		metric.WithDescription("Deployment state machine transitions"))
	if err != nil {
		return nil, err
	}

	m.DeploymentActive, err = meter.Int64UpDownCounter("deployment_active_count",
		metric.WithDescription("Currently in-flight deployments"))
	if err != nil {
		return nil, err
	}

	m.ChartResolutionDuration, err = meter.Float64Histogram("chart_resolution_duration_seconds",
		metric.WithDescription("Chart resolution duration from GitHub"),
		metric.WithUnit("s"))
	if err != nil {
		return nil, err
	}

	m.ArtifactPublishDuration, err = meter.Float64Histogram("artifact_publish_duration_seconds",
		metric.WithDescription("OCI artifact publish duration"),
		metric.WithUnit("s"))
	if err != nil {
		return nil, err
	}

	m.ArgoSyncDuration, err = meter.Float64Histogram("argocd_sync_duration_seconds",
		metric.WithDescription("Argo CD sync duration from trigger to healthy"),
		metric.WithUnit("s"))
	if err != nil {
		return nil, err
	}

	m.ArgoPollCount, err = meter.Int64Histogram("argocd_poll_count",
		metric.WithDescription("Number of polls per deployment health check"))
	if err != nil {
		return nil, err
	}

	m.DocumentDBDuration, err = meter.Float64Histogram("documentdb_operation_duration_seconds",
		metric.WithDescription("DocumentDB operation duration"),
		metric.WithUnit("s"))
	if err != nil {
		return nil, err
	}

	m.OIDCValidationDuration, err = meter.Float64Histogram("oidc_validation_duration_seconds",
		metric.WithDescription("OIDC token validation duration"),
		metric.WithUnit("s"))
	if err != nil {
		return nil, err
	}

	m.CompensationExecutions, err = meter.Int64Counter("compensation_executions_total",
		metric.WithDescription("Compensation/rollback executions"))
	if err != nil {
		return nil, err
	}

	m.StatePersistFailures, err = meter.Int64Counter("deployment_state_persist_failure_total",
		metric.WithDescription("Failed state persistence operations"))
	if err != nil {
		return nil, err
	}

	return m, nil
}

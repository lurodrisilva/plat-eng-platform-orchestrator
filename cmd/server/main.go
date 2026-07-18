package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	httpadapter "github.com/myorg/platform-orchestrator/internal/adapter/inbound/http"
	"github.com/myorg/platform-orchestrator/internal/adapter/inbound/http/handler"
	"github.com/myorg/platform-orchestrator/internal/adapter/outbound/argocd"
	"github.com/myorg/platform-orchestrator/internal/adapter/outbound/entra"
	"github.com/myorg/platform-orchestrator/internal/adapter/outbound/oci"
	"github.com/myorg/platform-orchestrator/internal/adapter/outbound/persistence"
	"github.com/myorg/platform-orchestrator/internal/adapter/outbound/policy"
	deploymentapp "github.com/myorg/platform-orchestrator/internal/application/deployment"
	"github.com/myorg/platform-orchestrator/internal/application/port"
	"github.com/myorg/platform-orchestrator/internal/infrastructure/config"
	"github.com/myorg/platform-orchestrator/internal/infrastructure/telemetry"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "config.yaml"
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Secret fields carry yaml:"-" so they never serialize into the config file;
	// populate them from the environment after Load (ArgoCD API token, DocumentDB
	// connection string). ACR push/pull is secretless via workload identity, so
	// no registry credential is read here.
	if v := os.Getenv("DOCUMENTDB_CONNECTION_STRING"); v != "" {
		cfg.DocDB.ConnectionString = v
	}
	if v := os.Getenv("ARGOCD_TOKEN"); v != "" {
		cfg.ArgoCD.Token = v
	}

	logger := telemetry.NewLogger(cfg.OTel.ServiceName + "-server")

	tp, err := telemetry.Init(ctx, cfg.OTel)
	if err != nil {
		logger.Warn("traces init failed", slog.String("error", err.Error()))
	} else {
		defer func() {
			shutdownCtx, c := context.WithTimeout(context.Background(), 5*time.Second)
			defer c()
			_ = tp.Shutdown(shutdownCtx)
		}()
	}

	// DocumentDB
	mongoClient, err := mongo.Connect(ctx, options.Client().ApplyURI(cfg.DocDB.ConnectionString))
	if err != nil {
		return fmt.Errorf("connect documentdb: %w", err)
	}
	defer func() { _ = mongoClient.Disconnect(ctx) }()

	deploymentsColl := mongoClient.Database(cfg.DocDB.Database).Collection(cfg.DocDB.DeploymentsCollection)
	deploymentRepo := persistence.NewDeploymentRepository(deploymentsColl, logger)

	// Outbound adapters for the deploy pipeline (ADR-0016 in-process executor).
	// The resolver pulls the base umbrella from GHCR anonymously; the publisher
	// pushes the composed chart to ACR, which ArgoCD pulls via workload identity
	// (secretless — ADR-0021 §3). The only long-lived credential is the ArgoCD
	// API token, read from env above.
	chartResolver, err := oci.NewResolver(logger)
	if err != nil {
		return fmt.Errorf("build chart resolver: %w", err)
	}
	chartComposer := oci.NewComposer(logger)
	artifactPublisher := oci.NewPublisher(cfg.OCI.Registry, cfg.OCI.RepositoryPrefix, logger)
	argoClient := argocd.NewClient(cfg.ArgoCD.ServerURL, cfg.ArgoCD.Token, logger)

	executor := deploymentapp.NewDeployExecutionHandler(
		deploymentRepo, chartResolver, chartComposer, artifactPublisher, argoClient,
		deploymentapp.ExecutionConfig{
			PollInterval:       time.Duration(cfg.Deploy.PollIntervalSeconds) * time.Second,
			ConvergenceTimeout: time.Duration(cfg.Deploy.HealthConvergenceTimeoutSeconds) * time.Second,
			ArgoAppNamespace:   cfg.ArgoCD.AppNamespace,
		},
		logger,
	)

	// J3 tunable allowlist (governance, server-side — ADR-0006). Loaded from the
	// policies config; reject overrides of platform-locked knobs at the API
	// boundary in enforce mode, observe-only in audit mode.
	var allowlist port.TunableAllowlist
	if cfg.Policies.ConfigPath != "" {
		al, err := policy.LoadTunableAllowlist(cfg.Policies.ConfigPath)
		if err != nil {
			return fmt.Errorf("load tunable allowlist: %w", err)
		}
		allowlist = al
		logger.Info("tunable allowlist loaded",
			slog.String("mode", al.Mode()),
			slog.String("path", cfg.Policies.ConfigPath))
	}

	// Application use cases.
	// Branch/env PolicyEvaluator adapter is still a gap (nil = no gate).
	app := deploymentapp.Application{
		Commands: deploymentapp.Commands{
			CreateDeployment: deploymentapp.NewCreateDeploymentHandler(deploymentRepo, nil, allowlist, logger),
		},
		Queries: deploymentapp.Queries{
			GetDeployment: deploymentapp.NewGetDeploymentHandler(deploymentRepo),
		},
		Executor: executor,
	}

	// OIDC token validator (ADR-0015). Built from the configured issuer/audience;
	// verification is public-key only, so this holds no credential. A build
	// failure here is fatal: the tenant is unreachable or misconfigured, and the
	// service must not start accepting tokens it cannot verify.
	validator, err := entra.New(ctx, cfg.Auth.OIDC.Issuer, cfg.Auth.OIDC.Audience, logger)
	if err != nil {
		logger.Error("failed to build OIDC token validator", slog.String("error", err.Error()))
		os.Exit(1)
	}
	logger.Info("OIDC token validator ready",
		slog.String("issuer", cfg.Auth.OIDC.Issuer),
		slog.String("audience", cfg.Auth.OIDC.Audience))

	// HTTP handler + router
	deploymentHandler := handler.NewDeployment(app, validator, allowlist, logger)
	router := httpadapter.NewRouter(deploymentHandler, logger)

	srv := &http.Server{
		Addr:         cfg.Server.Addr,
		Handler:      router,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("starting API server", slog.String("addr", cfg.Server.Addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutting down")
		shutdownCtx, c := context.WithTimeout(context.Background(), 15*time.Second)
		defer c()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return fmt.Errorf("server: %w", err)
	}
}

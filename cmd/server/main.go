package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	httpadapter "github.com/myorg/platform-orchestrator/internal/adapter/inbound/http"
	"github.com/myorg/platform-orchestrator/internal/adapter/inbound/http/handler"
	"github.com/myorg/platform-orchestrator/internal/adapter/outbound/argocd"
	"github.com/myorg/platform-orchestrator/internal/adapter/outbound/entra"
	"github.com/myorg/platform-orchestrator/internal/adapter/outbound/github"
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
	// GitHub App private key for the scaffold-new-app dispatch (Phase F). PEM,
	// yaml:"-" so it never lands in the config file; the scaffolder is built only
	// when this and the App ID are present.
	if v := os.Getenv("GITHUB_APP_PRIVATE_KEY"); v != "" {
		cfg.GitHub.PrivateKey = v
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
	// Push to ACR with workload identity (secretless — ADR-0021 gate 3) when the
	// target is an Azure Container Registry; anonymous otherwise (public registries).
	var publisherOpts []oci.Option
	if strings.HasSuffix(cfg.OCI.Registry, ".azurecr.io") {
		publisherOpts = append(publisherOpts, oci.WithCredential(oci.ACRWorkloadIdentityCredential(cfg.OCI.Registry)))
		logger.Info("ACR workload-identity push enabled", slog.String("registry", cfg.OCI.Registry))
	}
	artifactPublisher := oci.NewPublisher(cfg.OCI.Registry, cfg.OCI.RepositoryPrefix, logger, publisherOpts...)
	var argoOpts []argocd.Option
	if cfg.ArgoCD.Insecure {
		argoOpts = append(argoOpts, argocd.WithInsecureTLS())
	}
	argoClient := argocd.NewClient(cfg.ArgoCD.ServerURL, cfg.ArgoCD.Token, logger, argoOpts...)

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
	// J3 resource policy (ADR-0023): governs the application dependencies a
	// request may declare. Left nil when no policies file is configured, and
	// nil REFUSES a request declaring resources rather than skipping the gate —
	// an unconfigured gate must not be a free pass when what it gates is spend.
	var resourcePolicy port.ResourcePolicyEvaluator
	if cfg.Policies.ConfigPath != "" {
		al, err := policy.LoadTunableAllowlist(cfg.Policies.ConfigPath)
		if err != nil {
			return fmt.Errorf("load tunable allowlist: %w", err)
		}
		allowlist = al
		logger.Info("tunable allowlist loaded",
			slog.String("mode", al.Mode()),
			slog.String("path", cfg.Policies.ConfigPath))

		rp, err := policy.LoadResourcePolicy(cfg.Policies.ConfigPath)
		if err != nil {
			return fmt.Errorf("load resource policy: %w", err)
		}
		resourcePolicy = rp
		// Log configured=false at startup so an absent resourcePolicy section is
		// visible here rather than diagnosed from a refused deployment later.
		logger.Info("resource policy loaded",
			slog.String("mode", rp.Mode()),
			slog.Bool("configured", rp.Configured()),
			slog.String("path", cfg.Policies.ConfigPath))
	}

	// Application use cases.
	// Branch/env PolicyEvaluator adapter is still a gap (nil = no gate).
	app := deploymentapp.Application{
		Commands: deploymentapp.Commands{
			CreateDeployment: deploymentapp.NewCreateDeploymentHandler(deploymentRepo, nil, allowlist, resourcePolicy, logger),
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

	// GitHub App scaffolder (Phase F, ADR-0009). Optional: built only when a
	// GitHub App is configured (AppID + private key). The orchestrator's only
	// GitHub responsibility is to mint an installation token, fire the
	// scaffold-new-app workflow_dispatch, and read repo existence — the actual
	// render+push runs in the workflow, not here. Nil when unconfigured, which
	// makes POST /api/v1/apps answer 503.
	var appsScaffolder port.Scaffolder
	var appsRepoReader port.AppRepoReader
	if cfg.GitHub.AppID != 0 && cfg.GitHub.PrivateKey != "" {
		sc, err := github.NewScaffolder(github.ScaffolderConfig{
			AppID:           cfg.GitHub.AppID,
			InstallationID:  cfg.GitHub.InstallationID,
			PrivateKeyPEM:   cfg.GitHub.PrivateKey,
			ScaffolderOwner: cfg.GitHub.ScaffolderOwner,
			ScaffolderRepo:  cfg.GitHub.ScaffolderRepo,
			WorkflowFile:    cfg.GitHub.WorkflowFile,
			Ref:             cfg.GitHub.WorkflowRef,
			NewRepoOwner:    cfg.GitHub.NewRepoOwner,
		}, logger)
		if err != nil {
			return fmt.Errorf("build scaffolder: %w", err)
		}
		appsScaffolder = sc
		// The same GitHub App installation reads a scaffolded repo's own
		// deploy/umbrella/Chart.yaml, which is what GET /apps/{name} resolves the
		// app's chart identity from (ADR-0023, decision 6).
		appsRepoReader = sc
		logger.Info("github scaffolder enabled",
			slog.String("scaffolderRepo", cfg.GitHub.ScaffolderOwner+"/"+cfg.GitHub.ScaffolderRepo),
			slog.String("workflow", cfg.GitHub.WorkflowFile),
			slog.String("newRepoOwner", cfg.GitHub.NewRepoOwner))
	}

	// HTTP handler + router
	deploymentHandler := handler.NewDeployment(app, validator, allowlist, logger)
	appsHandler := handler.NewApps(handler.AppsDeps{
		Scaffolder: appsScaffolder,
		RepoReader: appsRepoReader,
		// The same OCI resolver that pulls the umbrella at deploy time also reads
		// what an app has published — one registry client, one auth story.
		Artifacts:      chartResolver,
		ChartRepo:      cfg.OCI.AppChartRepository,
		Validator:      validator,
		ResourcePolicy: resourcePolicy,
		Logger:         logger,
	})
	router := httpadapter.NewRouter(deploymentHandler, appsHandler, logger)

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

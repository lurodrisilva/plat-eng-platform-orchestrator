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

	logger := telemetry.NewLogger(cfg.OTel.ServiceName + "-server")

	tp, err := telemetry.Init(ctx, cfg.OTel)
	if err != nil {
		logger.Warn("traces init failed", slog.String("error", err.Error()))
	} else {
		defer func() {
			shutdownCtx, c := context.WithTimeout(context.Background(), 5*time.Second)
			defer c()
			tp.Shutdown(shutdownCtx)
		}()
	}

	// DocumentDB
	mongoClient, err := mongo.Connect(ctx, options.Client().ApplyURI(cfg.DocDB.ConnectionString))
	if err != nil {
		return fmt.Errorf("connect documentdb: %w", err)
	}
	defer mongoClient.Disconnect(ctx)

	deploymentsColl := mongoClient.Database(cfg.DocDB.Database).Collection(cfg.DocDB.DeploymentsCollection)
	deploymentRepo := persistence.NewDeploymentRepository(deploymentsColl, logger)

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
	}

	// HTTP handler + router
	deploymentHandler := handler.NewDeployment(app, nil, logger)
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

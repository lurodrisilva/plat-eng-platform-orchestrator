package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"github.com/myorg/platform-orchestrator/internal/activity"
	"github.com/myorg/platform-orchestrator/internal/argocd"
	"github.com/myorg/platform-orchestrator/internal/config"
	"github.com/myorg/platform-orchestrator/internal/documentdb"
	gh "github.com/myorg/platform-orchestrator/internal/github"
	"github.com/myorg/platform-orchestrator/internal/helm"
	"github.com/myorg/platform-orchestrator/internal/observability"
	"github.com/myorg/platform-orchestrator/internal/oci"
	"github.com/myorg/platform-orchestrator/internal/policy"
	"github.com/myorg/platform-orchestrator/internal/workflow"
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

	// Load config
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "config.yaml"
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Initialize logging
	logger := observability.InitLogging(cfg.OTel.ServiceName + "-worker")

	// Initialize traces
	tp, err := observability.InitTraces(ctx, cfg.OTel)
	if err != nil {
		logger.Warn("failed to initialize traces", slog.String("error", err.Error()))
	} else {
		defer func() {
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer shutdownCancel()
			tp.Shutdown(shutdownCtx)
		}()
	}

	// Initialize metrics
	mp, _, err := observability.InitMetrics(ctx, cfg.OTel)
	if err != nil {
		logger.Warn("failed to initialize metrics", slog.String("error", err.Error()))
	} else {
		defer func() {
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer shutdownCancel()
			mp.Shutdown(shutdownCtx)
		}()
	}

	// Initialize Temporal client
	temporalClient, err := client.Dial(client.Options{
		HostPort:  cfg.Temporal.HostPort,
		Namespace: cfg.Temporal.Namespace,
		Logger:    newTemporalLogger(logger),
	})
	if err != nil {
		return fmt.Errorf("connecting to Temporal: %w", err)
	}
	defer temporalClient.Close()

	// Initialize dependencies
	githubClient := gh.NewClient(cfg.GitHub, logger)
	chartResolver := helm.NewResolver(githubClient, logger)
	chartComposer := helm.NewComposer(logger)
	ociClient := oci.NewClient(cfg.OCI, logger)
	argoClient := argocd.NewClient(cfg.ArgoCD.ServerURL, cfg.ArgoCD.Token, logger)

	healthCfg := argocd.DefaultHealthEvalConfig()
	healthCfg.HealthConvergenceTimeout = time.Duration(cfg.Deploy.HealthConvergenceTimeoutSeconds) * time.Second
	healthCfg.PollInterval = time.Duration(cfg.Deploy.PollIntervalSeconds) * time.Second
	healthEvaluator := argocd.NewHealthEvaluator(argoClient, healthCfg, logger)

	policyEngine, err := policy.NewEngine(cfg.Policies.ConfigPath)
	if err != nil {
		return fmt.Errorf("initializing policy engine: %w", err)
	}

	stateRepo, err := documentdb.NewRepository(ctx, cfg.DocDB, logger)
	if err != nil {
		return fmt.Errorf("connecting to DocumentDB: %w", err)
	}

	// Create activities with all dependencies
	activities := &activity.Activities{
		ChartResolver:   chartResolver,
		ChartComposer:   chartComposer,
		Publisher:       ociClient,
		ArgoClient:      argoClient,
		HealthEvaluator: healthEvaluator,
		StateRepo:       stateRepo,
		PolicyEngine:    policyEngine,
		Logger:          logger,
	}

	// Register Temporal worker
	w := worker.New(temporalClient, cfg.Temporal.TaskQueue, worker.Options{
		MaxConcurrentActivityExecutionSize:     10,
		MaxConcurrentWorkflowTaskExecutionSize: 10,
	})

	w.RegisterWorkflow(workflow.DeploymentWorkflow)
	w.RegisterActivity(activities)

	logger.Info("starting Temporal worker",
		slog.String("taskQueue", cfg.Temporal.TaskQueue),
		slog.String("namespace", cfg.Temporal.Namespace),
	)

	// Run worker with graceful shutdown
	if err := w.Run(worker.InterruptCh()); err != nil {
		return fmt.Errorf("worker error: %w", err)
	}

	return nil
}

type temporalLogger struct {
	logger *slog.Logger
}

func newTemporalLogger(l *slog.Logger) *temporalLogger {
	return &temporalLogger{logger: l.With(slog.String("component", "temporal"))}
}

func (l *temporalLogger) Debug(msg string, keyvals ...any)  { l.logger.Debug(msg, keyvals...) }
func (l *temporalLogger) Info(msg string, keyvals ...any)   { l.logger.Info(msg, keyvals...) }
func (l *temporalLogger) Warn(msg string, keyvals ...any)   { l.logger.Warn(msg, keyvals...) }
func (l *temporalLogger) Error(msg string, keyvals ...any)  { l.logger.Error(msg, keyvals...) }

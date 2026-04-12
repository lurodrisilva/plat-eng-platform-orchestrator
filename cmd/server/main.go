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

	"go.temporal.io/sdk/client"

	"github.com/myorg/platform-orchestrator/internal/api"
	"github.com/myorg/platform-orchestrator/internal/auth"
	"github.com/myorg/platform-orchestrator/internal/config"
	"github.com/myorg/platform-orchestrator/internal/documentdb"
	"github.com/myorg/platform-orchestrator/internal/observability"
	"github.com/myorg/platform-orchestrator/internal/policy"
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
	logger := observability.InitLogging(cfg.OTel.ServiceName + "-server")

	// Initialize traces
	tp, err := observability.InitTraces(ctx, cfg.OTel)
	if err != nil {
		logger.Warn("failed to initialize traces, continuing without", slog.String("error", err.Error()))
	} else {
		defer func() {
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer shutdownCancel()
			tp.Shutdown(shutdownCtx)
		}()
	}

	// Initialize metrics
	mp, metrics, err := observability.InitMetrics(ctx, cfg.OTel)
	if err != nil {
		logger.Warn("failed to initialize metrics, continuing without", slog.String("error", err.Error()))
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

	// Initialize OIDC validator
	oidcValidator := auth.NewValidator(cfg.Auth)

	// Initialize policy engine
	policyEngine, err := policy.NewEngine(cfg.Policies.ConfigPath)
	if err != nil {
		return fmt.Errorf("initializing policy engine: %w", err)
	}

	// Initialize DocumentDB
	stateRepo, err := documentdb.NewRepository(ctx, cfg.DocDB, logger)
	if err != nil {
		return fmt.Errorf("connecting to DocumentDB: %w", err)
	}

	// Create handler and router
	handler := api.NewHandler(
		temporalClient,
		oidcValidator,
		policyEngine,
		stateRepo,
		metrics,
		cfg.Temporal.TaskQueue,
		logger,
	)
	router := api.NewRouter(handler, logger)

	// Start HTTP server
	srv := &http.Server{
		Addr:         cfg.Server.Addr,
		Handler:      router,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	// Graceful shutdown
	errCh := make(chan error, 1)
	go func() {
		logger.Info("starting API server", slog.String("addr", cfg.Server.Addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutting down API server")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer shutdownCancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return fmt.Errorf("server error: %w", err)
	}
}

// temporalLogger adapts slog to Temporal's logger interface.
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

package argocd

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/myorg/platform-orchestrator/internal/domain"
)

const (
	HealthHealthy     = "Healthy"
	HealthProgressing = "Progressing"
	HealthDegraded    = "Degraded"
	HealthSuspended   = "Suspended"
	HealthMissing     = "Missing"
	HealthUnknown     = "Unknown"

	SyncSynced    = "Synced"
	SyncOutOfSync = "OutOfSync"
	SyncUnknown   = "Unknown"
)

// HealthEvaluator monitors Argo CD Application health until a terminal state.
type HealthEvaluator struct {
	client                  *Client
	pollInterval            time.Duration
	syncCompletionTimeout   time.Duration
	healthConvergenceTimeout time.Duration
	unknownGracePeriod      time.Duration
	logger                  *slog.Logger
}

// HealthEvalConfig configures health evaluation timeouts.
type HealthEvalConfig struct {
	PollInterval             time.Duration
	SyncCompletionTimeout    time.Duration
	HealthConvergenceTimeout time.Duration
	UnknownGracePeriod       time.Duration
}

func DefaultHealthEvalConfig() HealthEvalConfig {
	return HealthEvalConfig{
		PollInterval:             10 * time.Second,
		SyncCompletionTimeout:    5 * time.Minute,
		HealthConvergenceTimeout: 10 * time.Minute,
		UnknownGracePeriod:       2 * time.Minute,
	}
}

func NewHealthEvaluator(client *Client, cfg HealthEvalConfig, logger *slog.Logger) *HealthEvaluator {
	return &HealthEvaluator{
		client:                   client,
		pollInterval:             cfg.PollInterval,
		syncCompletionTimeout:    cfg.SyncCompletionTimeout,
		healthConvergenceTimeout: cfg.HealthConvergenceTimeout,
		unknownGracePeriod:       cfg.UnknownGracePeriod,
		logger:                   logger,
	}
}

// WaitForHealthy polls the application until it reaches a terminal health state.
// Returns the final health assessment.
func (h *HealthEvaluator) WaitForHealthy(ctx context.Context, appName string) (domain.DeploymentHealth, error) {
	startTime := time.Now()
	unknownSince := time.Time{}
	syncSeen := false

	ticker := time.NewTicker(h.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return domain.DeploymentHealth{
				SyncStatus:   SyncUnknown,
				HealthStatus: HealthUnknown,
				Message:      "context cancelled",
				EvaluatedAt:  time.Now(),
			}, ctx.Err()

		case <-ticker.C:
			app, err := h.client.GetApplication(ctx, appName)
			if err != nil {
				h.logger.WarnContext(ctx, "failed to get application status",
					slog.String("app", appName),
					slog.String("error", err.Error()),
				)
				continue // transient error, keep polling
			}

			syncStatus := app.Status.Sync.Status
			healthStatus := app.Status.Health.Status
			elapsed := time.Since(startTime)

			h.logger.DebugContext(ctx, "poll status",
				slog.String("app", appName),
				slog.String("sync", syncStatus),
				slog.String("health", healthStatus),
				slog.Duration("elapsed", elapsed),
			)

			// SUCCESS: Synced + Healthy
			if syncStatus == SyncSynced && healthStatus == HealthHealthy {
				return domain.DeploymentHealth{
					SyncStatus:   syncStatus,
					HealthStatus: healthStatus,
					Message:      "deployment healthy",
					EvaluatedAt:  time.Now(),
				}, nil
			}

			// TERMINAL FAILURE: Synced + Degraded
			if syncStatus == SyncSynced && healthStatus == HealthDegraded {
				return domain.DeploymentHealth{
					SyncStatus:   syncStatus,
					HealthStatus: healthStatus,
					Message:      fmt.Sprintf("deployment degraded: %s", app.Status.Health.Message),
					EvaluatedAt:  time.Now(),
				}, fmt.Errorf("deployment degraded after sync: %s", app.Status.Health.Message)
			}

			// TERMINAL FAILURE: Missing resources
			if healthStatus == HealthMissing {
				return domain.DeploymentHealth{
					SyncStatus:   syncStatus,
					HealthStatus: healthStatus,
					Message:      "resources missing after sync",
					EvaluatedAt:  time.Now(),
				}, fmt.Errorf("resources missing: %s", app.Status.Health.Message)
			}

			// Track when we first saw Synced (for convergence timeout)
			if syncStatus == SyncSynced {
				syncSeen = true
			}

			// Unknown state grace period
			if healthStatus == HealthUnknown {
				if unknownSince.IsZero() {
					unknownSince = time.Now()
				} else if time.Since(unknownSince) > h.unknownGracePeriod {
					return domain.DeploymentHealth{
						SyncStatus:   syncStatus,
						HealthStatus: healthStatus,
						Message:      fmt.Sprintf("health unknown for %s, exceeds grace period", h.unknownGracePeriod),
						EvaluatedAt:  time.Now(),
					}, fmt.Errorf("health unknown for %s", time.Since(unknownSince))
				}
			} else {
				unknownSince = time.Time{} // reset
			}

			// Sync completion timeout
			if !syncSeen && elapsed > h.syncCompletionTimeout {
				return domain.DeploymentHealth{
					SyncStatus:   syncStatus,
					HealthStatus: healthStatus,
					Message:      fmt.Sprintf("sync not completed within %s", h.syncCompletionTimeout),
					EvaluatedAt:  time.Now(),
				}, fmt.Errorf("sync timeout: still %s after %s", syncStatus, elapsed)
			}

			// Global convergence timeout
			if elapsed > h.healthConvergenceTimeout {
				return domain.DeploymentHealth{
					SyncStatus:   syncStatus,
					HealthStatus: healthStatus,
					Message:      fmt.Sprintf("deployment did not converge within %s", h.healthConvergenceTimeout),
					EvaluatedAt:  time.Now(),
				}, fmt.Errorf("convergence timeout: %s/%s after %s", syncStatus, healthStatus, elapsed)
			}
		}
	}
}

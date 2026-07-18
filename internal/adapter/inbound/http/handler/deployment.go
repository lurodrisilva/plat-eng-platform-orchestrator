package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	deploymentapp "github.com/myorg/platform-orchestrator/internal/application/deployment"
	"github.com/myorg/platform-orchestrator/internal/application/port"
	"github.com/myorg/platform-orchestrator/internal/domain/deployment"
	"github.com/myorg/platform-orchestrator/internal/infrastructure/telemetry"
)

// Deployment handles deployment HTTP endpoints.
type Deployment struct {
	app       deploymentapp.Application
	validator port.TokenValidator
	allowlist port.TunableAllowlist
	logger    *slog.Logger
}

// NewDeployment creates a deployment HTTP handler. allowlist may be nil (the
// validate endpoint then reports no violations).
func NewDeployment(app deploymentapp.Application, validator port.TokenValidator, allowlist port.TunableAllowlist, logger *slog.Logger) *Deployment {
	return &Deployment{app: app, validator: validator, allowlist: allowlist, logger: logger}
}

type createRequest struct {
	Application struct {
		ID   string `json:"id"`
		Team string `json:"team"`
	} `json:"application"`
	Image struct {
		Repository string `json:"repository"`
		Tag        string `json:"tag"`
		Digest     string `json:"digest"`
	} `json:"image"`
	Chart struct {
		Repository        string `json:"repository"`
		Name              string `json:"name"`
		VersionConstraint string `json:"versionConstraint"`
		AllowPrerelease   bool   `json:"allowPrerelease"`
	} `json:"chart"`
	Target struct {
		Environment string `json:"environment"`
		Cluster     string `json:"cluster"`
		Namespace   string `json:"namespace"`
		AppProject  string `json:"appProject"`
	} `json:"target"`
	Values map[string]any `json:"values"`
	Source struct {
		GitSHA           string `json:"gitSha"`
		GitRef           string `json:"gitRef"`
		GitHubRunID      string `json:"githubRunId"`
		GitHubRunAttempt int    `json:"githubRunAttempt"`
		WorkflowName     string `json:"workflowName"`
		Actor            string `json:"actor"`
	} `json:"source"`
	CorrelationID string `json:"correlationId"`
}

// Create handles POST /api/v1/deployments.
func (h *Deployment) Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := telemetry.Enrich(ctx, h.logger)

	// Fail closed when the validator is unconfigured (ADR-0015). A nil validator
	// means the deploy could not build an Entra verifier at startup; answering
	// 401 keeps the endpoint shut rather than panicking on the nil interface —
	// an unauthenticated create must never fall through to the use-case.
	if h.validator == nil {
		logger.ErrorContext(ctx, "token validator is not configured; refusing the request")
		writeError(w, http.StatusUnauthorized, "AUTHENTICATION_FAILED", "authentication is not configured")
		return
	}

	// Validate OIDC token
	token := extractBearer(r.Header.Get("Authorization"))
	if token == "" {
		writeError(w, http.StatusUnauthorized, "AUTHENTICATION_FAILED", "missing authorization header")
		return
	}

	claims, err := h.validator.Validate(ctx, token)
	if err != nil {
		logger.WarnContext(ctx, "OIDC validation failed", slog.String("error", err.Error()))
		writeError(w, http.StatusUnauthorized, "AUTHENTICATION_FAILED", err.Error())
		return
	}

	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", fmt.Sprintf("invalid request: %v", err))
		return
	}

	cmd := deploymentapp.CreateDeploymentCommand{
		ApplicationID:    req.Application.ID,
		Team:             req.Application.Team,
		ImageRepository:  req.Image.Repository,
		ImageTag:         req.Image.Tag,
		ImageDigest:      req.Image.Digest,
		ChartRepository:  req.Chart.Repository,
		ChartName:        req.Chart.Name,
		ChartConstraint:  req.Chart.VersionConstraint,
		AllowPrerelease:  req.Chart.AllowPrerelease,
		Environment:      req.Target.Environment,
		Cluster:          req.Target.Cluster,
		Namespace:        req.Target.Namespace,
		AppProject:       req.Target.AppProject,
		Values:           req.Values,
		GitSHA:           req.Source.GitSHA,
		GitRef:           req.Source.GitRef,
		GitHubRunID:      req.Source.GitHubRunID,
		GitHubRunAttempt: req.Source.GitHubRunAttempt,
		WorkflowName:     req.Source.WorkflowName,
		Actor:            req.Source.Actor,
		RepositoryFull:   claims.Repository,
		CorrelationID:    req.CorrelationID,
	}

	result, err := h.app.Commands.CreateDeployment.Handle(ctx, cmd)
	if err != nil {
		logger.ErrorContext(ctx, "create deployment failed", slog.String("error", err.Error()))
		// A locked-knob override (J3 tunable allowlist) is a distinct 422 so the
		// caller can surface which knobs are platform-locked.
		var locked *deploymentapp.LockedKnobError
		if errors.As(err, &locked) {
			writeError(w, http.StatusUnprocessableEntity, "LOCKED_KNOB_OVERRIDE", err.Error())
			return
		}
		writeError(w, http.StatusUnprocessableEntity, "DEPLOYMENT_FAILED", err.Error())
		return
	}

	// Fire the deploy pipeline asynchronously (ADR-0016 in-process executor):
	// POST returns 202 immediately and the goroutine drives resolve→…→HEALTHY,
	// persisting each transition for GET to read. Detach from the request
	// context so the pipeline outlives the response while keeping telemetry
	// baggage for correlation.
	h.triggerDeploy(ctx, logger, result.DeploymentID)

	writeJSON(w, http.StatusAccepted, map[string]string{
		"deploymentId": result.DeploymentID,
		"status":       result.Status,
		"statusUrl":    fmt.Sprintf("/api/v1/deployments/%s", result.DeploymentID),
		"createdAt":    time.Now().UTC().Format(time.RFC3339),
	})
}

// Redeploy handles POST /api/v1/deployments/{id}/deploy — re-drives a deployment
// that stalled mid-flight (e.g. after a restart) through the pipeline from its
// persisted state. Same auth as Create. Uses a path segment rather than the
// :deploy colon convention because net/http's ServeMux cannot express a
// wildcard with a colon suffix ({id}:deploy) in one segment.
func (h *Deployment) Redeploy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := telemetry.Enrich(ctx, h.logger)

	if h.validator == nil {
		writeError(w, http.StatusUnauthorized, "AUTHENTICATION_FAILED", "authentication is not configured")
		return
	}
	token := extractBearer(r.Header.Get("Authorization"))
	if token == "" {
		writeError(w, http.StatusUnauthorized, "AUTHENTICATION_FAILED", "missing authorization header")
		return
	}
	if _, err := h.validator.Validate(ctx, token); err != nil {
		writeError(w, http.StatusUnauthorized, "AUTHENTICATION_FAILED", err.Error())
		return
	}

	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing deployment ID")
		return
	}
	if h.app.Executor == nil {
		writeError(w, http.StatusServiceUnavailable, "EXECUTOR_UNAVAILABLE", "deploy executor is not configured")
		return
	}

	h.triggerDeploy(ctx, logger, id)
	writeJSON(w, http.StatusAccepted, map[string]string{
		"deploymentId": id,
		"statusUrl":    fmt.Sprintf("/api/v1/deployments/%s", id),
	})
}

// triggerDeploy fires the executor in a detached goroutine, logging failures.
// No-op when no executor is wired (handler tests).
func (h *Deployment) triggerDeploy(ctx context.Context, logger *slog.Logger, id string) {
	if h.app.Executor == nil {
		return
	}
	depID := deployment.DeploymentID(id)
	go func() {
		bg := context.WithoutCancel(ctx)
		if err := h.app.Executor.Execute(bg, depID); err != nil {
			logger.ErrorContext(bg, "async deploy failed",
				slog.String("deploymentId", depID.String()),
				slog.String("error", err.Error()))
		}
	}()
}

// Status handles GET /api/v1/deployments/{id}.
func (h *Deployment) Status(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing deployment ID")
		return
	}

	q := deploymentapp.GetDeploymentQuery{DeploymentID: id}
	dto, err := h.app.Queries.GetDeployment.Handle(ctx, q)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, dto)
}

// validateRequest is the body of POST /api/v1/deployments:validate. The
// tunable allowlist is per-environment, so the verdict depends on which
// environment the overlay targets; an empty environment resolves to the
// default key set.
type validateRequest struct {
	Environment string         `json:"environment"`
	Values      map[string]any `json:"values"`
}

// validateResponse is the advisory J3 verdict for a values overlay. It echoes
// the environment the verdict was computed for so the caller can confirm which
// per-environment rule applied.
type validateResponse struct {
	Environment string   `json:"environment"`
	Mode        string   `json:"mode"`
	Violations  []string `json:"violations"`
	Blocked     bool     `json:"blocked"`
}

// ValidateTunables handles POST /api/v1/deployments:validate — a non-mutating
// dry-run of the J3 tunable-allowlist check over a values overlay for a target
// environment, for the portal create wizard. Always returns 200 with the
// verdict; blocked = the overlay would be rejected at create (enforce mode).
// Dev-open: no OIDC, no secrets, values only.
func (h *Deployment) ValidateTunables(w http.ResponseWriter, r *http.Request) {
	var req validateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", fmt.Sprintf("invalid request: %v", err))
		return
	}

	resp := validateResponse{Environment: req.Environment, Mode: "disabled", Violations: []string{}}
	if h.allowlist != nil {
		dec := h.allowlist.Validate(r.Context(), req.Values, req.Environment)
		resp.Mode = dec.Mode
		resp.Blocked = dec.Reject
		if dec.Violations != nil {
			resp.Violations = dec.Violations
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

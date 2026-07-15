package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	deploymentapp "github.com/myorg/platform-orchestrator/internal/application/deployment"
	"github.com/myorg/platform-orchestrator/internal/application/port"
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
		Repository      string `json:"repository"`
		Name            string `json:"name"`
		VersionConstraint string `json:"versionConstraint"`
		AllowPrerelease bool   `json:"allowPrerelease"`
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

	writeJSON(w, http.StatusAccepted, map[string]string{
		"deploymentId": result.DeploymentID,
		"status":       result.Status,
		"statusUrl":    fmt.Sprintf("/api/v1/deployments/%s", result.DeploymentID),
		"createdAt":    time.Now().UTC().Format(time.RFC3339),
	})
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

// validateRequest is the body of POST /api/v1/deployments:validate.
type validateRequest struct {
	Values map[string]any `json:"values"`
}

// validateResponse is the advisory J3 verdict for a values overlay.
type validateResponse struct {
	Mode       string   `json:"mode"`
	Violations []string `json:"violations"`
	Blocked    bool     `json:"blocked"`
}

// ValidateTunables handles POST /api/v1/deployments:validate — a non-mutating
// dry-run of the J3 tunable-allowlist check over a values overlay, for the
// portal create wizard. Always returns 200 with the verdict; blocked = the
// overlay would be rejected at create (enforce mode). Dev-open: no OIDC, no
// secrets, values only.
func (h *Deployment) ValidateTunables(w http.ResponseWriter, r *http.Request) {
	var req validateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", fmt.Sprintf("invalid request: %v", err))
		return
	}

	resp := validateResponse{Mode: "disabled", Violations: []string{}}
	if h.allowlist != nil {
		dec := h.allowlist.Validate(r.Context(), req.Values)
		resp.Mode = dec.Mode
		resp.Blocked = dec.Reject
		if dec.Violations != nil {
			resp.Violations = dec.Violations
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"go.temporal.io/sdk/client"

	"github.com/myorg/platform-orchestrator/internal/auth"
	"github.com/myorg/platform-orchestrator/internal/domain"
	"github.com/myorg/platform-orchestrator/internal/observability"
	"github.com/myorg/platform-orchestrator/internal/policy"
	"github.com/myorg/platform-orchestrator/internal/state"
	"github.com/myorg/platform-orchestrator/internal/workflow"
	v1 "github.com/myorg/platform-orchestrator/pkg/api/v1"
)

// Handler implements the Platform Orchestrator HTTP API.
type Handler struct {
	temporalClient client.Client
	oidcValidator  *auth.Validator
	policyEngine   *policy.Engine
	stateRepo      state.Repository
	metrics        *observability.Metrics
	taskQueue      string
	logger         *slog.Logger
}

func NewHandler(
	tc client.Client,
	oidc *auth.Validator,
	pe *policy.Engine,
	repo state.Repository,
	metrics *observability.Metrics,
	taskQueue string,
	logger *slog.Logger,
) *Handler {
	return &Handler{
		temporalClient: tc,
		oidcValidator:  oidc,
		policyEngine:   pe,
		stateRepo:      repo,
		metrics:        metrics,
		taskQueue:      taskQueue,
		logger:         logger,
	}
}

// CreateDeployment handles POST /api/v1/deployments.
func (h *Handler) CreateDeployment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := observability.EnrichWithTrace(ctx, h.logger)
	requestID := r.Header.Get("X-Request-ID")

	// Validate OIDC token
	tokenStr, err := auth.ExtractBearerToken(r.Header.Get("Authorization"))
	if err != nil {
		writeError(w, http.StatusUnauthorized, "AUTHENTICATION_FAILED", "Missing or invalid authorization header", requestID)
		return
	}

	claims, err := h.oidcValidator.ValidateToken(ctx, tokenStr)
	if err != nil {
		logger.WarnContext(ctx, "OIDC validation failed", slog.String("error", err.Error()))
		writeError(w, http.StatusUnauthorized, "AUTHENTICATION_FAILED", fmt.Sprintf("Token validation failed: %v", err), requestID)
		return
	}

	// Parse request body
	var apiReq v1.CreateDeploymentRequest
	if err := json.NewDecoder(r.Body).Decode(&apiReq); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", fmt.Sprintf("Invalid request body: %v", err), requestID)
		return
	}

	// Convert to domain type
	req := toDomainRequest(apiReq, claims)

	// Validate request fields
	if err := validateRequest(req); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), requestID)
		return
	}

	// Synchronous policy check (fast fail)
	decision, err := h.policyEngine.Evaluate(ctx, req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Policy evaluation failed", requestID)
		return
	}
	if !decision.Allowed {
		writeError(w, http.StatusUnprocessableEntity, "POLICY_VIOLATION", decision.Reason, requestID)
		return
	}

	// Idempotency key → Temporal workflow ID
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey == "" {
		idempotencyKey = fmt.Sprintf("%s-%s-%s-%s",
			req.Application.ID, req.Target.Environment, req.Source.GitSHA[:7], req.Source.GitHubRunID)
	}
	workflowID := fmt.Sprintf("deploy:%s:%s:%s", req.Application.ID, req.Target.Environment, req.Source.GitSHA[:7])

	// Start Temporal workflow
	input := workflow.DeploymentWorkflowInput{
		Request:   req,
		TaskQueue: h.taskQueue,
	}

	workflowOpts := client.StartWorkflowOptions{
		ID:                    workflowID,
		TaskQueue:             h.taskQueue,
		WorkflowIDReusePolicy: 2, // AllowDuplicateFailedOnly
		SearchAttributes: map[string]any{
			"Application":      req.Application.ID,
			"Environment":      req.Target.Environment,
			"Cluster":          req.Target.Cluster,
			"GitSha":           req.Source.GitSHA,
			"GithubRunId":      req.Source.GitHubRunID,
			"Actor":            req.Source.Actor,
			"Team":             req.Application.Team,
			"DeploymentStatus": string(domain.StateReceived),
		},
	}

	run, err := h.temporalClient.ExecuteWorkflow(ctx, workflowOpts, workflow.DeploymentWorkflow, input)
	if err != nil {
		// Check if workflow already exists (idempotent duplicate)
		if isAlreadyStartedError(err) {
			// Return the existing workflow info
			resp := v1.CreateDeploymentResponse{
				DeploymentID:       fmt.Sprintf("dep-%s-%s-%s-%s", time.Now().UTC().Format("20060102"), req.Application.ID, req.Target.Environment[:4], req.Source.GitSHA[:7]),
				TemporalWorkflowID: workflowID,
				Status:             string(domain.StateReceived),
				StatusURL:          fmt.Sprintf("/api/v1/deployments/%s", workflowID),
				CreatedAt:          time.Now().UTC().Format(time.RFC3339),
			}
			writeJSON(w, http.StatusConflict, resp)
			return
		}
		logger.ErrorContext(ctx, "failed to start workflow", slog.String("error", err.Error()))
		writeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "Failed to start deployment workflow", requestID)
		return
	}

	input.TemporalRunID = run.GetRunID()

	logger.InfoContext(ctx, "deployment workflow started",
		slog.String("workflowId", workflowID),
		slog.String("runId", run.GetRunID()),
		slog.String("application", req.Application.ID),
		slog.String("environment", req.Target.Environment),
	)

	deploymentID := fmt.Sprintf("dep-%s-%s-%s-%s",
		time.Now().UTC().Format("20060102"),
		req.Application.ID,
		req.Target.Environment[:4],
		req.Source.GitSHA[:7],
	)

	resp := v1.CreateDeploymentResponse{
		DeploymentID:       deploymentID,
		TemporalWorkflowID: workflowID,
		TemporalRunID:      run.GetRunID(),
		Status:             string(domain.StateReceived),
		StatusURL:          fmt.Sprintf("/api/v1/deployments/%s", deploymentID),
		CreatedAt:          time.Now().UTC().Format(time.RFC3339),
	}

	writeJSON(w, http.StatusAccepted, resp)
}

// GetDeployment handles GET /api/v1/deployments/{id}.
func (h *Handler) GetDeployment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	deploymentID := r.PathValue("id")
	requestID := r.Header.Get("X-Request-ID")

	if deploymentID == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Missing deployment ID", requestID)
		return
	}

	doc, err := h.stateRepo.GetDeployment(ctx, deploymentID)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("Deployment %s not found", deploymentID), requestID)
		return
	}

	events, _ := h.stateRepo.ListEvents(ctx, deploymentID)

	var history []v1.StateEntry
	for _, e := range events {
		history = append(history, v1.StateEntry{
			State: string(e.State),
			At:    e.Timestamp,
		})
	}

	resp := v1.DeploymentStatusResponse{
		DeploymentID:       doc.ID,
		Status:             string(doc.Status),
		Application:        doc.ApplicationID,
		Environment:        doc.Environment,
		Cluster:            doc.Cluster,
		Namespace:          doc.Namespace,
		Image: &v1.ImageInfo{
			Repository: doc.Image.Repository,
			Tag:        doc.Image.Tag,
			Digest:     doc.Image.Digest,
		},
		ChartVersion:       doc.Chart.DeploymentVersion,
		ArgoAppName:        doc.ArgoCD.ApplicationName,
		ArgoSyncStatus:     doc.ArgoCD.SyncStatus,
		ArgoHealthStatus:   doc.ArgoCD.HealthStatus,
		TemporalWorkflowID: doc.Temporal.WorkflowID,
		StartedAt:          doc.StartedAt,
		CompletedAt:        doc.CompletedAt,
		DurationMs:         doc.DurationMs,
		StateHistory:       history,
		Error:              doc.Error,
	}

	writeJSON(w, http.StatusOK, resp)
}

// HealthCheck handles GET /healthz.
func (h *Handler) HealthCheck(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ReadyCheck handles GET /readyz.
func (h *Handler) ReadyCheck(w http.ResponseWriter, _ *http.Request) {
	// Could check Temporal connectivity, DocumentDB connectivity, etc.
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func toDomainRequest(apiReq v1.CreateDeploymentRequest, claims *auth.Claims) domain.DeploymentRequest {
	return domain.DeploymentRequest{
		Application: domain.ApplicationInfo{
			ID:   apiReq.Application.ID,
			Team: apiReq.Application.Team,
		},
		Image: domain.ImageInfo{
			Repository: apiReq.Image.Repository,
			Tag:        apiReq.Image.Tag,
			Digest:     apiReq.Image.Digest,
		},
		Chart: domain.ChartSourceInfo{
			Repository:        apiReq.Chart.Repository,
			Name:              apiReq.Chart.Name,
			VersionConstraint: apiReq.Chart.VersionConstraint,
			AllowPrerelease:   apiReq.Chart.AllowPrerelease,
		},
		Target: domain.TargetInfo{
			Environment: apiReq.Target.Environment,
			Cluster:     apiReq.Target.Cluster,
			Namespace:   apiReq.Target.Namespace,
			AppProject:  apiReq.Target.AppProject,
		},
		Values: apiReq.Values,
		Source: domain.SourceInfo{
			GitSHA:             apiReq.Source.GitSHA,
			GitRef:             apiReq.Source.GitRef,
			GitHubRunID:        apiReq.Source.GitHubRunID,
			GitHubRunAttempt:   apiReq.Source.GitHubRunAttempt,
			WorkflowName:       apiReq.Source.WorkflowName,
			Actor:              apiReq.Source.Actor,
			RepositoryFullName: claims.Repository,
		},
		CorrelationID: apiReq.CorrelationID,
	}
}

func validateRequest(req domain.DeploymentRequest) error {
	if req.Application.ID == "" {
		return fmt.Errorf("application.id is required")
	}
	if req.Application.Team == "" {
		return fmt.Errorf("application.team is required")
	}
	if req.Image.Repository == "" {
		return fmt.Errorf("image.repository is required")
	}
	if req.Image.Digest == "" {
		return fmt.Errorf("image.digest is required")
	}
	if req.Chart.Repository == "" {
		return fmt.Errorf("chart.repository is required")
	}
	if req.Chart.Name == "" {
		return fmt.Errorf("chart.name is required")
	}
	if req.Target.Environment == "" {
		return fmt.Errorf("target.environment is required")
	}
	if req.Target.Cluster == "" {
		return fmt.Errorf("target.cluster is required")
	}
	if req.Target.Namespace == "" {
		return fmt.Errorf("target.namespace is required")
	}
	if req.Target.AppProject == "" {
		return fmt.Errorf("target.appProject is required")
	}
	if req.Source.GitSHA == "" || len(req.Source.GitSHA) < 7 {
		return fmt.Errorf("source.gitSha must be at least 7 characters")
	}
	if req.Source.GitHubRunID == "" {
		return fmt.Errorf("source.githubRunId is required")
	}
	return nil
}

func isAlreadyStartedError(err error) bool {
	if err == nil {
		return false
	}
	// Temporal SDK wraps this as *serviceerror.WorkflowExecutionAlreadyStarted
	// We check the error string as a portable fallback
	errMsg := err.Error()
	return strings.Contains(errMsg, "already started") || strings.Contains(errMsg, "WorkflowExecutionAlreadyStarted")
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, code, message, requestID string) {
	resp := v1.ErrorResponse{
		Error: v1.ErrorDetail{
			Code:    code,
			Message: message,
		},
		RequestID: requestID,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	writeJSON(w, status, resp)
}

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	deploymentapp "github.com/myorg/platform-orchestrator/internal/application/deployment"
	"github.com/myorg/platform-orchestrator/internal/application/port"
	"github.com/myorg/platform-orchestrator/internal/domain/deployment"
	"github.com/myorg/platform-orchestrator/internal/infrastructure/telemetry"
)

// Apps handles application-scaffolding HTTP endpoints. It dispatches an external
// GitHub Actions workflow (via the scaffolder port) and reports whether the
// scaffolded repository exists — it never clones, renders, or pushes (ADR-0009).
type Apps struct {
	scaffolder     port.Scaffolder
	validator      port.TokenValidator
	resourcePolicy port.ResourcePolicyEvaluator
	logger         *slog.Logger
}

// NewApps creates an apps HTTP handler. scaffolder may be nil (the endpoints
// then answer 503); validator may be nil (the endpoints then fail closed 401).
//
// resourcePolicy may be nil, which REFUSES a create that declares resources
// rather than skipping the gate — the scaffolded default becomes a real Azure
// server on the app's first deploy, so an unconfigured policy must not be a
// free pass. A create declaring no resources is unaffected.
func NewApps(scaffolder port.Scaffolder, validator port.TokenValidator, resourcePolicy port.ResourcePolicyEvaluator, logger *slog.Logger) *Apps {
	return &Apps{scaffolder: scaffolder, validator: validator, resourcePolicy: resourcePolicy, logger: logger}
}

type createAppRequest struct {
	Name        string `json:"name"`
	Team        string `json:"team"`
	Domain      string `json:"domain"`
	Description string `json:"description"`
	// Resources are the dependencies the scaffolded repo should default to
	// (ADR-0023). Name is accepted only to be ignored, as on the deploy path.
	Resources []struct {
		Type      string `json:"type"`
		Name      string `json:"name"`
		Size      string `json:"size"`
		Version   string `json:"version"`
		StorageMb int    `json:"storageMb"`
	} `json:"resources"`
}

// authenticate runs the shared Entra fail-closed boundary. It returns the
// verified claims and true when the caller is authenticated; otherwise it has
// already written the 401 response and returns false.
func (h *Apps) authenticate(w http.ResponseWriter, r *http.Request, logger *slog.Logger) (port.OIDCClaims, bool) {
	ctx := r.Context()

	// Fail closed when the validator is unconfigured (ADR-0015): a nil validator
	// means no Entra verifier was built at startup, so answering 401 keeps the
	// endpoint shut rather than panicking on the nil interface.
	if h.validator == nil {
		logger.ErrorContext(ctx, "token validator is not configured; refusing the request")
		writeError(w, http.StatusUnauthorized, "AUTHENTICATION_FAILED", "authentication is not configured")
		return port.OIDCClaims{}, false
	}

	token := extractBearer(r.Header.Get("Authorization"))
	if token == "" {
		writeError(w, http.StatusUnauthorized, "AUTHENTICATION_FAILED", "missing authorization header")
		return port.OIDCClaims{}, false
	}

	claims, err := h.validator.Validate(ctx, token)
	if err != nil {
		logger.WarnContext(ctx, "OIDC validation failed", slog.String("error", err.Error()))
		writeError(w, http.StatusUnauthorized, "AUTHENTICATION_FAILED", err.Error())
		return port.OIDCClaims{}, false
	}
	return claims, true
}

// Create handles POST /api/v1/apps — dispatches the scaffold-new-app workflow
// for a new application. Entra-authenticated. Returns 202 with the repository
// the workflow will create and a status URL to poll for its existence.
func (h *Apps) Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := telemetry.Enrich(ctx, h.logger)

	claims, ok := h.authenticate(w, r, logger)
	if !ok {
		return
	}

	// Scaffolder is optional: the service runs without GitHub App config, but
	// then this endpoint cannot dispatch and reports 503 rather than 500.
	if h.scaffolder == nil {
		logger.ErrorContext(ctx, "scaffolder is not configured; cannot dispatch scaffold")
		writeError(w, http.StatusServiceUnavailable, "SCAFFOLDER_UNAVAILABLE", "app scaffolding is not configured")
		return
	}

	var req createAppRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", fmt.Sprintf("invalid request: %v", err))
		return
	}

	domain := req.Domain
	if domain == "" {
		domain = "account"
	}

	// Declared dependencies become the scaffolded repo's DEFAULT database shape,
	// so they are validated and authorized here rather than deferred to the
	// first deploy — a scaffold that writes an unprovisionable shape produces a
	// repo whose very first deploy cannot succeed.
	//
	// Policy is evaluated against the DEFAULT rule set (empty environment), not
	// a specific environment's: a scaffold targets no environment, and the repo
	// default has to be acceptable wherever the app is eventually deployed. The
	// deploy is judged again, by that environment's own rules — which is how
	// production still denies postgres even though the repo carries one.
	dbSpec, err := h.authorizeAppResources(ctx, logger, req)
	if err != nil {
		var notAllowed *deploymentapp.ResourceNotAllowedError
		if errors.As(err, &notAllowed) {
			writeError(w, http.StatusUnprocessableEntity, "RESOURCE_NOT_ALLOWED", err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}

	result, err := h.scaffolder.Dispatch(ctx, port.ScaffoldRequest{
		Name:        req.Name,
		Team:        req.Team,
		Domain:      domain,
		Description: req.Description,
		Actor:       claims.Principal(),
		DBSize:      dbSpec.Size,
		DBVersion:   dbSpec.Version,
		DBStorageMb: dbSpec.StorageMb,
	})
	if err != nil {
		logger.WarnContext(ctx, "scaffold dispatch failed", slog.String("error", err.Error()))
		// A name that fails the scaffolder's defensive validation is a client
		// error, not a server fault.
		if strings.Contains(err.Error(), "invalid app name") {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
			return
		}
		writeError(w, http.StatusBadGateway, "SCAFFOLD_DISPATCH_FAILED", err.Error())
		return
	}

	// Poll by RepoName (the repo that materializes, carrying the collision-
	// avoidance suffix), not AppName. appName is returned for display only.
	writeJSON(w, http.StatusAccepted, map[string]string{
		"appName":    result.AppName,
		"repoName":   result.RepoName,
		"repository": result.Repository,
		"repoUrl":    result.RepoURL,
		"statusUrl":  "/api/v1/apps/" + result.RepoName,
	})
}

// authorizeAppResources validates the declared dependencies against the domain
// and the resource policy, and returns the postgres shape to pass through to the
// scaffolder. A create declaring no resources returns the zero value, which the
// scaffolder adapter omits so the workflow's own defaults apply.
func (h *Apps) authorizeAppResources(ctx context.Context, logger *slog.Logger, req createAppRequest) (deploymentapp.ResourceSpec, error) {
	if len(req.Resources) == 0 {
		return deploymentapp.ResourceSpec{}, nil
	}

	specs := make([]deploymentapp.ResourceSpec, 0, len(req.Resources))
	for _, r := range req.Resources {
		if r.Name != "" {
			logger.WarnContext(ctx, "ignoring caller-supplied resource name; it is derived from the app name",
				slog.String("suppliedName", r.Name), slog.String("appName", req.Name))
		}
		specs = append(specs, deploymentapp.ResourceSpec{
			Type: r.Type, Size: r.Size, Version: r.Version, StorageMb: r.StorageMb,
		})
	}

	// Names derive from the app name, which is also what the scaffolder's render
	// uses — so validation here is judging the identity the repo will actually
	// carry, not a stand-in for it.
	resources, err := deploymentapp.BuildResources(req.Name, specs)
	if err != nil {
		return deploymentapp.ResourceSpec{}, err
	}
	if err := deploymentapp.AuthorizeResources(ctx, h.resourcePolicy, resources, "", logger); err != nil {
		return deploymentapp.ResourceSpec{}, err
	}

	// Only postgres reaches the scaffolder: the workflow's inputs describe one
	// database. Any other type would have been refused above.
	for _, r := range resources {
		if r.Type() == deployment.ResourceTypePostgres {
			return deploymentapp.ResourceSpec{
				Type:      r.Type().String(),
				Size:      r.Size(),
				Version:   r.Version(),
				StorageMb: r.StorageMb(),
			}, nil
		}
	}
	return deploymentapp.ResourceSpec{}, nil
}

// Status handles GET /api/v1/apps/{name} — reports whether the scaffolded
// repository exists yet. Entra-authenticated.
func (h *Apps) Status(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := telemetry.Enrich(ctx, h.logger)

	if _, ok := h.authenticate(w, r, logger); !ok {
		return
	}

	if h.scaffolder == nil {
		writeError(w, http.StatusServiceUnavailable, "SCAFFOLDER_UNAVAILABLE", "app scaffolding is not configured")
		return
	}

	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing app name")
		return
	}

	status, err := h.scaffolder.RepoStatus(ctx, name)
	if err != nil {
		logger.WarnContext(ctx, "repo status failed", slog.String("error", err.Error()))
		if strings.Contains(err.Error(), "invalid app name") {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
			return
		}
		writeError(w, http.StatusBadGateway, "REPO_STATUS_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"name":          status.Name,
		"ready":         status.Exists,
		"repoUrl":       status.RepoURL,
		"defaultBranch": status.DefaultBranch,
	})
}

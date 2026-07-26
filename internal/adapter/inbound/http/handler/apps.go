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
// scaffolded repository exists and what it can deploy — it never clones,
// renders, or pushes (ADR-0009).
type Apps struct {
	scaffolder     port.Scaffolder
	repoReader     port.AppRepoReader
	artifacts      port.AppArtifactResolver
	chartRepo      string
	validator      port.TokenValidator
	resourcePolicy port.ResourcePolicyEvaluator
	logger         *slog.Logger
}

// AppsDeps carries the collaborators NewApps needs. A struct rather than a
// parameter list because two of the fields are same-shaped optional interfaces
// and swapping them positionally would compile.
type AppsDeps struct {
	// Scaffolder may be nil, which makes the endpoints answer 503.
	Scaffolder port.Scaffolder

	// RepoReader and Artifacts resolve what a scaffolded app can deploy. Either
	// being nil (or ChartRepo being empty) degrades GET /apps/{name} to repo
	// existence alone — reported as chartStatus "unavailable", never as "not
	// published yet". The distinction matters: the portal offers Deploy on
	// published, waits on awaiting-first-build, and should surface a problem on
	// unavailable rather than waiting forever.
	RepoReader port.AppRepoReader
	Artifacts  port.AppArtifactResolver

	// ChartRepo is the OCI repository scaffolded apps publish their umbrellas
	// to, e.g. ghcr.io/lurodrisilva/helm-charts, without the chart name.
	ChartRepo string

	// Validator may be nil, which makes the endpoints fail closed with 401.
	Validator port.TokenValidator

	// ResourcePolicy may be nil, which REFUSES a create that declares resources
	// rather than skipping the gate — the scaffolded default becomes a real
	// Azure server on the app's first deploy, so an unconfigured policy must not
	// be a free pass. A create declaring no resources is unaffected.
	ResourcePolicy port.ResourcePolicyEvaluator

	Logger *slog.Logger
}

// NewApps creates an apps HTTP handler.
func NewApps(deps AppsDeps) *Apps {
	return &Apps{
		scaffolder:     deps.Scaffolder,
		repoReader:     deps.RepoReader,
		artifacts:      deps.Artifacts,
		chartRepo:      deps.ChartRepo,
		validator:      deps.Validator,
		resourcePolicy: deps.ResourcePolicy,
		logger:         deps.Logger,
	}
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

// Chart publication states reported by GET /api/v1/apps/{name}.
//
// Three states, not a boolean, because "we could not find out" is a different
// answer from "it is not there yet" and only one of them means "keep waiting".
// A registry outage reported as awaiting-first-build leaves the portal showing
// "waiting for the app's first build" for a build that finished hours ago.
const (
	chartStatusPublished   = "published"
	chartStatusAwaitingCI  = "awaiting-first-build"
	chartStatusUnavailable = "unavailable"
)

// Status handles GET /api/v1/apps/{name} — reports whether the scaffolded
// repository exists yet and what it can deploy. Entra-authenticated.
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

	resp := map[string]any{
		"name":          status.Name,
		"ready":         status.Exists,
		"repoUrl":       status.RepoURL,
		"defaultBranch": status.DefaultBranch,
	}
	// A repository that does not exist yet has nothing to look up, and asking
	// the registry about it would answer "not published" — true, but for the
	// wrong reason and one poll ahead of the truth.
	if status.Exists {
		h.describeDeployable(ctx, logger, status.Name, resp)
	} else {
		resp["chartPublished"] = false
		resp["chartStatus"] = chartStatusAwaitingCI
	}

	writeJSON(w, http.StatusOK, resp)
}

// describeDeployable fills in what the app can actually deploy: the umbrella
// chart resolved from the app's own repository, the version its CI has
// published, and the image that version runs (ADR-0023, decision 6).
//
// It never fails the request. Repository existence is the answer the portal
// polls for and it has already been obtained; degrading the artifact detail to
// chartStatus "unavailable" keeps that answer available while still telling the
// caller the difference between "not yet" and "could not tell".
func (h *Apps) describeDeployable(ctx context.Context, logger *slog.Logger, name string, resp map[string]any) {
	resp["chartPublished"] = false

	if h.repoReader == nil || h.artifacts == nil || h.chartRepo == "" {
		resp["chartStatus"] = chartStatusUnavailable
		return
	}

	// The chart's name comes from the repository, never from the app name. The
	// repository carries a collision-avoidance suffix the chart does not, and a
	// long app name is truncated to fit it — so `orders-v3-fcdc` publishes
	// `orders-v3-umbrella`, and no reversal of the suffix is reliable.
	umbrella, err := h.repoReader.ReadUmbrellaChart(ctx, name)
	if err != nil {
		logger.WarnContext(ctx, "could not read the app's umbrella chart",
			slog.String("app", name), slog.String("error", err.Error()))
		resp["chartStatus"] = chartStatusUnavailable
		return
	}
	if umbrella.ChartName == "" {
		// The repository exists but the scaffold workflow has not pushed its
		// content yet — a real window, since the repo is created before the push.
		resp["chartStatus"] = chartStatusAwaitingCI
		return
	}

	chart := map[string]any{
		"repository": h.chartRepo,
		"name":       umbrella.ChartName,
		// The umbrella values key the application subchart hangs under. Reported
		// rather than assumed: the scaffolder substitutes `hex-scaffold` for the
		// app's own name when it renders, so a caller that scopes a values
		// overlay to a hardcoded alias writes a key Helm discards in silence.
		"appValuesKey": umbrella.AppValuesKey,
		"version":      "",
	}
	resp["chart"] = chart

	published, err := h.artifacts.ResolveLatest(ctx, h.chartRepo, umbrella.ChartName, umbrella.AppValuesKey)
	if err != nil {
		logger.WarnContext(ctx, "could not resolve the app's published chart",
			slog.String("app", name),
			slog.String("chart", umbrella.ChartName),
			slog.String("error", err.Error()))
		resp["chartStatus"] = chartStatusUnavailable
		return
	}
	if !published.Published {
		resp["chartStatus"] = chartStatusAwaitingCI
		return
	}

	chart["version"] = published.ChartVersion
	resp["chartPublished"] = true
	resp["chartStatus"] = chartStatusPublished
	resp["image"] = map[string]any{
		"repository": published.ImageRepository,
		"tag":        published.ImageTag,
		"digest":     published.ImageDigest,
		// The commit the image was built from. A deployment needs provenance the
		// domain will accept, and without this the caller has to supply a sha it
		// does not have — which is how the portal ended up sending the template
		// repository's HEAD for every application.
		"sourceCommit": published.ImageSourceCommit,
	}
}

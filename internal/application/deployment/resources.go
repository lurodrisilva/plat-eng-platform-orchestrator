package deploymentapp

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/myorg/platform-orchestrator/internal/application/port"
	"github.com/myorg/platform-orchestrator/internal/domain/deployment"
)

// reservedValuePaths are the value-key prefixes the platform owns outright.
// They are not tunable knobs with a guardrail attached; they are the chart
// shape platformValues() writes, and reaching them from the request's `values`
// overlay would bypass resourcePolicy entirely.
//
// `sqldatabase` renders a PostgresInstance XR, which the control plane fulfils
// as a real Azure Flexible Server. An overlay setting `sqldatabase.enabled:
// true` on a deployment that declared NO resources would provision a billed
// server with nothing having authorized it — and the tunable allowlist ships
// audit-first, so it would log the violation and allow it.
//
// `hex-scaffold.postgres.bindBuildingBlock` is the other half: it decides which
// Secrets the app reads its credentials from. Letting a caller set it
// independently of the XR reintroduces exactly the disagreement between an app
// and its database that ADR-0023 exists to make impossible.
var reservedValuePaths = []string{
	"sqldatabase",
	"hex-scaffold.postgres.bindBuildingBlock",
}

// reservedOverrides returns the reserved paths a values overlay tries to set,
// as dotted prefixes rather than every leaf beneath them — a caller who sent a
// whole `sqldatabase` block should be told the block is reserved, not handed a
// list of its leaves.
func reservedOverrides(values map[string]any) []string {
	var hit []string
	for _, path := range reservedValuePaths {
		if valuesHavePath(values, strings.Split(path, ".")) {
			hit = append(hit, path)
		}
	}
	return hit
}

// valuesHavePath reports whether a nested values map contains the given path.
// A path whose final segment is present counts even when its value is empty:
// `sqldatabase: {}` is still an attempt to address the reserved block.
func valuesHavePath(values map[string]any, segments []string) bool {
	cur := values
	for i, seg := range segments {
		v, ok := cur[seg]
		if !ok {
			return false
		}
		if i == len(segments)-1 {
			return true
		}
		next, ok := v.(map[string]any)
		if !ok {
			return false
		}
		cur = next
	}
	return false
}

// BuildResources turns caller-declared dependencies into validated domain
// value objects, naming each one after the application. A caller-supplied name
// cannot appear here because ResourceSpec has no name field — the derivation is
// the only source (ADR-0023).
//
// Exported because both entrances need it and they must agree: POST /apps
// writes the repo's default database shape and POST /deployments provisions
// against it, so a shape one accepts and the other rejects would be a create
// that cannot be deployed.
func BuildResources(applicationID string, specs []ResourceSpec) ([]deployment.Resource, error) {
	if len(specs) == 0 {
		return nil, nil
	}
	resources := make([]deployment.Resource, 0, len(specs))
	for i, s := range specs {
		r, err := deployment.NewResource(s.Type, applicationID, s.Size, s.Version, s.StorageMb)
		if err != nil {
			return nil, fmt.Errorf("resources[%d]: %w", i, err)
		}
		resources = append(resources, r)
	}
	return resources, nil
}

// AuthorizeResources runs the resource policy over declared dependencies.
//
// A nil evaluator does NOT skip the gate — it closes it. Everywhere else in
// this service a nil collaborator means "that gate is not configured, carry
// on"; here carrying on would provision billed Azure infrastructure with
// nothing having authorized it, so the absent gate refuses instead. A request
// declaring no resources is unaffected, which is why the fail-closed posture
// costs nothing until someone asks the platform to spend money.
//
// An empty environment resolves to the policy's default rule set — that is what
// the scaffold path passes, because a repo default targets no environment.
func AuthorizeResources(
	ctx context.Context,
	evaluator port.ResourcePolicyEvaluator,
	resources []deployment.Resource,
	environment string,
	logger *slog.Logger,
) error {
	if len(resources) == 0 {
		return nil
	}
	if evaluator == nil {
		return &ResourceNotAllowedError{Violations: []string{
			"no resource policy is configured, so the platform cannot authorize an application dependency",
		}}
	}

	dec := evaluator.Evaluate(ctx, toResourceRequests(resources), environment)
	if dec.Reject {
		return &ResourceNotAllowedError{Violations: dec.Violations}
	}
	if len(dec.Violations) > 0 && logger != nil {
		logger.WarnContext(ctx, "resource policy would-deny (audit)",
			slog.String("mode", dec.Mode),
			slog.String("environment", environment),
			slog.Any("violations", dec.Violations),
		)
	}
	return nil
}

// toResourceRequests reduces domain resources to the facts policy judges. The
// derived name is deliberately not among them: policy governs what may be
// provisioned, not what it is called.
func toResourceRequests(resources []deployment.Resource) []port.ResourceRequest {
	out := make([]port.ResourceRequest, 0, len(resources))
	for _, r := range resources {
		out = append(out, port.ResourceRequest{
			Type:      r.Type().String(),
			Size:      r.Size(),
			Version:   r.Version(),
			StorageMb: r.StorageMb(),
		})
	}
	return out
}

package deploymentapp

import (
	"fmt"
	"sort"
	"strings"

	"github.com/myorg/platform-orchestrator/internal/domain/deployment"
)

// LockedKnobError is returned when a create request overrides one or more
// platform-locked knobs (value-key paths absent from the tunable allowlist)
// while the validator is in enforce mode. It wraps
// deployment.ErrTunableLocked so callers can errors.Is/errors.As it — the HTTP
// handler maps it to 422 LOCKED_KNOB_OVERRIDE with the offending keys named.
type LockedKnobError struct {
	Keys []string
}

func (e *LockedKnobError) Error() string {
	keys := append([]string(nil), e.Keys...)
	sort.Strings(keys)
	return fmt.Sprintf("%s: override of platform-locked knob(s): %s",
		deployment.ErrTunableLocked, strings.Join(keys, ", "))
}

func (e *LockedKnobError) Unwrap() error { return deployment.ErrTunableLocked }

// ResourceNotAllowedError is returned when a create request declares an
// application dependency the resource policy refuses — a type, size, version,
// storage size or instance count the target environment does not permit, or a
// request made while no policy is configured to authorize it.
//
// It wraps deployment.ErrResourceNotAllowed so callers can errors.Is/errors.As
// it; the HTTP handler maps it to 422 RESOURCE_NOT_ALLOWED. Unlike
// LockedKnobError this is never advisory: the thing refused would have created
// billed Azure infrastructure.
type ResourceNotAllowedError struct {
	Violations []string
}

func (e *ResourceNotAllowedError) Error() string {
	if len(e.Violations) == 0 {
		return deployment.ErrResourceNotAllowed.Error()
	}
	violations := append([]string(nil), e.Violations...)
	sort.Strings(violations)
	return fmt.Sprintf("%s: %s", deployment.ErrResourceNotAllowed, strings.Join(violations, "; "))
}

func (e *ResourceNotAllowedError) Unwrap() error { return deployment.ErrResourceNotAllowed }

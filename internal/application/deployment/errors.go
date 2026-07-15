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

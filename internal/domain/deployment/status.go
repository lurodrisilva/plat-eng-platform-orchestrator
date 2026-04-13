package deployment

import (
	"errors"
	"fmt"
)

// Status represents a deployment lifecycle state.
type Status int

const (
	StatusReceived          Status = iota + 1
	StatusValidating
	StatusRejected
	StatusMetadataGenerated
	StatusChartResolved
	StatusChartComposed
	StatusArtifactPublished
	StatusArgoAppCreated
	StatusSyncing
	StatusHealthy
	StatusDegraded
	StatusFailed
	StatusRolledBack
	StatusCompleted
)

// ErrInvalidTransition is returned when a state transition is not allowed.
var ErrInvalidTransition = errors.New("invalid status transition")

// _transitions defines allowed state transitions.
var _transitions = map[Status][]Status{
	StatusReceived:          {StatusValidating},
	StatusValidating:        {StatusRejected, StatusMetadataGenerated, StatusFailed},
	StatusMetadataGenerated: {StatusChartResolved, StatusFailed},
	StatusChartResolved:     {StatusChartComposed, StatusFailed},
	StatusChartComposed:     {StatusArtifactPublished, StatusFailed},
	StatusArtifactPublished: {StatusArgoAppCreated, StatusFailed},
	StatusArgoAppCreated:    {StatusSyncing, StatusFailed, StatusRolledBack},
	StatusSyncing:           {StatusHealthy, StatusDegraded, StatusFailed, StatusRolledBack},
	StatusHealthy:           {StatusCompleted, StatusFailed},
	StatusDegraded:          {StatusRolledBack, StatusFailed},
}

// _terminalStatuses are states from which no further transitions are allowed.
var _terminalStatuses = map[Status]bool{
	StatusRejected:   true,
	StatusFailed:     true,
	StatusRolledBack: true,
	StatusCompleted:  true,
}

func (s Status) String() string {
	switch s {
	case StatusReceived:
		return "RECEIVED"
	case StatusValidating:
		return "VALIDATING"
	case StatusRejected:
		return "REJECTED"
	case StatusMetadataGenerated:
		return "METADATA_GENERATED"
	case StatusChartResolved:
		return "CHART_RESOLVED"
	case StatusChartComposed:
		return "CHART_COMPOSED"
	case StatusArtifactPublished:
		return "ARTIFACT_PUBLISHED"
	case StatusArgoAppCreated:
		return "ARGO_APP_CREATED"
	case StatusSyncing:
		return "SYNCING"
	case StatusHealthy:
		return "HEALTHY"
	case StatusDegraded:
		return "DEGRADED"
	case StatusFailed:
		return "FAILED"
	case StatusRolledBack:
		return "ROLLED_BACK"
	case StatusCompleted:
		return "COMPLETED"
	default:
		return fmt.Sprintf("Status(%d)", s)
	}
}

// ParseStatus converts a string to a Status.
func ParseStatus(s string) (Status, error) {
	switch s {
	case "RECEIVED":
		return StatusReceived, nil
	case "VALIDATING":
		return StatusValidating, nil
	case "REJECTED":
		return StatusRejected, nil
	case "METADATA_GENERATED":
		return StatusMetadataGenerated, nil
	case "CHART_RESOLVED":
		return StatusChartResolved, nil
	case "CHART_COMPOSED":
		return StatusChartComposed, nil
	case "ARTIFACT_PUBLISHED":
		return StatusArtifactPublished, nil
	case "ARGO_APP_CREATED":
		return StatusArgoAppCreated, nil
	case "SYNCING":
		return StatusSyncing, nil
	case "HEALTHY":
		return StatusHealthy, nil
	case "DEGRADED":
		return StatusDegraded, nil
	case "FAILED":
		return StatusFailed, nil
	case "ROLLED_BACK":
		return StatusRolledBack, nil
	case "COMPLETED":
		return StatusCompleted, nil
	default:
		return 0, fmt.Errorf("unknown deployment status: %q", s)
	}
}

// IsTerminal reports whether the status is a terminal state.
func (s Status) IsTerminal() bool { return _terminalStatuses[s] }

// IsSuccess reports whether the deployment reached successful completion.
func (s Status) IsSuccess() bool { return s == StatusCompleted }

// CanTransitionTo checks whether the transition is allowed.
func (s Status) CanTransitionTo(next Status) bool {
	allowed, ok := _transitions[s]
	if !ok {
		return false
	}
	for _, a := range allowed {
		if a == next {
			return true
		}
	}
	return false
}

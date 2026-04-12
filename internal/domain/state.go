package domain

import "fmt"

type DeploymentState string

const (
	StateReceived          DeploymentState = "RECEIVED"
	StateValidating        DeploymentState = "VALIDATING"
	StateRejected          DeploymentState = "REJECTED"
	StateMetadataGenerated DeploymentState = "METADATA_GENERATED"
	StateChartResolved     DeploymentState = "CHART_RESOLVED"
	StateChartComposed     DeploymentState = "CHART_COMPOSED"
	StateArtifactPublished DeploymentState = "ARTIFACT_PUBLISHED"
	StateArgoAppCreated    DeploymentState = "ARGO_APP_CREATED"
	StateSyncing           DeploymentState = "SYNCING"
	StateHealthy           DeploymentState = "HEALTHY"
	StateDegraded          DeploymentState = "DEGRADED"
	StateFailed            DeploymentState = "FAILED"
	StateRolledBack        DeploymentState = "ROLLED_BACK"
	StateCompleted         DeploymentState = "COMPLETED"
)

// transitions defines the allowed state transitions in the deployment state machine.
var transitions = map[DeploymentState][]DeploymentState{
	StateReceived:          {StateValidating},
	StateValidating:        {StateRejected, StateMetadataGenerated, StateFailed},
	StateMetadataGenerated: {StateChartResolved, StateFailed},
	StateChartResolved:     {StateChartComposed, StateFailed},
	StateChartComposed:     {StateArtifactPublished, StateFailed},
	StateArtifactPublished: {StateArgoAppCreated, StateFailed},
	StateArgoAppCreated:    {StateSyncing, StateFailed, StateRolledBack},
	StateSyncing:           {StateHealthy, StateDegraded, StateFailed, StateRolledBack},
	StateHealthy:           {StateCompleted, StateFailed},
	StateDegraded:          {StateRolledBack, StateFailed},
}

// terminalStates are states from which no further transitions are allowed.
var terminalStates = map[DeploymentState]bool{
	StateRejected:   true,
	StateFailed:     true,
	StateRolledBack: true,
	StateCompleted:  true,
}

// ValidateTransition checks whether a state transition is allowed.
func ValidateTransition(from, to DeploymentState) error {
	if terminalStates[from] {
		return fmt.Errorf("cannot transition from terminal state %s", from)
	}
	allowed, ok := transitions[from]
	if !ok {
		return fmt.Errorf("unknown state %s", from)
	}
	for _, s := range allowed {
		if s == to {
			return nil
		}
	}
	return fmt.Errorf("invalid transition from %s to %s", from, to)
}

// IsTerminal returns true if the state is a terminal state.
func (s DeploymentState) IsTerminal() bool {
	return terminalStates[s]
}

// IsSuccess returns true if the deployment reached a successful terminal state.
func (s DeploymentState) IsSuccess() bool {
	return s == StateCompleted
}

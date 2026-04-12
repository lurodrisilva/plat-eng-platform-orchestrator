package domain

import "testing"

func TestValidateTransition_AllowedTransitions(t *testing.T) {
	tests := []struct {
		from DeploymentState
		to   DeploymentState
	}{
		{StateReceived, StateValidating},
		{StateValidating, StateRejected},
		{StateValidating, StateMetadataGenerated},
		{StateValidating, StateFailed},
		{StateMetadataGenerated, StateChartResolved},
		{StateChartResolved, StateChartComposed},
		{StateChartComposed, StateArtifactPublished},
		{StateArtifactPublished, StateArgoAppCreated},
		{StateArgoAppCreated, StateSyncing},
		{StateSyncing, StateHealthy},
		{StateSyncing, StateDegraded},
		{StateSyncing, StateRolledBack},
		{StateHealthy, StateCompleted},
		{StateDegraded, StateRolledBack},
	}

	for _, tt := range tests {
		t.Run(string(tt.from)+"→"+string(tt.to), func(t *testing.T) {
			if err := ValidateTransition(tt.from, tt.to); err != nil {
				t.Errorf("expected valid transition from %s to %s, got error: %v", tt.from, tt.to, err)
			}
		})
	}
}

func TestValidateTransition_DisallowedTransitions(t *testing.T) {
	tests := []struct {
		from DeploymentState
		to   DeploymentState
	}{
		{StateReceived, StateCompleted},
		{StateReceived, StateHealthy},
		{StateValidating, StateChartResolved},
		{StateSyncing, StateChartComposed},
		{StateHealthy, StateSyncing},
	}

	for _, tt := range tests {
		t.Run(string(tt.from)+"→"+string(tt.to), func(t *testing.T) {
			if err := ValidateTransition(tt.from, tt.to); err == nil {
				t.Errorf("expected error for invalid transition from %s to %s", tt.from, tt.to)
			}
		})
	}
}

func TestValidateTransition_FromTerminalState(t *testing.T) {
	terminals := []DeploymentState{StateRejected, StateFailed, StateRolledBack, StateCompleted}

	for _, terminal := range terminals {
		t.Run(string(terminal), func(t *testing.T) {
			if err := ValidateTransition(terminal, StateReceived); err == nil {
				t.Errorf("expected error transitioning from terminal state %s", terminal)
			}
		})
	}
}

func TestIsTerminal(t *testing.T) {
	if !StateCompleted.IsTerminal() {
		t.Error("COMPLETED should be terminal")
	}
	if !StateFailed.IsTerminal() {
		t.Error("FAILED should be terminal")
	}
	if !StateRejected.IsTerminal() {
		t.Error("REJECTED should be terminal")
	}
	if StateSyncing.IsTerminal() {
		t.Error("SYNCING should not be terminal")
	}
	if StateReceived.IsTerminal() {
		t.Error("RECEIVED should not be terminal")
	}
}

func TestIsSuccess(t *testing.T) {
	if !StateCompleted.IsSuccess() {
		t.Error("COMPLETED should be success")
	}
	if StateFailed.IsSuccess() {
		t.Error("FAILED should not be success")
	}
	if StateHealthy.IsSuccess() {
		t.Error("HEALTHY should not be success (COMPLETED is the terminal success)")
	}
}

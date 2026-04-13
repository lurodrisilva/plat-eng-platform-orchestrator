package deployment

import "testing"

func TestStatus_CanTransitionTo(t *testing.T) {
	tests := []struct {
		name string
		from Status
		to   Status
		want bool
	}{
		{name: "received to validating", from: StatusReceived, to: StatusValidating, want: true},
		{name: "validating to rejected", from: StatusValidating, to: StatusRejected, want: true},
		{name: "validating to metadata", from: StatusValidating, to: StatusMetadataGenerated, want: true},
		{name: "syncing to healthy", from: StatusSyncing, to: StatusHealthy, want: true},
		{name: "syncing to degraded", from: StatusSyncing, to: StatusDegraded, want: true},
		{name: "healthy to completed", from: StatusHealthy, to: StatusCompleted, want: true},
		{name: "degraded to rolled back", from: StatusDegraded, to: StatusRolledBack, want: true},
		{name: "received to completed", from: StatusReceived, to: StatusCompleted, want: false},
		{name: "received to healthy", from: StatusReceived, to: StatusHealthy, want: false},
		{name: "syncing to chart composed", from: StatusSyncing, to: StatusChartComposed, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.from.CanTransitionTo(tt.to)
			if got != tt.want {
				t.Errorf("(%s).CanTransitionTo(%s) = %v, want %v", tt.from, tt.to, got, tt.want)
			}
		})
	}
}

func TestStatus_IsTerminal(t *testing.T) {
	tests := []struct {
		name string
		give Status
		want bool
	}{
		{name: "completed is terminal", give: StatusCompleted, want: true},
		{name: "failed is terminal", give: StatusFailed, want: true},
		{name: "rejected is terminal", give: StatusRejected, want: true},
		{name: "rolled back is terminal", give: StatusRolledBack, want: true},
		{name: "syncing is not terminal", give: StatusSyncing, want: false},
		{name: "received is not terminal", give: StatusReceived, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.give.IsTerminal()
			if got != tt.want {
				t.Errorf("(%s).IsTerminal() = %v, want %v", tt.give, got, tt.want)
			}
		})
	}
}

func TestStatus_String_Roundtrip(t *testing.T) {
	statuses := []Status{
		StatusReceived, StatusValidating, StatusRejected, StatusMetadataGenerated,
		StatusChartResolved, StatusChartComposed, StatusArtifactPublished,
		StatusArgoAppCreated, StatusSyncing, StatusHealthy, StatusDegraded,
		StatusFailed, StatusRolledBack, StatusCompleted,
	}

	for _, s := range statuses {
		t.Run(s.String(), func(t *testing.T) {
			parsed, err := ParseStatus(s.String())
			if err != nil {
				t.Fatalf("ParseStatus(%q) returned error: %v", s.String(), err)
			}
			if parsed != s {
				t.Errorf("roundtrip failed: got %v, want %v", parsed, s)
			}
		})
	}
}

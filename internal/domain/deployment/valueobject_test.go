package deployment

import (
	"strings"
	"testing"
	"time"
)

func TestNewDeploymentID_ShortEnvironmentDoesNotPanic(t *testing.T) {
	ts := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	cases := []struct{ env, wantSlug string }{
		{"dev", "dev"},          // 3 chars — must not panic on [:4]
		{"qa", "qa"},            // 2 chars
		{"development", "deve"}, // truncated to 4
		{"prod", "prod"},        // exactly 4
	}
	for _, c := range cases {
		id := NewDeploymentID("acct", c.env, "f9f1f60", ts)
		if !strings.Contains(id.String(), "-"+c.wantSlug+"-") {
			t.Errorf("env %q: id %q missing slug %q", c.env, id, c.wantSlug)
		}
	}
}

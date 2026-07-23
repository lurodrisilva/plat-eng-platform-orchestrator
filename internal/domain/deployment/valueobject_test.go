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

func TestNewSource_ProvenanceOptionalExceptSHA(t *testing.T) {
	const sha = "95a2fd2a84c349b6f17f4a677fd228c0bbeb9ac2"
	// Portal (human) deploy: no CI run id / attempt, just the image's source sha.
	if _, err := NewSource(sha, "refs/heads/master", "", 0, "developer-portal", "user", "owner/repo"); err != nil {
		t.Fatalf("portal source (empty runID/attempt) should be valid, got: %v", err)
	}
	// CI deploy: full provenance still valid.
	if _, err := NewSource(sha, "refs/heads/main", "12345", 1, "deploy", "ci", "owner/repo"); err != nil {
		t.Fatalf("CI source should be valid, got: %v", err)
	}
	// The load-bearing sha is still required — ShortSHA feeds the component id,
	// deployment version, and composed chart, so a <7-char sha must be rejected.
	if _, err := NewSource("short", "", "", 0, "", "", ""); err == nil {
		t.Fatal("gitSHA < 7 chars must still be rejected")
	}
}

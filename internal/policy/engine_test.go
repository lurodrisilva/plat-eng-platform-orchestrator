package policy

import (
	"context"
	"testing"

	"github.com/myorg/platform-orchestrator/internal/domain"
)

func makeReq(env, ref, repo string) domain.DeploymentRequest {
	return domain.DeploymentRequest{
		Application: domain.ApplicationInfo{ID: "test-app", Team: "test"},
		Target:      domain.TargetInfo{Environment: env, Cluster: "c1", Namespace: "ns1", AppProject: "proj1"},
		Source: domain.SourceInfo{
			GitSHA:             "abc123def456789012345678901234567890abcd",
			GitRef:             ref,
			RepositoryFullName: repo,
			GitHubRunID:        "1",
			GitHubRunAttempt:   1,
			WorkflowName:       "deploy",
			Actor:              "user",
		},
	}
}

func TestEngine_AllowedBranch(t *testing.T) {
	engine := &Engine{
		config: PolicyConfig{
			EnvironmentRules: []EnvironmentRule{
				{
					Environment:     "production",
					AllowedBranches: []string{"main", "release/*"},
				},
			},
		},
	}

	tests := []struct {
		name    string
		ref     string
		allowed bool
	}{
		{"main branch allowed", "refs/heads/main", true},
		{"release branch allowed", "refs/heads/release/1.0", true},
		{"feature branch denied", "refs/heads/feature/abc", false},
		{"tag denied", "refs/tags/v1.0.0", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := makeReq("production", tt.ref, "myorg/app")
			decision, err := engine.Evaluate(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if decision.Allowed != tt.allowed {
				t.Errorf("expected allowed=%v, got %v (reason: %s)", tt.allowed, decision.Allowed, decision.Reason)
			}
		})
	}
}

func TestEngine_NoRulesAllowsAll(t *testing.T) {
	engine := &Engine{config: PolicyConfig{}}

	req := makeReq("production", "refs/heads/feature/test", "any/repo")
	decision, err := engine.Evaluate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Allowed {
		t.Error("expected allowed when no rules defined")
	}
}

func TestEngine_RepoAllowlist(t *testing.T) {
	engine := &Engine{
		config: PolicyConfig{
			EnvironmentRules: []EnvironmentRule{
				{
					Environment:  "production",
					AllowedRepos: []string{"myorg/allowed-app"},
				},
			},
		},
	}

	t.Run("allowed repo", func(t *testing.T) {
		req := makeReq("production", "refs/heads/main", "myorg/allowed-app")
		decision, _ := engine.Evaluate(context.Background(), req)
		if !decision.Allowed {
			t.Error("expected allowed for whitelisted repo")
		}
	})

	t.Run("denied repo", func(t *testing.T) {
		req := makeReq("production", "refs/heads/main", "myorg/other-app")
		decision, _ := engine.Evaluate(context.Background(), req)
		if decision.Allowed {
			t.Error("expected denied for non-whitelisted repo")
		}
	})
}

func TestMatchRefPattern(t *testing.T) {
	tests := []struct {
		ref     string
		pattern string
		match   bool
	}{
		{"refs/heads/main", "main", true},
		{"main", "main", true},
		{"refs/heads/release/1.0", "release/*", true},
		{"refs/heads/release/1.0.1", "release/*", true},
		{"release/1.0", "release/*", true},
		{"refs/heads/feature/abc", "release/*", false},
		{"refs/tags/v1.0.0", "v1.0.0", true},
	}

	for _, tt := range tests {
		t.Run(tt.ref+"→"+tt.pattern, func(t *testing.T) {
			got := matchRefPattern(tt.ref, tt.pattern)
			if got != tt.match {
				t.Errorf("matchRefPattern(%q, %q) = %v, want %v", tt.ref, tt.pattern, got, tt.match)
			}
		})
	}
}

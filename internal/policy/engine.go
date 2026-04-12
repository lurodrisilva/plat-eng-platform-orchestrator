package policy

import (
	"context"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/myorg/platform-orchestrator/internal/domain"
)

// Decision represents the outcome of a policy evaluation.
type Decision struct {
	Allowed bool         `json:"allowed"`
	Reason  string       `json:"reason,omitempty"`
	Rules   []RuleResult `json:"rules"`
}

type RuleResult struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Message string `json:"message,omitempty"`
}

// Engine evaluates deployment policies.
type Engine struct {
	config PolicyConfig
}

type PolicyConfig struct {
	EnvironmentRules []EnvironmentRule `yaml:"environmentRules"`
	FreezeWindows    []FreezeWindow    `yaml:"freezeWindows"`
}

type EnvironmentRule struct {
	Environment    string   `yaml:"environment"`
	AllowedBranches []string `yaml:"allowedBranches"`
	AllowedRepos    []string `yaml:"allowedRepos"`
}

type FreezeWindow struct {
	Name        string `yaml:"name"`
	Environment string `yaml:"environment"`
	Start       string `yaml:"start"` // RFC3339
	End         string `yaml:"end"`
	Reason      string `yaml:"reason"`
}

func NewEngine(configPath string) (*Engine, error) {
	cfg := PolicyConfig{}
	if configPath != "" {
		data, err := os.ReadFile(configPath)
		if err != nil {
			return nil, fmt.Errorf("reading policy config: %w", err)
		}
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("parsing policy config: %w", err)
		}
	}
	return &Engine{config: cfg}, nil
}

// Evaluate runs all policy rules against a deployment request.
func (e *Engine) Evaluate(_ context.Context, req domain.DeploymentRequest) (Decision, error) {
	var results []RuleResult

	results = append(results, e.evaluateBranchRule(req))
	results = append(results, e.evaluateRepoRule(req))
	results = append(results, e.evaluateFreezeRule(req))

	decision := Decision{Allowed: true, Rules: results}
	for _, r := range results {
		if !r.Passed {
			decision.Allowed = false
			decision.Reason = r.Message
			break
		}
	}

	return decision, nil
}

func (e *Engine) evaluateBranchRule(req domain.DeploymentRequest) RuleResult {
	for _, rule := range e.config.EnvironmentRules {
		if rule.Environment != req.Target.Environment {
			continue
		}
		if len(rule.AllowedBranches) == 0 {
			continue
		}
		ref := req.Source.GitRef
		for _, pattern := range rule.AllowedBranches {
			if matchRefPattern(ref, pattern) {
				return RuleResult{Name: "branch-environment-gate", Passed: true}
			}
		}
		return RuleResult{
			Name:    "branch-environment-gate",
			Passed:  false,
			Message: fmt.Sprintf("ref %q is not allowed for environment %q (allowed: %v)", ref, req.Target.Environment, rule.AllowedBranches),
		}
	}
	// No rule defined for this environment — allow by default.
	return RuleResult{Name: "branch-environment-gate", Passed: true}
}

func (e *Engine) evaluateRepoRule(req domain.DeploymentRequest) RuleResult {
	for _, rule := range e.config.EnvironmentRules {
		if rule.Environment != req.Target.Environment {
			continue
		}
		if len(rule.AllowedRepos) == 0 {
			continue
		}
		for _, repo := range rule.AllowedRepos {
			if repo == req.Source.RepositoryFullName {
				return RuleResult{Name: "repository-allowlist", Passed: true}
			}
		}
		return RuleResult{
			Name:    "repository-allowlist",
			Passed:  false,
			Message: fmt.Sprintf("repository %q is not allowed for environment %q", req.Source.RepositoryFullName, req.Target.Environment),
		}
	}
	return RuleResult{Name: "repository-allowlist", Passed: true}
}

func (e *Engine) evaluateFreezeRule(req domain.DeploymentRequest) RuleResult {
	// Freeze window evaluation is intentionally left as a stub.
	// In production, compare current time against configured windows.
	_ = req
	return RuleResult{Name: "freeze-window", Passed: true}
}

// matchRefPattern checks whether a git ref matches a branch pattern.
// Supports exact match and wildcard suffix (e.g., "release/*").
func matchRefPattern(ref, pattern string) bool {
	// Normalize ref: refs/heads/main → main, refs/tags/v1.0 → v1.0
	ref = strings.TrimPrefix(ref, "refs/heads/")
	ref = strings.TrimPrefix(ref, "refs/tags/")

	if strings.HasSuffix(pattern, "/*") {
		prefix := strings.TrimSuffix(pattern, "/*")
		return strings.HasPrefix(ref, prefix+"/") || ref == prefix
	}
	return ref == pattern
}

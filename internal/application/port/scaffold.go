package port

import "context"

// ScaffoldRequest is the intent to scaffold a new application repository.
type ScaffoldRequest struct {
	Name        string
	Team        string
	Domain      string
	Description string
	Actor       string
}

// ScaffoldResult reports where the scaffolded application will live. Repository
// is the "owner/name" slug; RepoURL is its https browse URL.
type ScaffoldResult struct {
	AppName    string
	Repository string // owner/name
	RepoURL    string
}

// RepoStatus reports whether the scaffolded repository exists yet.
type RepoStatus struct {
	Name          string
	Exists        bool
	RepoURL       string
	DefaultBranch string
}

// Scaffolder dispatches an external GitHub Actions workflow that scaffolds a new
// application repository, and reports whether that repository has materialized.
//
// The orchestrator does not clone, render, or push here: it only mints an
// installation token, fires workflow_dispatch, and reads repo existence. The
// actual scaffolding (template render + repo create + push) runs in the Actions
// workflow, not in this process (ADR-0009 — the orchestrator does not host
// source or generate manifests).
type Scaffolder interface {
	Dispatch(ctx context.Context, req ScaffoldRequest) (ScaffoldResult, error)
	RepoStatus(ctx context.Context, name string) (RepoStatus, error)
}

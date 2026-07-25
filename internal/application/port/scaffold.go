package port

import "context"

// ScaffoldRequest is the intent to scaffold a new application repository.
//
// The DB* fields are the app's default database shape, written into the
// scaffolded repo's umbrella values. They are the "repo holds the default" half
// of ADR-0023; a later deploy may override them via resources[]. Empty means
// the scaffolder's own defaults apply — the orchestrator does not invent a
// shape the caller did not ask for.
type ScaffoldRequest struct {
	Name        string
	Team        string
	Domain      string
	Description string
	Actor       string

	DBSize      string
	DBVersion   string
	DBStorageMb int
}

// ScaffoldResult reports where the scaffolded application will live. AppName is
// the clean application identity (drives the rendered code's namespaces/chart);
// RepoName is the actual repository name, which carries a short random suffix so
// repeated scaffolds never collide on the target account. Repository is the
// "owner/RepoName" slug; RepoURL is its https browse URL. Poll RepoStatus by
// RepoName (the repo that materializes), not AppName.
type ScaffoldResult struct {
	AppName    string
	RepoName   string
	Repository string // owner/RepoName
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

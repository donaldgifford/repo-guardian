// Package reconciler defines the Reconciler interface and registry for
// post-check actions. Reconcilers are pluggable actions that run after file
// checks pass — for example, syncing GitHub custom properties from
// catalog-info.yaml content.
package reconciler

import (
	"context"
	"log/slog"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/policy"
)

// Reconciler defines the interface for a post-check action that runs
// after a file rule's existence/assertion checks pass.
type Reconciler interface {
	// Name returns the reconciler type name (e.g., "custom_properties").
	Name() string

	// Reconcile performs the reconciler's action for a single repository.
	Reconcile(ctx context.Context, params *ReconcileParams) error
}

// ReconcileParams holds the inputs for a reconciler invocation.
type ReconcileParams struct {
	// Client is the GitHub API client scoped to the installation.
	Client ghclient.Client

	// Owner is the GitHub organization or user that owns the repository.
	Owner string

	// Repo is the repository name.
	Repo string

	// DefaultBranch is the repository's default branch (e.g., "main").
	DefaultBranch string

	// Content is the file content that triggered this reconciler.
	// Empty string if the file was not found.
	Content string

	// OpenPRs is the list of currently open pull requests in the repository.
	OpenPRs []*ghclient.PullRequest

	// DryRun indicates the reconciler should log actions without making changes.
	DryRun bool

	// Logger is a structured logger for the reconciler to use.
	Logger *slog.Logger

	// PRTemplate is the resolved (reconciler.pr → defaults.pr) PR
	// template for any PR this reconciler may open. Nil means the
	// caller passed no policy-driven PR config; reconcilers fall back
	// to their hardcoded defaults in that case. Reconciler PRs
	// deliberately skip rule.pr (DESIGN-0013 Q4 resolution).
	PRTemplate *policy.PRTemplate
}

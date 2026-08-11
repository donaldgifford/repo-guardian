// Package github provides a client interface and implementation for
// interacting with the GitHub API as a GitHub App.
package github

import (
	"context"
	"time"
)

// PullRequest represents a GitHub pull request with the fields
// relevant to repo-guardian's operations.
type PullRequest struct {
	Number int
	Title  string
	// Body is the PR description. Carried so a reconcile can tell
	// whether a refresh would actually change anything and skip the
	// PATCH when it would not (INV-0011 A4/B4).
	Body      string
	Head      string    // Branch name.
	State     string    // "open", "closed".
	CreatedAt time.Time // PR creation timestamp; used for age-bucketed metrics.

	// HTMLURL is the browser link to the PR, taken from the API rather
	// than assembled from owner/repo/number. Assembling it would bake
	// github.com into a codebase that is deliberately heading towards a
	// provider abstraction (INV-0006/0007), and would be wrong on GitHub
	// Enterprise today.
	//
	// Populate this at EVERY &PullRequest literal. Forgetting one is how
	// CreatedAt silently collapsed the PR-age gauge to its <1d bucket.
	HTMLURL string
}

// Installation represents a GitHub App installation on an org or user account.
type Installation struct {
	ID      int64
	Account string
}

// Repository represents a GitHub repository with metadata needed
// for the checker engine to decide whether to process it.
type Repository struct {
	Owner      string
	Name       string
	Archived   bool
	Fork       bool
	HasBranch  bool   // Whether the repo has a default branch (non-empty repo).
	DefaultRef string // Default branch name (e.g., "main").
}

// CustomPropertyValue represents a single custom property key-value pair
// on a GitHub repository. Value is nil to represent an unset/null
// property value; SetCustomPropertyValues passes a nil Value through as
// a JSON null, which is GitHub's documented mechanism for clearing a
// repository's property value.
type CustomPropertyValue struct {
	PropertyName string
	Value        *string
}

// RepoSettings holds GitHub repository settings that can be checked and
// remediated by setting rules.
type RepoSettings struct {
	DefaultBranch       string
	HasIssues           bool
	HasWiki             bool
	DeleteBranchOnMerge bool
	AllowMergeCommit    bool
	AllowSquashMerge    bool
	AllowRebaseMerge    bool
}

// RepoUpdateOpts holds fields for updating repository settings.
// Only non-nil pointer fields are applied.
type RepoUpdateOpts struct {
	HasIssues           *bool
	HasWiki             *bool
	DeleteBranchOnMerge *bool
	AllowMergeCommit    *bool
	AllowSquashMerge    *bool
	AllowRebaseMerge    *bool
	DefaultBranch       *string
}

// Ruleset represents a GitHub repository ruleset.
type Ruleset struct {
	ID                   int64
	Name                 string
	Enforcement          string // "active", "disabled", "evaluate"
	Target               string // "branch", "tag"
	BypassActors         []RulesetBypassActor
	Conditions           *RulesetConditions
	RequirePullRequest   *RulesetPullRequest
	RequireStatusChecks  *RulesetStatusChecks
	RequireLinearHistory bool
}

// RulesetBypassActor represents an actor that can bypass ruleset rules.
type RulesetBypassActor struct {
	ActorID   int64
	ActorType string // "OrganizationAdmin", "RepositoryRole", etc.
}

// RulesetConditions defines which branches the ruleset applies to.
type RulesetConditions struct {
	IncludePatterns []string
	ExcludePatterns []string
}

// RulesetPullRequest holds pull request requirements for a ruleset.
type RulesetPullRequest struct {
	RequiredApprovals      int
	DismissStaleReviews    bool
	RequireCodeOwnerReview bool
}

// RulesetStatusChecks holds required status check settings.
type RulesetStatusChecks struct {
	RequiredChecks     []string
	StrictStatusChecks bool
}

// Label represents a GitHub repository label.
type Label struct {
	Name        string
	Color       string
	Description string
}

// Client defines the GitHub operations that repo-guardian requires.
// This interface is the primary mock boundary for unit tests.
type Client interface {
	// GetContents checks whether a file exists at the given path in a repository.
	GetContents(ctx context.Context, owner, repo, path string) (bool, error)

	// ListOpenPullRequests returns all open pull requests for a repository.
	ListOpenPullRequests(ctx context.Context, owner, repo string) ([]*PullRequest, error)

	// GetRepository returns repository metadata including archive/fork status and default branch.
	GetRepository(ctx context.Context, owner, repo string) (*Repository, error)

	// GetBranchSHA returns the commit SHA of the given branch, or empty string if the branch does not exist.
	GetBranchSHA(ctx context.Context, owner, repo, branch string) (string, error)

	// CreateBranch creates a new branch from the given base SHA.
	CreateBranch(ctx context.Context, owner, repo, branch, baseSHA string) error

	// DeleteBranch deletes a branch from the repository.
	DeleteBranch(ctx context.Context, owner, repo, branch string) error

	// CreateOrUpdateFile creates or updates a file on the given branch.
	CreateOrUpdateFile(ctx context.Context, owner, repo, branch, path, content, message string) error

	// CreatePullRequest creates a new pull request and returns it.
	CreatePullRequest(ctx context.Context, owner, repo, title, body, head, base string) (*PullRequest, error)

	// AddLabelsToPR attaches the given label names to the PR (which is also
	// addressable as an issue). The call is a no-op when labels is empty.
	// Each label must already exist in the repository; missing labels return
	// an error from the API.
	AddLabelsToPR(ctx context.Context, owner, repo string, prNumber int, labels []string) error

	// ListInstallations returns all installations for this GitHub App.
	ListInstallations(ctx context.Context) ([]*Installation, error)

	// ListInstallationRepos returns all repositories accessible to the given installation.
	ListInstallationRepos(ctx context.Context, installationID int64) ([]*Repository, error)

	// CreateInstallationClient returns a Client scoped to a specific installation.
	// This is needed because each installation has its own access token.
	CreateInstallationClient(ctx context.Context, installationID int64) (Client, error)

	// GetFileContent returns the decoded content of a file in a repository.
	// Returns empty string and no error if the file does not exist.
	GetFileContent(ctx context.Context, owner, repo, path string) (string, error)

	// GetCustomPropertyValues returns all custom property values set on a repository.
	GetCustomPropertyValues(ctx context.Context, owner, repo string) ([]*CustomPropertyValue, error)

	// SetCustomPropertyValues creates or updates custom property values on a repository.
	SetCustomPropertyValues(ctx context.Context, owner, repo string, properties []*CustomPropertyValue) error

	// GetOrgPropertySchema returns the names of every custom property
	// defined at the organization level (DESIGN-0019 preflight). Values
	// are values-only, least-privilege: the App never creates or
	// mutates schema definitions, only reads names to filter payloads.
	// Requires the org-level "Custom properties: read" permission;
	// callers must fail open (send the unfiltered payload) on error
	// rather than block a sync that would otherwise succeed.
	GetOrgPropertySchema(ctx context.Context, org string) ([]string, error)

	// GetVulnerabilityAlertsEnabled checks if vulnerability alerts are enabled.
	GetVulnerabilityAlertsEnabled(ctx context.Context, owner, repo string) (bool, error)

	// EnableVulnerabilityAlerts enables vulnerability alerts for a repository.
	EnableVulnerabilityAlerts(ctx context.Context, owner, repo string) error

	// DisableVulnerabilityAlerts disables vulnerability alerts for a repository.
	DisableVulnerabilityAlerts(ctx context.Context, owner, repo string) error

	// GetRepoSettings returns the repository settings.
	GetRepoSettings(ctx context.Context, owner, repo string) (*RepoSettings, error)

	// UpdateRepository updates repository settings.
	UpdateRepository(ctx context.Context, owner, repo string, opts *RepoUpdateOpts) error

	// ListRepositoryRulesets returns all rulesets for a repository.
	ListRepositoryRulesets(ctx context.Context, owner, repo string) ([]*Ruleset, error)

	// GetRepositoryRuleset returns a specific ruleset by ID.
	GetRepositoryRuleset(ctx context.Context, owner, repo string, rulesetID int64) (*Ruleset, error)

	// CreateRepositoryRuleset creates a new repository ruleset.
	CreateRepositoryRuleset(ctx context.Context, owner, repo string, ruleset *Ruleset) (*Ruleset, error)

	// UpdateRepositoryRuleset updates an existing repository ruleset.
	UpdateRepositoryRuleset(ctx context.Context, owner, repo string, rulesetID int64, ruleset *Ruleset) (*Ruleset, error)

	// ListLabels returns all labels for a repository.
	ListLabels(ctx context.Context, owner, repo string) ([]*Label, error)

	// CreateLabel creates a new label in the repository.
	CreateLabel(ctx context.Context, owner, repo string, label *Label) error

	// UpdateLabel updates an existing label.
	UpdateLabel(ctx context.Context, owner, repo, name string, label *Label) error

	// DeleteLabel deletes a label from the repository.
	DeleteLabel(ctx context.Context, owner, repo, name string) error

	// RateLimitRemaining returns the current core rate-limit budget for
	// the given installation. Used by the stale-sweep reserve gate
	// (IMPL-0011 Phase 5e) and IMPL-0015 BudgetTracker.
	//
	// Returns (remaining, limit, resetAt, err); limit ≤ 0 means
	// "unknown" and the reserve gate falls open. resetAt is the
	// GitHub-reported hourly window rollover; callers may compare it
	// against time.Now() to trigger a refresh.
	RateLimitRemaining(ctx context.Context, installationID int64) (remaining, limit int, resetAt time.Time, err error)

	// GetContentsOnBranch returns the blob sha for a file at the given
	// path on the specified branch. Returns (sha, true, nil) when the
	// file exists, ("", false, nil) when it does not, and ("", false,
	// err) on transport or non-404 errors. The sha is required by
	// DeleteFile (optimistic concurrency); existence alone is not
	// enough to act on.
	GetContentsOnBranch(ctx context.Context, owner, repo, path, branch string) (sha string, exists bool, err error)

	// DeleteFile removes a file from the given branch. Required by the
	// IMPL-0013 Phase 3 orphan-cleanup path: when a file rule becomes
	// satisfied on the default branch, the template file repo-guardian
	// authored on the reconcile branch must be removed.
	DeleteFile(ctx context.Context, owner, repo, branch, path, sha, message string) error

	// UpdatePullRequest edits the title and body of an open pull
	// request. Used by IMPL-0013 Phase 3 to refresh the PR body when
	// the actionable rule set shrinks between sweeps.
	UpdatePullRequest(ctx context.Context, owner, repo string, number int, title, body string) error

	// UpdatePRBranch merges the pull request's base branch into its
	// head branch, keeping a long-lived reconcile PR mergeable as the
	// default branch advances. Returns an error when the merge cannot
	// be performed (most often a conflict with base); callers treat
	// the call as best-effort and continue rather than abort.
	UpdatePRBranch(ctx context.Context, owner, repo string, number int) error

	// ClosePullRequest transitions the pull request to the closed
	// state without merging. Used by IMPL-0013 Phase 3 when every file
	// rule has been satisfied and the PR is no longer needed.
	ClosePullRequest(ctx context.Context, owner, repo string, number int) error

	// ListPRComments returns issue comments on the given pull request.
	// (PR comments are issue comments under the hood.) Used by
	// UpsertPRComment to discover an existing sticky comment by its
	// marker line.
	ListPRComments(ctx context.Context, owner, repo string, number int) ([]*Comment, error)

	// UpsertPRComment writes a sticky comment to a pull request: if a
	// comment whose body starts with the given marker exists, edits
	// it in place; otherwise creates a new comment with the marker
	// prepended on row 1. Used by IMPL-0013 Phase 4 for the
	// reconcile-log sticky comment.
	UpsertPRComment(ctx context.Context, owner, repo string, number int, marker, body string) error
}

// Comment represents an issue/PR comment with the fields needed for
// marker-based sticky-comment upsert. Populated from
// go-github IssueComment responses.
type Comment struct {
	ID   int64
	Body string
}

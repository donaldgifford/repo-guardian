// Package github provides a client interface and implementation for
// interacting with the GitHub API as a GitHub App.
package github

import "context"

// PullRequest represents a GitHub pull request with the fields
// relevant to repo-guardian's operations.
type PullRequest struct {
	Number int
	Title  string
	Head   string // Branch name.
	State  string // "open", "closed".
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
// on a GitHub repository.
type CustomPropertyValue struct {
	PropertyName string
	Value        string
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
}

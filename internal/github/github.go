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
}

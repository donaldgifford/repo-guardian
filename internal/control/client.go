package control

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Reader is everything a control may read. It has no write methods, and
// the Evaluation App's token backs it. It is bound to one repository
// when built.
//
// File reads are pinned to one commit and cached, so every control in
// one evaluation sees the same tree. API reads (settings, rulesets,
// properties, labels, the org schema) are live observations: cached for
// the evaluation, but atomic neither with the commit nor with each
// other.
type Reader interface {
	// GetContents reads one file. found is false, with a nil error, when
	// the path does not exist.
	GetContents(ctx context.Context, path string) (content []byte, blobSHA string, found bool, err error)

	// ListDirectory returns the entry names under path; empty when the
	// directory does not exist.
	ListDirectory(ctx context.Context, path string) ([]string, error)

	// GetRepository reads the managed settings. A field the response
	// lacks is nil, never false.
	GetRepository(ctx context.Context) (RepositorySettings, error)

	// ListRulesets returns every ruleset that applies to the repository,
	// inherited ones included, each with its rules.
	ListRulesets(ctx context.Context) ([]Ruleset, error)

	// GetCustomProperties returns the repository's property values; a nil
	// value is a property explicitly set to null.
	GetCustomProperties(ctx context.Context) (map[string]*string, error)

	// OrgPropertySchema returns the property names the org schema
	// defines. It never mutates the schema.
	OrgPropertySchema(ctx context.Context) ([]string, error)

	// ListLabels returns every repository label.
	ListLabels(ctx context.Context) ([]Label, error)
}

// PRObserver is what the workflows read about pull requests and
// branches. No control uses it. The Evaluation App's token backs it in
// the evaluate activity; the Remediation App's token backs it in the
// remediation run.
type PRObserver interface {
	// ListPullRequests returns the open PRs whose head branch starts with
	// headPrefix, paginated to completion. A failed page is an error,
	// never a short list.
	ListPullRequests(ctx context.Context, headPrefix string) ([]PullRequest, error)

	// GetPullRequest returns one PR with its author and head repository.
	GetPullRequest(ctx context.Context, number int) (*PullRequest, error)

	// ListCommits returns branch's commits, newest first.
	ListCommits(ctx context.Context, branch string) ([]Commit, error)

	// GetRef returns branch's head SHA; exists is false, with a nil
	// error, when the branch does not exist.
	GetRef(ctx context.Context, branch string) (sha string, exists bool, err error)
}

// Writer is what the remediation workflow applies a change set with.
// Controls never receive it. The Remediation App's token backs it.
// There is no branch delete: repo-guardian never deletes a branch.
type Writer interface {
	// Commit publishes changes as one commit on branch through GitHub's
	// createCommitOnBranch mutation with expectedHeadOid = baseSHA, so
	// GitHub rejects the write when the head is anything else. That
	// rejection is ErrExpectedHeadMismatch. The branch must exist.
	Commit(ctx context.Context, branch, baseSHA string, changes []FileChange, message string) (headSHA string, err error)

	// CreateRef creates branch at sha; it fails when the branch exists.
	CreateRef(ctx context.Context, branch, sha string) error

	// UpdateBranch merges the base into PR number's head, provided the
	// head is still expectedHeadSHA. ErrUpdateBranchPending means GitHub
	// accepted the merge but the head has not moved yet;
	// ErrExpectedHeadMismatch means the head was not expectedHeadSHA;
	// ErrMergeConflict means the base does not merge cleanly. The first
	// two say "re-read the head and try again".
	UpdateBranch(ctx context.Context, number int, expectedHeadSHA string) error

	// UpdatePullRequestBase points PR number at base, for a renamed
	// default branch.
	UpdatePullRequestBase(ctx context.Context, number int, base string) error

	// CreatePullRequest opens a PR from head into base.
	CreatePullRequest(ctx context.Context, head, base, title, body string) (*PullRequest, error)

	// UpdatePullRequest replaces PR number's title and body.
	UpdatePullRequest(ctx context.Context, number int, title, body string) error

	// ClosePullRequest closes PR number without merging.
	ClosePullRequest(ctx context.Context, number int) error

	// UpsertPRComment edits the comment on PR number whose first line is
	// marker, or creates one.
	UpsertPRComment(ctx context.Context, number int, marker, body string) error

	// UpdateRepository writes the non-nil fields of settings.
	UpdateRepository(ctx context.Context, settings RepositorySettings) error

	// UpsertRuleset creates rs (ID 0) or updates it by ID. Only
	// repository-sourced rulesets may be written.
	UpsertRuleset(ctx context.Context, rs Ruleset) error

	// SetCustomProperties writes props; a nil value clears the property.
	SetCustomProperties(ctx context.Context, props map[string]*string) error

	// UpsertLabel creates l or updates the label of the same name
	// (compared case-insensitively, as GitHub does).
	UpsertLabel(ctx context.Context, l Label) error

	// DeleteLabel deletes the label name.
	DeleteLabel(ctx context.Context, name string) error
}

// Writer outcomes a remediation must tell apart. They are sentinels
// because callers need only which one occurred; implementations wrap
// them with the provider's detail. They do not survive an activity
// boundary, so the remediation activity turns the first two into a
// deferral value before returning.
var (
	// ErrExpectedHeadMismatch is a write refused because the branch head
	// was not the expected SHA: a concurrent push, a rewind, or a branch
	// deleted and recreated.
	ErrExpectedHeadMismatch = errors.New("branch head is not the expected sha")

	// ErrUpdateBranchPending is an update-branch GitHub accepted but has
	// not applied yet; re-read the head before acting on it.
	ErrUpdateBranchPending = errors.New("update-branch accepted, head not yet moved")

	// ErrMergeConflict is an update-branch whose base does not merge
	// cleanly into the head.
	ErrMergeConflict = errors.New("update-branch merge conflict")
)

// RepositorySettings is the managed repository settings. On a read, a
// nil field is one the response did not include, which a control
// reports as unknown{reason=permission}, never as false. On
// UpdateRepository, a nil field is not written. Enum strings are the
// REST and GraphQL values, which agree.
//
// The JSON form is stable because the value travels in activity
// payloads and feeds read digests.
type RepositorySettings struct {
	DefaultBranch            *string `json:"default_branch,omitempty"`
	HasWiki                  *bool   `json:"has_wiki,omitempty"`
	HasIssues                *bool   `json:"has_issues,omitempty"`
	HasProjects              *bool   `json:"has_projects,omitempty"`
	AllowMergeCommit         *bool   `json:"allow_merge_commit,omitempty"`
	AllowSquashMerge         *bool   `json:"allow_squash_merge,omitempty"`
	AllowRebaseMerge         *bool   `json:"allow_rebase_merge,omitempty"`
	AllowAutoMerge           *bool   `json:"allow_auto_merge,omitempty"`
	AllowUpdateBranch        *bool   `json:"allow_update_branch,omitempty"`
	DeleteBranchOnMerge      *bool   `json:"delete_branch_on_merge,omitempty"`
	WebCommitSignoffRequired *bool   `json:"web_commit_signoff_required,omitempty"`
	SquashMergeCommitTitle   *string `json:"squash_merge_commit_title,omitempty"`   // PR_TITLE | COMMIT_OR_PR_TITLE
	SquashMergeCommitMessage *string `json:"squash_merge_commit_message,omitempty"` // PR_BODY | COMMIT_MESSAGES | BLANK
	MergeCommitTitle         *string `json:"merge_commit_title,omitempty"`          // PR_TITLE | MERGE_MESSAGE
	MergeCommitMessage       *string `json:"merge_commit_message,omitempty"`        // PR_BODY | PR_TITLE | BLANK

	// SecurityAndAnalysis maps a feature (secret_scanning, ...) to
	// "enabled" or "disabled". A nil map means the block was absent; a
	// missing key means that one feature is unknown.
	SecurityAndAnalysis map[string]string `json:"security_and_analysis,omitempty"`
}

// RulesetSource is the level that owns a ruleset. Only a
// repository-owned ruleset can be written.
type RulesetSource string

// Ruleset sources, normalised to lower case from the API's source_type.
const (
	RulesetSourceRepository   RulesetSource = "repository"
	RulesetSourceOrganization RulesetSource = "organization"
	RulesetSourceEnterprise   RulesetSource = "enterprise"
)

// Ruleset is one ruleset as the API returns it. ID is 0 for a ruleset
// UpsertRuleset should create. Conditions, rules and bypass actors stay
// in the API's own JSON shape so this package needs no GitHub types; a
// nil field was not returned.
type Ruleset struct {
	ID           int64           `json:"id,omitempty"`
	Name         string          `json:"name"`
	Source       RulesetSource   `json:"source"`
	Enforcement  string          `json:"enforcement"` // active | disabled | evaluate
	Target       string          `json:"target"`      // branch | tag | push
	Conditions   json.RawMessage `json:"conditions,omitempty"`
	Rules        json.RawMessage `json:"rules,omitempty"`
	BypassActors json.RawMessage `json:"bypass_actors,omitempty"`
}

// Label is a repository label. Color is six hex digits without a
// leading "#".
type Label struct {
	Name        string `json:"name"`
	Color       string `json:"color"`
	Description string `json:"description,omitempty"`
}

// PullRequest is what PR observation and find-or-adopt need.
type PullRequest struct {
	Number      int
	Title       string
	Body        string
	URL         string
	State       string // open | closed
	Merged      bool
	BaseRef     string
	HeadRef     string
	HeadSHA     string
	HeadRepoID  int64 // 0 when the head repository was deleted
	AuthorID    int64
	AuthorLogin string
	AuthorType  string // User | Bot
	CreatedAt   time.Time
}

// Commit is one entry of ListCommits. AuthorID is 0 when the git author
// is not linked to a GitHub account. A createCommitOnBranch commit has
// CommitterLogin "web-flow" and is Verified: the one author signal a
// git author email cannot fake.
type Commit struct {
	SHA            string
	Message        string
	AuthorID       int64
	AuthorLogin    string
	CommitterLogin string
	Verified       bool
}

// FileChange is one file write or delete.
type FileChange struct {
	Path    string
	Content []byte // ignored when Delete is set
	Delete  bool

	// Reason is missing, rule_failed:<rule id>, stale or orphan. It feeds
	// the PR body and the evaluate preview and is not persisted.
	Reason string
}

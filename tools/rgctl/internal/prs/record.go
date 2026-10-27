package prs

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

// SchemaVersion is the record format version.
const SchemaVersion = 1

// Results a close run records per pull request.
const (
	ResultPlanned        = "planned"
	ResultCommented      = "commented"
	ResultClosed         = "closed"
	ResultBranchDeleted  = "branch_deleted"
	ResultAlreadyClosed  = "already_closed"
	ResultSkippedEdited  = "skipped_edited"
	ResultSkippedNotOurs = "skipped_not_ours"
	ResultError          = "error"
)

// Record is the file prs list writes and prs close reads: the cutover
// contract (DESIGN-0035 D5).
type Record struct {
	SchemaVersion int       `json:"schema_version"`
	GeneratedAt   time.Time `json:"generated_at"`
	Selection     Selection `json:"selection"`
	BotLogin      string    `json:"bot_login"`
	PRs           []Entry   `json:"prs"`
	Summary       Summary   `json:"summary"`
}

// Selection is the flags that produced a record.
type Selection struct {
	Repo   string `json:"repo,omitempty"`
	Org    string `json:"org,omitempty"`
	Config string `json:"config,omitempty"`
	// Orgs is the orgs actually scanned.
	Orgs       []string `json:"orgs,omitempty"`
	Branches   []string `json:"branches,omitempty"`
	Exhaustive bool     `json:"exhaustive,omitempty"`
}

// Entry is one repo-guardian pull request.
type Entry struct {
	Repository   string    `json:"repository"`
	Number       int       `json:"number"`
	URL          string    `json:"url"`
	HeadBranch   string    `json:"head_branch"`
	HeadSHA      string    `json:"head_sha"`
	Title        string    `json:"title"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	Edited       bool      `json:"edited"`
	EditedBy     []string  `json:"edited_by,omitempty"`
	ReconcileLog bool      `json:"reconcile_log"`
	// Result and Error are set by prs close.
	Result string `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

// Summary counts a list run.
type Summary struct {
	// Hits is search results (or open PRs read, without search).
	Hits int `json:"hits"`
	// Verified is hits read back through the Pull Requests API.
	Verified int `json:"verified"`
	// Dropped is verified PRs that failed the identity rule.
	Dropped int `json:"dropped"`
	Clean   int `json:"clean"`
	Edited  int `json:"edited"`
	// Incomplete is the number of orgs whose search was incomplete or hit
	// the 1000-result cap.
	Incomplete int `json:"incomplete"`
}

// Load reads a record and checks its schema version.
func Load(path string) (*Record, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the operator names the record to read
	if err != nil {
		return nil, fmt.Errorf("read record: %w", err)
	}
	var rec Record
	if err := json.Unmarshal(b, &rec); err != nil {
		return nil, fmt.Errorf("read record %s: %w", path, err)
	}
	if rec.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("read record %s: schema_version %d, this rgctl reads %d", path, rec.SchemaVersion, SchemaVersion)
	}
	return &rec, nil
}

// Encode writes rec as indented JSON.
func (rec *Record) Encode(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(rec)
}

// Save writes rec to path.
func (rec *Record) Save(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec // the operator names the output file
	if err != nil {
		return fmt.Errorf("write record: %w", err)
	}
	if err := rec.Encode(f); err != nil {
		_ = f.Close() //nolint:errcheck // the encode error is the one to report
		return fmt.Errorf("write record: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("write record: %w", err)
	}
	return nil
}

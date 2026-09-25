// Package workflows holds repo-guardian's Temporal workflow code
// (DESIGN-0026). Workflow code must be deterministic, so this package
// never imports the engine, the store or the GitHub client; a depguard
// rule enforces that. Everything with side effects runs in the
// activities package, which workflows reach by the names below.
package workflows

import "strconv"

// Workflow type names. They are part of every running execution's
// identity, so renaming one strands the executions already started
// under the old name.
const (
	RepoWorkflowName = "RepoWorkflow"
)

// Activity names. The activities package registers under these names
// and workflows execute by them, so neither side imports the other.
const (
	CheckRepoActivity        = "CheckRepo"
	RecordCheckActivity      = "RecordCheck"
	RecordCheckErrorActivity = "RecordCheckError"
	ParkActivity             = "Park"
)

// Signal names accepted by RepoWorkflow.
const (
	RecheckSignal       = "recheck"
	PolicyChangedSignal = "policy_changed"
	ParkSignal          = "park"
)

// RepoWorkflowID is the workflow ID for repositories.id. The ID is the
// per-repository lock: one RepoWorkflow per repository, kept across
// renames and transfers because the internal id never changes.
func RepoWorkflowID(repositoryID int64) string {
	return "repo/" + strconv.FormatInt(repositoryID, 10)
}

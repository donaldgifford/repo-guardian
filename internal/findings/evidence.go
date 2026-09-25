package findings

import "time"

// Evidence is the typed payload recorded with a finding. Each reason
// code has exactly one evidence type, and the store marshals it as
// JSON. Evidence is built only from data the engine already has; it
// must never cost an extra GitHub API call (DESIGN-0025 OQ9).
//
// Values that come from a repository are data. Every consumer renders
// them as text and never interpolates them into markup, queries or
// shell.
type Evidence interface {
	// Reason returns the reason code this evidence explains.
	Reason() Reason
}

// FileMissingEvidence lists the paths checked when none existed.
type FileMissingEvidence struct {
	PathsChecked []string `json:"paths_checked"`
}

// AssertionFailedEvidence names the file and the failed assertion. The
// message is clipped to ClipRunes.
type AssertionFailedEvidence struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// ContentDiffersEvidence names the file and the comparison used,
// "bytes" or "yaml".
type ContentDiffersEvidence struct {
	Path       string `json:"path"`
	Comparison string `json:"comparison"`
}

// ForbiddenPresentEvidence names the first forbidden path found.
type ForbiddenPresentEvidence struct {
	Path string `json:"path"`
}

// SettingMismatchEvidence records a repository setting that differs
// from the policy.
type SettingMismatchEvidence struct {
	Property string `json:"property"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
}

// RulesetMissingEvidence names the branch no matching ruleset covers.
type RulesetMissingEvidence struct {
	Branch string `json:"branch"`
}

// RulesetMismatchEvidence lists how an existing ruleset differs from
// the policy.
type RulesetMismatchEvidence struct {
	Branch     string   `json:"branch"`
	RulesetID  int64    `json:"ruleset_id"`
	Mismatches []string `json:"mismatches"`
}

// OutOfScopePolicyEvidence marks an org outside the top-level scope.
type OutOfScopePolicyEvidence struct{}

// OutOfScopeRuleEvidence marks an org the rule's scope excludes.
type OutOfScopeRuleEvidence struct{}

// IgnoredGlobalEvidence names the global ignore pattern that matched.
type IgnoredGlobalEvidence struct {
	Pattern string `json:"pattern"`
}

// IgnoredRuleEvidence names the rule ignore pattern that matched.
type IgnoredRuleEvidence struct {
	Pattern string `json:"pattern"`
}

// GateClosedEvidence names the referee rule that was not satisfied.
type GateClosedEvidence struct {
	Referee string `json:"referee"`
}

// BranchMissingEvidence names the absent branch-protection target.
type BranchMissingEvidence struct {
	Branch string `json:"branch"`
}

// EmptyRepositoryEvidence marks a repository with no default branch.
type EmptyRepositoryEvidence struct{}

// GateErrorEvidence records a failed referee evaluation. The gate fails
// closed. The error is clipped to ClipRunes.
type GateErrorEvidence struct {
	Referee string `json:"referee"`
	Error   string `json:"error"`
}

// ForeignPROpenEvidence marks a rule yielding to a human PR. The PR
// itself is recorded under the "pr" key as ForeignPREvidence.
type ForeignPROpenEvidence struct{}

// MigratedFromV1Evidence records when v1 first saw the rule failing.
type MigratedFromV1Evidence struct {
	V1ActionableSince time.Time `json:"v1_actionable_since"`
}

// Reason implements Evidence.
func (FileMissingEvidence) Reason() Reason { return ReasonFileMissing }

// Reason implements Evidence.
func (AssertionFailedEvidence) Reason() Reason { return ReasonAssertionFailed }

// Reason implements Evidence.
func (ContentDiffersEvidence) Reason() Reason { return ReasonContentDiffers }

// Reason implements Evidence.
func (ForbiddenPresentEvidence) Reason() Reason { return ReasonForbiddenPresent }

// Reason implements Evidence.
func (SettingMismatchEvidence) Reason() Reason { return ReasonSettingMismatch }

// Reason implements Evidence.
func (RulesetMissingEvidence) Reason() Reason { return ReasonRulesetMissing }

// Reason implements Evidence.
func (RulesetMismatchEvidence) Reason() Reason { return ReasonRulesetMismatch }

// Reason implements Evidence.
func (OutOfScopePolicyEvidence) Reason() Reason { return ReasonOutOfScopePolicy }

// Reason implements Evidence.
func (OutOfScopeRuleEvidence) Reason() Reason { return ReasonOutOfScopeRule }

// Reason implements Evidence.
func (IgnoredGlobalEvidence) Reason() Reason { return ReasonIgnoredGlobal }

// Reason implements Evidence.
func (IgnoredRuleEvidence) Reason() Reason { return ReasonIgnoredRule }

// Reason implements Evidence.
func (GateClosedEvidence) Reason() Reason { return ReasonGateClosed }

// Reason implements Evidence.
func (BranchMissingEvidence) Reason() Reason { return ReasonBranchMissing }

// Reason implements Evidence.
func (EmptyRepositoryEvidence) Reason() Reason { return ReasonEmptyRepository }

// Reason implements Evidence.
func (GateErrorEvidence) Reason() Reason { return ReasonGateError }

// Reason implements Evidence.
func (ForeignPROpenEvidence) Reason() Reason { return ReasonForeignPROpen }

// Reason implements Evidence.
func (MigratedFromV1Evidence) Reason() Reason { return ReasonMigratedFromV1 }

// PREvidence describes our reconcile-branch PR. It is recorded under
// the "pr" key when the remediation is pr_open.
type PREvidence struct {
	Number    int       `json:"number"`
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"created_at"`
}

// ForeignPREvidence describes a human PR the rule yields to. It is
// recorded under the "pr" key when the remediation is foreign_pr.
type ForeignPREvidence struct {
	Number      int    `json:"number"`
	URL         string `json:"url"`
	Head        string `json:"head"`
	MatchedTerm string `json:"matched_term"`
}

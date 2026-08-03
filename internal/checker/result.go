package checker

// Per-check outcome types (IMPL-0023 Phase 1 / DESIGN-0022 §Per-rule
// posture state). The engine already computes every fact in here while
// deciding what to do; before this it threw all of it away the moment
// the PR was opened. CheckResult is the value the worker's write-back
// persists into `rule_state`, turning an event stream into queryable
// posture.

// RuleKind classifies a rule for posture reporting. The values are
// persisted verbatim in `rule_state.rule_kind`, so they are part of the
// schema contract — renaming one is a migration, not a refactor.
type RuleKind string

// The three rule kinds the policy engine evaluates.
const (
	RuleKindFile             RuleKind = "file"
	RuleKindSetting          RuleKind = "setting"
	RuleKindBranchProtection RuleKind = "branch_protection"
)

// RuleOutcome is one rule's verdict for one repository.
//
// Actionable means "this repo is not compliant with this rule right
// now", which is deliberately not the same as "the engine did
// something". A file rule stays actionable while its PR sits open,
// because the default branch is still missing the file. A setting rule
// that was found mismatched and successfully remediated in the same
// pass is *not* actionable, because by the end of the check the repo
// complies. Reporting it as actionable would set actionable_since on
// one tick and clear it on the next, manufacturing a flap out of a
// self-healing event.
//
// Only rules the engine actually evaluated appear. Rules skipped by
// scope, ignore, a closed when-gate, or `enabled = false` produce no
// outcome at all — they do not apply to this repo, so counting them in
// `repos_tracked` would dilute every compliance percentage. The
// worker's delete-not-in reconciliation then clears any row a previous
// policy left behind.
type RuleOutcome struct {
	RuleName   string
	Kind       RuleKind
	Actionable bool
}

// CheckResult is everything one CheckRepo pass learned that outlives
// the pass itself.
//
// CatalogParseOK is a per-repo, not per-rule, fact, so it rides here
// rather than in Outcomes: nil means no catalog rule was evaluated
// (unknown), false means a catalog-info.yaml was found and could not be
// parsed into a Component. That distinction is why it is a pointer —
// "unknown" and "broken" drive different operator responses, and a
// plain bool would silently merge them.
type CheckResult struct {
	Outcomes       []RuleOutcome
	CatalogParseOK *bool
}

// add appends an outcome. Nil-safe so callers on the skip paths, which
// legitimately have no result to fill, need no guard of their own.
func (r *CheckResult) add(name string, kind RuleKind, actionable bool) {
	if r == nil {
		return
	}

	r.Outcomes = append(r.Outcomes, RuleOutcome{
		RuleName:   name,
		Kind:       kind,
		Actionable: actionable,
	})
}

// setCatalogParseOK records catalog parseability, copying nil through
// unchanged so "no catalog rule ran" stays distinct from a verdict.
// Nil-safe on the same terms as add — every mutation of a CheckResult
// goes through a method, so no caller has to know which fields tolerate
// a nil receiver and which do not.
func (r *CheckResult) setCatalogParseOK(ok *bool) {
	if r == nil {
		return
	}

	r.CatalogParseOK = ok
}

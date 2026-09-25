package checker

import "github.com/donaldgifford/repo-guardian/internal/findings"

// Per-check outcome types (IMPL-0023 Phase 1 / DESIGN-0022 §Per-rule
// posture state). The engine already computes every fact in here while
// deciding what to do; before this it threw all of it away the moment
// the PR was opened. CheckResult is the value the worker's write-back
// persists into `rule_state`, turning an event stream into queryable
// posture.

// RuleKind classifies a rule for posture reporting. The values are
// persisted verbatim in `rule_state.rule_kind` and `findings.rule_kind`,
// so they are part of the schema contract — renaming one is a migration,
// not a refactor.
type RuleKind = findings.RuleKind

// The three rule kinds the policy engine evaluates.
const (
	RuleKindFile             = findings.RuleKindFile
	RuleKindSetting          = findings.RuleKindSetting
	RuleKindBranchProtection = findings.RuleKindBranchProtection
)

// RuleOutcome is one rule's verdict for one repository, in the v2
// finding facets (DESIGN-0025 § Finding model).
//
// Status is about the default branch, deliberately not about what the
// engine did. A file rule stays non_compliant while its PR sits open,
// because the default branch is still missing the file. A setting rule
// that was found mismatched and successfully remediated in the same pass
// is compliant with remediation applied, because by the end of the check
// the repo complies. Recording it as non_compliant would flap on every
// self-healing tick.
//
// The enrichment is data-only: nothing that decides an action reads
// Reason, Remediation or Evidence.
type RuleOutcome struct {
	RuleName    string
	Kind        RuleKind
	Status      findings.Status
	Reason      findings.Reason
	Remediation findings.Remediation
	Evidence    findings.Evidence
	// PR is our open reconcile PR when Remediation is pr_open.
	PR *findings.PREvidence
	// ForeignPR is the human PR the rule yields to when Remediation is
	// foreign_pr.
	ForeignPR *findings.ForeignPREvidence
}

// Actionable is v1's boolean: the repository is non-compliant and
// repo-guardian has not yielded to a human PR. It keeps the PR, drift
// and auto-close paths and v1's rule_state write-back unchanged, and the
// parity suite compares it against the pre-enrichment verdicts.
func (o RuleOutcome) Actionable() bool { //nolint:gocritic // value receiver keeps call sites on slices of outcomes simple
	return o.Status == findings.StatusNonCompliant && o.Remediation != findings.RemediationForeignPR
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

// record appends an outcome. Nil-safe so callers on the skip paths, which
// legitimately have no result to fill, need no guard of their own. An
// empty Remediation is normalized to none.
func (r *CheckResult) record(o RuleOutcome) { //nolint:gocritic // outcomes are built inline at every call site
	if r == nil {
		return
	}

	if o.Remediation == "" {
		o.Remediation = findings.RemediationNone
	}

	r.Outcomes = append(r.Outcomes, o)
}

// statusOf maps v1's actionable boolean to a status, for rules whose
// reason is not yet known at the call site.
func statusOf(actionable bool) findings.Status {
	if actionable {
		return findings.StatusNonCompliant
	}

	return findings.StatusCompliant
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

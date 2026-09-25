// Package findings is the v2 finding vocabulary (DESIGN-0025 § Finding
// model): the status, reason and remediation facets and the typed
// evidence recorded for each reason.
//
// It is a leaf package. The checker produces these values, the store
// persists them and the API renders them, so it imports nothing from the
// rest of the module.
package findings

// Status answers "is the rule met on the default branch?".
type Status string

// Status values. They match the findings.status CHECK constraint.
const (
	StatusCompliant     Status = "compliant"
	StatusNonCompliant  Status = "non_compliant"
	StatusNotApplicable Status = "not_applicable"
	// StatusUnknown means the rule could not be evaluated, for example
	// because a gate referee failed.
	StatusUnknown Status = "unknown"
)

// Reason explains a status. It is empty when the status is compliant.
type Reason string

// Reason codes. Each has exactly one evidence shape.
const (
	ReasonNone             Reason = ""
	ReasonFileMissing      Reason = "file_missing"
	ReasonAssertionFailed  Reason = "assertion_failed"
	ReasonContentDiffers   Reason = "content_differs"
	ReasonForbiddenPresent Reason = "forbidden_present"
	ReasonSettingMismatch  Reason = "setting_mismatch"
	ReasonRulesetMissing   Reason = "ruleset_missing"
	ReasonRulesetMismatch  Reason = "ruleset_mismatch"
	ReasonOutOfScopePolicy Reason = "out_of_scope_policy"
	ReasonOutOfScopeRule   Reason = "out_of_scope_rule"
	ReasonIgnoredGlobal    Reason = "ignored_global"
	ReasonIgnoredRule      Reason = "ignored_rule"
	ReasonGateClosed       Reason = "gate_closed"
	ReasonBranchMissing    Reason = "branch_missing"
	ReasonEmptyRepository  Reason = "empty_repository"
	ReasonGateError        Reason = "gate_error"
	// ReasonMigratedFromV1 marks a finding backfilled from v1's
	// rule_state: v1 knew the rule failed but not why. The first v2
	// check replaces it.
	ReasonMigratedFromV1 Reason = "migrated_from_v1"
)

// Remediation records what repo-guardian did or is doing about a
// finding.
type Remediation string

// Remediation values. They match the findings.remediation CHECK
// constraint.
const (
	RemediationNone Remediation = "none"
	// RemediationPROpen means our reconcile-branch PR is open.
	RemediationPROpen Remediation = "pr_open"
	// RemediationForeignPR means a human PR the rule yields to is open.
	RemediationForeignPR Remediation = "foreign_pr"
	// RemediationApplied means a setting or ruleset was fixed in place
	// during this check.
	RemediationApplied Remediation = "applied"
	RemediationDryRun  Remediation = "dry_run"
	// RemediationDisabled is a setting rule with remediate = false.
	RemediationDisabled Remediation = "disabled"
)

// EvidenceVersion is the version stamped into findings.evidence_version.
// A reader that sees a higher version renders the reason and skips the
// evidence.
const EvidenceVersion = 1

// ClipRunes is the rune limit for free-text evidence such as assertion
// messages and error strings.
const ClipRunes = 1024

// Clip returns s shortened to at most maxRunes runes. A clipped string
// ends in '…' so readers can tell it was shortened. It counts runes,
// not bytes, so it never splits a UTF-8 sequence. maxRunes <= 0
// returns "".
func Clip(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}

	rs := []rune(s)
	if len(rs) <= maxRunes {
		return s
	}

	return string(rs[:maxRunes-1]) + "…"
}

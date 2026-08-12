package monitoring

import "slices"

// Mechanism is a configured feature that is the sole producer of one or
// more metric series.
//
// The definition is deliberately narrow, and the narrowness is the
// point. If a feature is off, its series never appears, so a panel over
// it renders permanently empty and an alert over it either never fires
// or — worse, when the expression involves absent() or a ratio — fires
// forever. That is not hypothetical: INV-0012 finding A was an alert
// watching a counter with no producer, which survived for months
// because a never-firing alert looks exactly like a healthy system.
//
// The corollary is the rule for adding members: a feature that
// instruments nothing is NOT a mechanism, however prominent it is in
// the config. label_sync, workflow_sync and the branch_protection
// reconciler are all real, configurable features that emit no metrics
// today, so they gate nothing and are absent from this list. Adding
// them "for symmetry" would invite a panel that is empty by
// construction. If Phase 6 wants a label-sync panel, the metric has to
// exist first.
type Mechanism string

// The mechanisms, each annotated with the series it is the sole
// producer of. Keep this list and the comment in sync — the comment is
// the justification for membership.
const (
	// MechanismFileRules gates files_missing_total, prs_created_total,
	// open_prs_by_rule and the repos_actionable/repos_tracked posture
	// pair. Effectively always on, but a config can disable every file
	// rule, and then the compliance panels have nothing to chart.
	MechanismFileRules Mechanism = "file_rules"

	// MechanismAbsentRules gates files_forbidden_present_total. Only
	// check = "absent" rules produce it (IMPL-0019).
	MechanismAbsentRules Mechanism = "absent_rules"

	// MechanismWhenGates gates rule_gate_closed_total, produced only by
	// rules carrying a when { rule_satisfied } block.
	MechanismWhenGates Mechanism = "when_gates"

	// MechanismSettingRules gates settings_checked_total and
	// settings_mismatched_total.
	MechanismSettingRules Mechanism = "setting_rules"

	// MechanismSettingRemediation gates settings_remediated_total and
	// the SettingRemediationChurn alert. A setting rule that only
	// reports never produces the remediation counter.
	MechanismSettingRemediation Mechanism = "setting_remediation"

	// MechanismBranchProtectionRules gates
	// branch_protection_checked_total.
	MechanismBranchProtectionRules Mechanism = "branch_protection_rules"

	// MechanismBranchProtectionRemediation gates
	// branch_protection_remediated_total and the
	// BranchProtectionChurn alert.
	MechanismBranchProtectionRemediation Mechanism = "branch_protection_remediation"

	// MechanismCustomProperties gates property_schema_missing,
	// custom_property_missing_schema_total,
	// custom_property_cleared_total and catalog_parse_failed_total —
	// so it gates the PropertySchemaMissing and CatalogParseFailures
	// alerts and the E4 catalog-parse log panel.
	//
	// Note the schema preflight runs in BOTH api and github-action
	// modes, so PropertySchemaMissing gates on the reconciler being
	// attached, not on the mode. Gating it on the mode is the easy
	// thing to get backwards and would silently drop the alert for
	// every api-mode deployment.
	MechanismCustomProperties Mechanism = "custom_properties"

	// MechanismCustomPropertiesGHA gates properties_prs_created_total,
	// which only github-action mode produces, and therefore the
	// PropertiesPRBurst alert.
	MechanismCustomPropertiesGHA Mechanism = "custom_properties_gha"

	// MechanismStrictScope gates out_of_scope_total and the
	// RuleNeverApplies alert. It also decides whether per-org rows are
	// declarable at all — see Model.Strict.
	MechanismStrictScope Mechanism = "strict_scope"

	// MechanismIgnoreLists gates ignored_total.
	MechanismIgnoreLists Mechanism = "ignore_lists"

	// MechanismAutoClosePR gates prs_closed_total{reason="satisfied"},
	// the convergence-rate panel's only source. With auto-close off,
	// StaleOpenPRs also means something different and its threshold is
	// wrong — this is one of the mechanisms that RETUNES rather than
	// merely adds.
	MechanismAutoClosePR Mechanism = "auto_close_pr"

	// MechanismOrphanCleanup gates pr_orphan_left_total.
	MechanismOrphanCleanup Mechanism = "orphan_cleanup"

	// MechanismRepoParking gates the archived and fork reasons on
	// repos_parked_total / repos_unmeasurable. The access_denied reason
	// needs no mechanism — an installation can lose read access at any
	// time regardless of config, which is why RepoAccessDenied is
	// unconditional (INV-0015).
	MechanismRepoParking Mechanism = "repo_parking"

	// MechanismDryRun is INVERTED: it does not enable a series, it
	// SUPPRESSES prs_created_total. A generator that only knows how to
	// add alerts will page a dry-run deployment forever with
	// "no PRs created" expressions, so consumers must be able to ask.
	MechanismDryRun Mechanism = "dry_run"
)

// Mechanisms is the set in use.
type Mechanisms map[Mechanism]struct{}

// Has reports whether m is configured.
func (ms Mechanisms) Has(m Mechanism) bool {
	_, ok := ms[m]

	return ok
}

// HasAny reports whether any of ms is configured. An empty argument
// list returns true: an artifact requiring no mechanism is
// unconditional, which is the common case and must not be filtered out
// by an empty-slice check.
func (ms Mechanisms) HasAny(want ...Mechanism) bool {
	if len(want) == 0 {
		return true
	}

	for _, m := range want {
		if ms.Has(m) {
			return true
		}
	}

	return false
}

// Sorted returns the configured mechanisms in a stable order.
func (ms Mechanisms) Sorted() []Mechanism {
	out := make([]Mechanism, 0, len(ms))
	for m := range ms {
		out = append(out, m)
	}

	slices.Sort(out)

	return out
}

// add records a mechanism.
func (ms Mechanisms) add(m Mechanism) { ms[m] = struct{}{} }

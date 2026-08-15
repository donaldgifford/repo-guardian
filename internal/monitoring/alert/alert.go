// Package alert is the alert catalogue, authored as data so the
// generator can emit only the alerts whose mechanism is configured.
// See DESIGN-0022 §Config-generated monitoring and IMPL-0023 task 5.3.
//
// # Why the catalogue is data
//
// An alert whose metric has no producer does not fail loudly. It sits
// there never firing, and a never-firing alert looks exactly like a
// healthy system — which is how INV-0012 finding A's alert survived for
// months watching a counter nothing incremented. Making the gating a
// field means "which alerts did we skip, and why" is answerable, and
// means a predicate cannot be forgotten for one alert out of
// twenty-five.
//
// # Import discipline
//
// Imports internal/monitoring and the standard library. It must NOT
// import internal/monitoring/dashboard: the whole point of the
// three-package split is that if DESIGN-0022 OQ5's escape hatch fires
// and the Grafana SDK moves out of this binary, `monitoring generate
// --format k8s` keeps emitting the PrometheusRule.
package alert

import (
	"fmt"
	"strings"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/monitoring"
)

// Severities.
const (
	SeverityWarning  = "warning"
	SeverityCritical = "critical"
)

// Spec is one alerting rule.
type Spec struct {
	// Name is the alert name, conventionally RepoGuardian-prefixed.
	Name string

	// Group is the PrometheusRule group it belongs to.
	Group string

	// Expr is the PromQL expression.
	Expr string

	// Window is the range-vector window Expr uses, carried separately
	// so the window-versus-For invariant is checkable without parsing
	// PromQL. Zero for expressions over instant vectors only.
	Window time.Duration

	// For is the pending period.
	For time.Duration

	Severity    string
	Summary     string
	Description string

	// Requires gates emission: the alert ships only if at least one of
	// these mechanisms is configured. Empty means unconditional.
	Requires []monitoring.Mechanism

	// Excludes suppresses emission when any of these is configured.
	//
	// The inverted case the plan did not account for. dry_run does not
	// enable a series, it SUPPRESSES prs_created_total — so a
	// PR-shaped alert on a dry-run deployment is empty by construction,
	// and one shaped "no PRs created" would page forever.
	Excludes []monitoring.Mechanism
}

// Catalogue returns every alert repo-guardian knows how to emit.
//
// Ordered by group then name, so the generated manifest is stable for
// the drift gate without a sort at emit time.
func Catalogue() []Spec {
	specs := make([]Spec, 0, 32)
	specs = append(specs, coreSpecs()...)
	specs = append(specs, mechanismSpecs()...)
	specs = append(specs, infraSpecs()...)

	return specs
}

// coreSpecs are the always-on service-health alerts.
func coreSpecs() []Spec {
	const group = "repo-guardian"

	return []Spec{
		{
			Name:     "RepoGuardianNoRepoChecks",
			Group:    group,
			Expr:     `sum(increase(repo_guardian_repos_checked_total[2h])) == 0`,
			Window:   2 * time.Hour,
			For:      30 * time.Minute,
			Severity: SeverityWarning,
			Summary:  "repo-guardian has checked no repositories in 2 hours",
			Description: "The sweep, the queue, or the worker pool has stopped. " +
				"Check scheduler_is_leader and queue_depth first.",
		},
		{
			Name:  "RepoGuardianHighErrorRate",
			Group: group,
			Expr: "(\n" +
				"  sum(rate(repo_guardian_errors_total[15m]))\n" +
				"  /\n" +
				"  clamp_min(sum(rate(repo_guardian_repos_checked_total[15m])), 1)\n" +
				") > 0.10",
			Window:      15 * time.Minute,
			For:         10 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "More than 10% of repository checks are failing",
			Description: "clamp_min keeps the ratio defined when no check has completed in the window.",
		},
		{
			Name:  "RepoGuardianAllChecksFailing",
			Group: group,
			Expr: "(\n" +
				"  sum(rate(repo_guardian_errors_total[30m])) > 0\n" +
				"  and\n" +
				"  sum(rate(repo_guardian_repos_checked_total[30m])) == 0\n" +
				")",
			Window:   30 * time.Minute,
			For:      15 * time.Minute,
			Severity: SeverityCritical,
			Summary:  "Every repository check is failing",
			Description: "Errors are being recorded and no check is completing. " +
				"Usually credentials, connectivity, or a policy that fails to load.",
		},
		{
			Name:        "RepoGuardianSlowChecks",
			Group:       group,
			Expr:        `histogram_quantile(0.99, sum(rate(repo_guardian_check_duration_seconds_bucket[15m])) by (le)) > 30`,
			Window:      15 * time.Minute,
			For:         15 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "p99 repository check duration is above 30s",
			Description: "Usually GitHub API latency or a repository with an unusually large file set.",
		},
		{
			Name:     "RepoGuardianNoWebhooks",
			Group:    group,
			Expr:     `increase(repo_guardian_webhook_received_total[24h]) == 0`,
			Window:   24 * time.Hour,
			For:      time.Hour,
			Severity: SeverityWarning,
			Summary:  "No webhook has been received in 24 hours",
			Description: "Either the App's webhook delivery is broken or the endpoint is unreachable. " +
				"Check the App's recent deliveries in GitHub.",
		},
		{
			Name:  "RepoGuardianWebhookRejectionsHigh",
			Group: group,
			// Window widened from 15m to match `for` — INV-0012 finding
			// E. A window shorter than the pending period cannot hold a
			// condition true across it when the source is sparse, and
			// webhook rejections are sparse by nature.
			Expr:     `sum by (reason) (rate(repo_guardian_webhook_rejected_total[30m])) > 0.1`,
			Window:   30 * time.Minute,
			For:      30 * time.Minute,
			Severity: SeverityWarning,
			Summary:  "Webhook requests are being rejected",
			Description: "Signature validation failures (401s) — usually a wrong or rotated webhook secret. " +
				"Source-IP rejections happen at the operator's edge layer, not here (DESIGN-0023).",
		},
		{
			Name:  "RepoGuardianPostureExportStalled",
			Group: group,
			// The liveness signal for every posture gauge, which have no
			// other heartbeat: a leader whose store reads all fail keeps
			// serving its last successful values indefinitely, and a
			// dashboard reading them cannot tell "the fleet is stable"
			// from "nothing has updated in six hours". Alert on the
			// absence of ok increments, never on the gauges.
			Expr:     `sum(increase(repo_guardian_posture_export_total{outcome="ok"}[1h])) == 0`,
			Window:   time.Hour,
			For:      30 * time.Minute,
			Severity: SeverityCritical,
			Summary:  "The posture exporter has not completed a successful tick in an hour",
			Description: "Every compliance gauge is now stale but still being served, so dashboards " +
				"show old numbers as if they were current. Check the leader and the store.",
		},
	}
}

// mechanismSpecs are the alerts gated on a configured feature.
func mechanismSpecs() []Spec {
	return append(prSpecs(), propertySpecs()...)
}

func prSpecs() []Spec {
	const group = "repo-guardian.pr-convergence"

	return []Spec{
		{
			Name:     "RepoGuardianPRBurst",
			Group:    group,
			Expr:     `sum(increase(repo_guardian_prs_created_total[1h])) > 50`,
			Window:   time.Hour,
			For:      5 * time.Minute,
			Severity: SeverityWarning,
			Summary:  "More than 50 PRs opened in an hour",
			Description: "Usually a policy change that made a rule actionable fleet-wide. " +
				"Verify it was intended before the PRs land.",
			Requires: []monitoring.Mechanism{monitoring.MechanismFileRules},
			// dry_run suppresses prs_created_total entirely, so this
			// alert is empty by construction on a dry-run deployment.
			Excludes: []monitoring.Mechanism{monitoring.MechanismDryRun},
		},
		{
			Name:     "RepoGuardianStaleOpenPRs",
			Group:    group,
			Expr:     `max(repo_guardian_open_prs_by_rule{age_bucket="30d+"}) > 0`,
			For:      time.Hour,
			Severity: SeverityWarning,
			Summary:  "A repo-guardian PR has been open for over 30 days",
			Description: "Nobody is merging them, or they are stuck. Note this threshold assumes " +
				"auto-close is on: with auto_close_pr = false a satisfied PR stays open by design " +
				"and will trip this eventually.",
			Requires: []monitoring.Mechanism{monitoring.MechanismFileRules},
			Excludes: []monitoring.Mechanism{monitoring.MechanismDryRun},
		},
		{
			Name:     "RepoGuardianPRDrift",
			Group:    group,
			Expr:     `sum(rate(repo_guardian_pr_open_with_empty_actionable_total[1h])) > 0`,
			Window:   time.Hour,
			For:      30 * time.Minute,
			Severity: SeverityWarning,
			Summary:  "Open PRs have no actionable rule left",
			Description: "Every rule is satisfied on the default branch but the PR is still open, " +
				"so auto-close is not converging. This is the INV-0005 drift surface.",
			// Gated on auto-close, and this is the subtle one: with
			// auto-close OFF, drift is the DESIGNED behaviour and the
			// alert would fire permanently on a correctly-configured
			// deployment. An alert that is always firing is an alert
			// nobody reads.
			Requires: []monitoring.Mechanism{monitoring.MechanismAutoClosePR},
			Excludes: []monitoring.Mechanism{monitoring.MechanismDryRun},
		},
		{
			Name:        "RepoGuardianSettingRemediationChurn",
			Group:       group,
			Expr:        "sum by (rule_name, org) (\n  rate(repo_guardian_settings_remediated_total[6h])\n) > 0.01",
			Window:      6 * time.Hour,
			For:         6 * time.Hour,
			Severity:    SeverityWarning,
			Summary:     "A repository setting is being remediated repeatedly",
			Description: "repo-guardian and something else are fighting over the same setting.",
			Requires:    []monitoring.Mechanism{monitoring.MechanismSettingRemediation},
		},
		{
			Name:        "RepoGuardianBranchProtectionChurn",
			Group:       group,
			Expr:        "sum by (rule_name, org) (\n  rate(repo_guardian_branch_protection_remediated_total[6h])\n) > 0.01",
			Window:      6 * time.Hour,
			For:         6 * time.Hour,
			Severity:    SeverityWarning,
			Summary:     "A branch protection ruleset is being remediated repeatedly",
			Description: "repo-guardian and something else are fighting over the same ruleset.",
			Requires:    []monitoring.Mechanism{monitoring.MechanismBranchProtectionRemediation},
		},
		{
			Name:  "RepoGuardianRuleNeverApplies",
			Group: group,
			Expr: "sum by (org) (increase(repo_guardian_out_of_scope_total{level=\"rule\"}[24h])) > 0\n" +
				"and\n" +
				"sum by (org) (increase(repo_guardian_files_missing_total[24h])) == 0",
			Window:   24 * time.Hour,
			For:      2 * time.Hour,
			Severity: SeverityWarning,
			Summary:  "An org has rules skipped for scope and no rule applying",
			Description: "Probably a scope block that matches nothing. The repositories are being " +
				"processed and every rule is being skipped.",
			// out_of_scope_total has no producer outside strict mode.
			Requires: []monitoring.Mechanism{monitoring.MechanismStrictScope},
		},
	}
}

// firstOrIncrease builds "this counter moved in the window, OR it just
// appeared for the first time".
//
// increase() cannot see a counter's first-ever increment. A CounterVec
// child does not exist until something calls WithLabelValues, so its
// first scrape lands at 1 and every scrape after it also reads 1 —
// there is no earlier sample to subtract, and increase() over that
// window is 0. For an alert whose threshold is "> 0 of a rare event",
// that is precisely the occurrence worth alerting on, and it is the one
// occurrence the expression structurally cannot catch.
//
// The second disjunct fixes it: a series present now and absent one
// window ago is new, and its value is its whole history.
//
// Neither disjunct aggregates, so the alert keeps the counter's own
// labels — `org` and `property` on the schema alert, `org` and
// `installation_id` on the parking one. Those are the whole value of
// the notification, and a sum() would throw them away.
//
// Only for RARE counters where the FIRST event matters. On a busy
// counter the second disjunct is noise, and on a threshold alert
// (`> 50`) it would be wrong outright — a brand-new counter at 1 is not
// a burst.
func firstOrIncrease(selector, window string) string {
	series, matchers, _ := strings.Cut(selector, "{")
	if matchers != "" {
		matchers = "{" + matchers
	}

	sel := series + matchers

	return "increase(" + sel + "[" + window + "]) > 0\n" +
		"or\n" +
		"(" + sel + " unless " + sel + " offset " + window + ") > 0"
}

func propertySpecs() []Spec {
	const group = "repo-guardian.custom-property-sync"

	return []Spec{
		{
			Name:     "RepoGuardianPropertySchemaMissing",
			Group:    group,
			Expr:     firstOrIncrease("repo_guardian_custom_property_missing_schema_total", "1h"),
			Window:   time.Hour,
			For:      5 * time.Minute,
			Severity: SeverityWarning,
			Summary:  "A mapped custom property is not defined in the org schema",
			Description: "The property is dropped from the PATCH and the rest of the payload still " +
				"syncs. Define it in the org's custom-property schema.",
			// Gates on the RECONCILER, not on its mode: the schema
			// preflight runs in both api and github-action mode.
			// Gating on mode silently drops this alert for every
			// api-mode deployment.
			Requires: []monitoring.Mechanism{monitoring.MechanismCustomProperties},
		},
		{
			Name:     "RepoGuardianCatalogParseFailures",
			Group:    group,
			Expr:     firstOrIncrease("repo_guardian_catalog_parse_failed_total", "1h"),
			Window:   time.Hour,
			For:      5 * time.Minute,
			Severity: SeverityWarning,
			Summary:  "A catalog-info.yaml failed to parse",
			Description: "The reconcile is skipped rather than clearing the managed properties " +
				"(INV-0011 A1), so the repository keeps its last-known values.",
			Requires: []monitoring.Mechanism{monitoring.MechanismCustomProperties},
		},
		// RepoGuardianPropertiesPRBurst was here until IMPL-0023 Phase 7.
		// It watched repo_guardian_properties_prs_created_total, an
		// unlabelled counter that existed only because reconciler PRs
		// were counted separately from engine PRs. Now that they are not,
		// this alert would be RepoGuardianPRBurst with a different name
		// and the same 50-per-hour threshold.
		//
		// The coverage question was checked rather than assumed: PRBurst
		// requires MechanismFileRules, and a custom_properties reconciler
		// is only ever attached to a file rule, so a policy that could
		// have triggered the old alert always engages the new one.
	}
}

// infraGroup is the group both halves of the infrastructure catalogue
// render into; the split below is a function-length concession, not a
// grouping decision, and the two halves must stay in one group.
const infraGroup = "repo-guardian.multi-replica"

// infraSpecs are the queue, scheduler, store and rate-limit alerts.
func infraSpecs() []Spec {
	return append(queueSpecs(), runtimeSpecs()...)
}

// queueSpecs are the alerts over the durable queue.
func queueSpecs() []Spec {
	const group = infraGroup

	return []Spec{
		{
			Name:        "RepoGuardianQueueDepthHigh",
			Group:       group,
			Expr:        `max(repo_guardian_queue_depth{queue="jobs"}) > 1000`,
			For:         10 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "Job queue depth is above 1000",
			Description: "Workers are not keeping up. Scale replicas or raise worker concurrency.",
		},
		{
			Name:     "RepoGuardianReaperRequeues",
			Group:    group,
			Expr:     `rate(repo_guardian_queue_reaped_total[15m]) > 0.1`,
			Window:   15 * time.Minute,
			For:      15 * time.Minute,
			Severity: SeverityWarning,
			Summary:  "Jobs are being reaped and requeued",
			Description: "Workers are dying mid-job or exceeding JOB_ACK_TIMEOUT. Every reap " +
				"duplicates work against an API budget that is probably already tight.",
		},
		{
			Name:     "RepoGuardianQueueBackpressure",
			Group:    group,
			Expr:     `max(repo_guardian_queue_delayed_depth) > 100`,
			For:      30 * time.Minute,
			Severity: SeverityWarning,
			Summary:  "More than 100 jobs are parked in the delayed set",
			Description: "Sustained rate-limit backpressure (IMPL-0022). Jobs are deferring rather " +
				"than failing, which is correct, but the fleet is not converging.",
		},
		{
			Name:  "RepoGuardianJobsExhausted",
			Group: group,
			// Summed, unlike the label-preserving firstOrIncrease
			// alerts: this counter's labels identify a queue, not a
			// tenant, so one alert for the fleet is what an operator
			// wants. Same first-increment reasoning either way.
			Expr: "sum(increase(repo_guardian_queue_attempts_exhausted_total[1h])) > 0\n" +
				"or\n" +
				"sum(repo_guardian_queue_attempts_exhausted_total " +
				"unless repo_guardian_queue_attempts_exhausted_total offset 1h) > 0",
			Window:      time.Hour,
			For:         5 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "A job hit MAX_JOB_ATTEMPTS and was dropped",
			Description: "The second disjunct catches the first-ever increment; see firstOrIncrease.",
		},
	}
}

// runtimeSpecs are the scheduler, store and rate-limit alerts.
func runtimeSpecs() []Spec {
	const group = infraGroup

	return []Spec{
		{
			Name:     "RepoGuardianNoSchedulerLeader",
			Group:    group,
			Expr:     `max(repo_guardian_scheduler_is_leader{name="stale-sweep"}) == 0`,
			For:      5 * time.Minute,
			Severity: SeverityCritical,
			Summary:  "No replica holds the stale-sweep leader lock",
			Description: "Nothing is enqueuing stale repositories. Aggregate with max, never sum: " +
				"during failover both replicas can briefly hold a series.",
		},
		{
			Name:  "RepoGuardianStoreQueryErrors",
			Group: group,
			// Window widened from 5m to match `for` — INV-0012 finding E.
			Expr: "sum(rate(repo_guardian_store_query_seconds_count{outcome=\"error\"}[10m]))\n" +
				"/\n" +
				"sum(rate(repo_guardian_store_query_seconds_count[10m])) > 0.1",
			Window:      10 * time.Minute,
			For:         10 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "More than 10% of store queries are failing",
			Description: "Postgres is unreachable, saturated, or the schema is behind the binary.",
		},
		{
			Name:     "RepoGuardianRateLimitNearExhaustion",
			Group:    group,
			Expr:     `min(repo_guardian_rate_limit_remaining) < 200`,
			For:      5 * time.Minute,
			Severity: SeverityWarning,
			Summary:  "GitHub API budget is nearly exhausted",
			Description: "Fed solely by the sweep's per-installation sample. If that sampling is " +
				"ever removed this series goes unfed and the alert silently stops working.",
		},
		{
			Name:     "RepoGuardianRateLimitThrottling",
			Group:    group,
			Expr:     `sum(increase(repo_guardian_queue_delayed_total{reason="rate_limit"}[1h])) > 10`,
			Window:   time.Hour,
			For:      30 * time.Minute,
			Severity: SeverityWarning,
			Summary:  "Jobs are being deferred for rate limiting",
			Description: "The IMPL-0022 delayed-requeue path is active. Work is not being lost, " +
				"but throughput is capped by the API budget.",
		},
		{
			Name:  "RepoGuardianRepoAccessDenied",
			Group: group,
			// The reason selector is LOAD-BEARING. repos_parked_total
			// also counts routine archived and fork parks, which happen
			// on every normal onboarding sweep, so the same expression
			// without the selector pages on healthy behaviour.
			Expr:     firstOrIncrease(`repo_guardian_repos_parked_total{reason="access_denied"}`, "1h"),
			Window:   time.Hour,
			For:      15 * time.Minute,
			Severity: SeverityWarning,
			Summary:  "A repository was parked because the installation cannot read it",
			Description: "The App lost access, or was never granted it. The repository is excluded " +
				"from every compliance number until discovery sees it again (INV-0015).",
			// Deliberately unconditional: an installation can lose read
			// access at any time regardless of configuration.
		},
	}
}

// Generate returns the alerts whose mechanisms the model configures,
// plus the ones that were skipped and why.
//
// Reporting the skips is not decoration. "Which alerts are not in this
// manifest" is the question an operator asks after an incident nothing
// paged for, and deriving it by hand from a config file is exactly the
// work this generator exists to remove.
func Generate(m *monitoring.Model) (kept []Spec, skipped []Skip) {
	if m == nil {
		return nil, nil
	}

	all := Catalogue()
	for i := range all {
		s := &all[i]

		if reason, ok := s.skipReason(m.Mechanisms); ok {
			skipped = append(skipped, Skip{Alert: s.Name, Reason: reason})

			continue
		}

		kept = append(kept, *s)
	}

	return kept, skipped
}

// Skip records an alert that was not emitted.
type Skip struct {
	Alert  string
	Reason string
}

// skipReason reports whether the spec should be omitted.
func (s *Spec) skipReason(ms monitoring.Mechanisms) (string, bool) {
	for _, m := range s.Excludes {
		if ms.Has(m) {
			return fmt.Sprintf("suppressed by mechanism %q", m), true
		}
	}

	if len(s.Requires) > 0 && !ms.HasAny(s.Requires...) {
		return fmt.Sprintf("requires one of %v, none configured", s.Requires), true
	}

	return "", false
}

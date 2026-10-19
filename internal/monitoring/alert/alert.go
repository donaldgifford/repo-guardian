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
//
// Nothing in it is mechanism-gated: every alert reads a series that is
// absent, not zero, on a deployment that never remediates, so an
// ungated remediation alert stays silent there instead of paging.
// Requires and Excludes stay for an alert whose silence would mislead.
func Catalogue() []Spec {
	specs := make([]Spec, 0, 20)
	specs = append(specs, coreSpecs()...)
	specs = append(specs, remediationSpecs()...)
	specs = append(specs, infraSpecs()...)
	specs = append(specs, temporalSpecs()...)

	return specs
}

// coreSpecs are the always-on service-health and evaluation alerts.
func coreSpecs() []Spec {
	const group = "repo-guardian"

	return []Spec{
		{
			Name:  "RepoGuardianEvaluationErrorRate",
			Group: group,
			// The `> 0` on the denominator leaves an idle evaluator with
			// no result instead of a division by zero; an evaluator
			// recording only errors reads 1 and fires.
			Expr: "(\n" +
				"  sum(rate(repo_guardian_evaluations_total{outcome=\"error\"}[15m]))\n" +
				"  /\n" +
				"  (sum(rate(repo_guardian_evaluations_total[15m])) > 0)\n" +
				") > 0.10",
			Window:   15 * time.Minute,
			For:      10 * time.Minute,
			Severity: SeverityWarning,
			Summary:  "More than 10% of evaluations are failing",
			Description: "At 100% the evaluator cannot read GitHub or the store at all: usually " +
				"the Evaluation App's credentials, connectivity, or a policy that fails to load.",
		},
		{
			Name:  "RepoGuardianControlEvaluationErrors",
			Group: group,
			// Per (org, control), first increment included: a control
			// that errors on every repository from the moment it is
			// assigned creates its series at a non-zero value.
			Expr:     firstOrIncrease(`repo_guardian_evaluation_errors_total`, "1h"),
			Window:   time.Hour,
			For:      15 * time.Minute,
			Severity: SeverityWarning,
			Summary:  "A control is returning an error status",
			Description: "The control could not evaluate: a GitHub read it needs failed or was " +
				"refused. One control erroring across an org is usually a missing App " +
				"permission; the E4 logs name the repositories.",
		},
		{
			Name:  "RepoGuardianEvaluationFreshness",
			Group: group,
			// Twice the default EVAL_INTERVAL. The p50 sits near the
			// interval by design, so only a p95 well past it means
			// repositories are waiting longer than their timer.
			Expr: "histogram_quantile(0.95, sum by (le) " +
				"(rate(repo_guardian_evaluation_freshness_seconds_bucket[6h]))) > 172800",
			Window:   6 * time.Hour,
			For:      time.Hour,
			Severity: SeverityWarning,
			Summary:  "Controls are re-evaluated more than two days apart (p95)",
			Description: "Evaluation is not keeping up with the fleet: check the evaluation queue's " +
				"schedule-to-start latency and budget waits before raising EVALUATOR_CONCURRENCY.",
		},
		{
			Name:  "RepoGuardianStaleEpochDiscards",
			Group: group,
			// A threshold, not a first-increment alert: a policy rollout
			// discards a few results by design (DESIGN-0030 D14).
			Expr:     `sum(increase(repo_guardian_results_discarded_total{reason="stale_epoch"}[2h])) > 100`,
			Window:   2 * time.Hour,
			For:      time.Hour,
			Severity: SeverityWarning,
			Summary:  "Evaluations keep being discarded for a moved assignment epoch",
			Description: "Assignments change between evaluation and record faster than evaluations " +
				"finish — usually a policy being re-activated repeatedly. Evaluation work is " +
				"being thrown away.",
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
// labels — `org` and `control` on the evaluation-errors alert. Those are
// the whole value of the notification, and a sum() would throw them
// away.
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

// remediationGroup holds the remediation hold alerts (IMPL-0030 OQ11).
const remediationGroup = "repo-guardian.remediation"

// remediationSpecs are the holds that need a human. Per OQ11:
// foreign_branch, permission and the Remediation App losing a
// repository alert on their first occurrence; conflict alerts only
// once held a week; pr_cap, stale_branch and cooldown never alert
// (routine, shown in the UI).
func remediationSpecs() []Spec {
	const group = remediationGroup

	return []Spec{
		{
			Name:     "RepoGuardianRemediationAppUnreachable",
			Group:    group,
			Expr:     firstOrIncrease(`repo_guardian_remediation_blocked_total{reason=~"remediation_app_no_access|remediation_app_suspended"}`, "1h"),
			Window:   time.Hour,
			Severity: SeverityWarning,
			Summary:  "The Remediation App cannot reach repositories it is assigned to remediate",
			Description: "Assignments in remediate mode were downgraded to evaluate because the " +
				"Remediation App is not installed on the repository or its installation is " +
				"suspended. Install or unsuspend the App, or set the org's mode to evaluate.",
		},
		{
			Name:  "RepoGuardianRemediationBlocked",
			Group: group,
			// Keeps org and reason: which of the two holds it is decides
			// who acts.
			Expr:     firstOrIncrease(`repo_guardian_remediation_blocked_total{reason=~"foreign_branch|permission"}`, "1h"),
			Window:   time.Hour,
			Severity: SeverityWarning,
			Summary:  "A remediation is held on a foreign branch or a missing permission",
			Description: "foreign_branch: someone else pushed to the remediation branch, so " +
				"repo-guardian will not touch it until the branch is restored or deleted. " +
				"permission: GitHub refused the Remediation App's write; the App lacks a " +
				"permission the control needs. The E4 logs name the repositories.",
		},
		{
			Name:  "RepoGuardianRemediationConflictHeld",
			Group: group,
			// Published by every api replica from the store, so max by.
			Expr:     `max by (org) (repo_guardian_remediation_holds{reason="conflict",age_bucket=~"7-30d|30d[+]"}) > 0`,
			For:      time.Hour,
			Severity: SeverityWarning,
			Summary:  "A remediation has been held on a conflict for over a week",
			Description: "Two controls own one resource, or a human change keeps conflicting " +
				"with the remediation. A short conflict resolves itself; a week means the " +
				"policy needs an owner decision.",
		},
	}
}

// infraGroup is the group the store and rate-limit alerts render into.
const infraGroup = "repo-guardian.multi-replica"

// infraSpecs are the store and rate-limit alerts.
func infraSpecs() []Spec {
	const group = infraGroup

	return []Spec{
		{
			Name:  "RepoGuardianStoreErrors",
			Group: group,
			// Window widened from 5m to match `for` — INV-0012 finding E.
			Expr: "sum(rate(repo_guardian_store_query_seconds_count{outcome=\"error\"}[10m]))\n" +
				"/\n" +
				"(sum(rate(repo_guardian_store_query_seconds_count[10m])) > 0) > 0.1",
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
			Description: "Set by every evaluation from the response headers. The installation budget " +
				"defers evaluations until the reset, so work waits rather than fails.",
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

// temporalGroup holds the worker-health alerts over the Temporal SDK's
// own metrics (Temporal's worker-health guidance). The SDK's
// histograms carry second-scale buckets via temporal.MetricViews. Its
// counters keep their default names (no _total), exported as gauges;
// rate() handles their resets all the same.
//
// They group by task_queue, never namespace: the SDK labels its series
// with the Temporal namespace, and a ServiceMonitor scrape renames that
// to exported_namespace because namespace is the pod's.
const temporalGroup = "repo-guardian.temporal"

// temporalSpecs are the Temporal worker-health alerts.
func temporalSpecs() []Spec {
	const group = temporalGroup

	return []Spec{
		{
			Name:  "RepoGuardianTemporalActivityBacklog",
			Group: group,
			Expr: "histogram_quantile(0.99, sum by (le, task_queue) " +
				"(rate(temporal_activity_schedule_to_start_latency_seconds_bucket[30m]))) > 60",
			Window:   30 * time.Minute,
			For:      30 * time.Minute,
			Severity: SeverityWarning,
			Summary:  "Activities wait more than a minute (p99) for a worker slot",
			Description: "Far above Temporal's generic 200ms on purpose: evaluations are GitHub " +
				"rate-limit bound and a policy rollout queues work by design. Minutes of " +
				"sustained wait mean the fleet cannot keep up; add evaluator replicas (or KEDA " +
				"maxReplicas) or EVALUATOR_CONCURRENCY, within the GitHub budget.",
		},
		{
			Name:     "RepoGuardianTemporalStickyCacheEvictions",
			Group:    group,
			Expr:     `sum(rate(temporal_sticky_cache_total_forced_eviction[30m])) > 1`,
			Window:   30 * time.Minute,
			For:      30 * time.Minute,
			Severity: SeverityWarning,
			Summary:  "Workers are force-evicting cached workflows",
			Description: "More long-lived workflows are active than the sticky cache holds " +
				"(one EvaluationWorkflow per repository), so evicted workflows replay their " +
				"history on every task. Add worker replicas; each has its own cache.",
		},
		{
			Name:  "RepoGuardianTemporalWorkerSlotsExhausted",
			Group: group,
			// max: fires only when no pod has a free slot. One busy pod
			// is normal; the whole fleet busy is saturation.
			Expr: `max by (task_queue, worker_type) ` +
				`(temporal_worker_task_slots_available{worker_type=~"WorkflowWorker|ActivityWorker"}) == 0`,
			For:      15 * time.Minute,
			Severity: SeverityWarning,
			Summary:  "No worker has a free task slot",
			Description: "Every worker's slots are in use, so new tasks queue. Add worker " +
				"replicas, or raise EVALUATOR_CONCURRENCY within the GitHub budget.",
		},
		{
			Name:  "RepoGuardianTemporalWorkflowTaskLatency",
			Group: group,
			Expr: "histogram_quantile(0.99, sum by (le, task_queue) " +
				"(rate(temporal_workflow_task_schedule_to_start_latency_seconds_bucket[15m]))) > 0.2",
			Window:   15 * time.Minute,
			For:      15 * time.Minute,
			Severity: SeverityWarning,
			Summary:  "Workflow tasks wait more than 200ms (p99) to start",
			Description: "Temporal's recommended threshold. Workflow tasks are short, so waiting " +
				"means workers are saturated, down, or their build is not the deployment's " +
				"current version (check the worker's deployment readiness check).",
		},
		{
			Name:  "RepoGuardianTemporalWorkflowTaskFailures",
			Group: group,
			Expr: `sum by (task_queue) ` +
				`(rate(temporal_workflow_task_execution_failed[15m])) > 0`,
			Window:   15 * time.Minute,
			For:      15 * time.Minute,
			Severity: SeverityWarning,
			Summary:  "Workflow tasks are failing",
			Description: "A failing workflow task retries forever and its workflow makes no " +
				"progress. Almost always nondeterminism: a workflow change shipped without " +
				"workflow.GetVersion. The worker log names the workflow id.",
		},
		{
			Name:  "RepoGuardianTemporalNoWorkerPollers",
			Group: group,
			// A worker whose pollers all stopped still exports the gauge
			// at zero; a queue with no worker at all exports nothing,
			// which the backlog alerts above catch instead.
			Expr: `max by (task_queue, poller_type) ` +
				`(temporal_num_pollers{poller_type=~"workflow_task|activity_task"}) == 0`,
			For:      10 * time.Minute,
			Severity: SeverityCritical,
			Summary:  "A worker has stopped polling its task queue",
			Description: "The process is up but takes no tasks: usually lost connectivity to the " +
				"frontend or a build that is not the deployment's current version.",
		},
		{
			Name:  "RepoGuardianTemporalUnreachable",
			Group: group,
			Expr: "sum(rate(temporal_long_request_failure[10m]))\n" +
				"/\n" +
				"(sum(rate(temporal_long_request[10m])) > 0) > 0.5",
			Window:   10 * time.Minute,
			For:      10 * time.Minute,
			Severity: SeverityCritical,
			Summary:  "Most Temporal polls are failing",
			Description: "Workers cannot reach the Temporal frontend: check the address, mTLS " +
				"material and the frontend's own health. Nothing evaluates until it recovers.",
		},
	}
}

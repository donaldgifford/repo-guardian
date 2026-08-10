// Package metrics defines Prometheus metrics for repo-guardian observability.
package metrics

import (
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Prometheus label name constants — keep in sync with operator dashboards
// and PromQL recipes in contrib/README.md.
const (
	labelOrg            = "org"
	labelRuleName       = "rule_name"
	labelReason         = "reason"
	labelOutcome        = "outcome"
	labelInstallationID = "installation_id"
	labelProperty       = "property"
)

// All repo-guardian Prometheus metrics.
var (
	// ReposCheckedTotal counts the total number of repositories processed.
	ReposCheckedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_repos_checked_total",
		Help: "Total repositories processed.",
	}, []string{"trigger", labelOrg})

	// PRsCreatedTotal counts the total number of PRs created, by org.
	PRsCreatedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_prs_created_total",
		Help: "Total pull requests created.",
	}, []string{labelOrg})

	// PRsUpdatedTotal counts the total number of existing PRs updated, by org.
	PRsUpdatedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_prs_updated_total",
		Help: "Total existing pull requests updated.",
	}, []string{labelOrg})

	// FilesMissingTotal counts missing files detected, labeled by rule name and org.
	FilesMissingTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_files_missing_total",
		Help: "Missing files detected.",
	}, []string{labelRuleName, labelOrg})

	// FilesForbiddenPresentTotal counts forbidden files detected present
	// on the default branch by an absent-mode file rule (IMPL-0019 /
	// DESIGN-0020). It is the absent-mode analogue of FilesMissingTotal:
	// an actionable absent rule increments this, never FilesMissingTotal.
	FilesForbiddenPresentTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_files_forbidden_present_total",
		Help: "Forbidden files detected present by an absent-mode rule.",
	}, []string{labelRuleName, labelOrg})

	// RuleGateClosedTotal counts file-rule evaluations skipped because a
	// when-gate was closed (IMPL-0019 / DESIGN-0020). reason="not_satisfied"
	// is the ordinary "referee not yet satisfied on default branch" path;
	// reason="error" means the referee evaluation errored and the gate
	// failed closed — the alertable signal that API trouble is silently
	// suppressing rules. Incremented only in the primary evaluation pass
	// (findActionableRules), never in runReconcilers, per the file-rule
	// double-iteration contract.
	RuleGateClosedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_rule_gate_closed_total",
		Help: "File-rule evaluations skipped because a when-gate was closed.",
	}, []string{labelRuleName, labelOrg, labelReason})

	// CheckDurationSeconds records the time to check a single repo.
	CheckDurationSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "repo_guardian_check_duration_seconds",
		Help:    "Time to check a single repository.",
		Buckets: prometheus.DefBuckets,
	})

	// WebhookReceivedTotal counts webhooks received, labeled by event type.
	WebhookReceivedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_webhook_received_total",
		Help: "Webhooks received.",
	}, []string{"event_type"})

	// ErrorsTotal counts errors, labeled by operation and org.
	ErrorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_errors_total",
		Help: "Errors encountered.",
	}, []string{"operation", labelOrg})

	// ReposParkedTotal counts repositories taken out of the sweep, by
	// reason. Its own metric rather than a label on ErrorsTotal, which is
	// pre-existing and widely queried; carrying `reason` from birth costs
	// nothing and keeps one series as parking reasons grow (INV-0015).
	//
	// reason="access_denied" is the actionable one and what the shipped
	// alert watches. archived and fork are routine and expected to climb
	// slowly in any long-lived org.
	ReposParkedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_repos_parked_total",
		Help: "Repositories parked (removed from the stale sweep), by reason.",
	}, []string{labelOrg, "installation_id", "reason"})

	// GitHubRateRemaining tracks the GitHub API rate limit remaining.
	GitHubRateRemaining = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "repo_guardian_github_rate_remaining",
		Help: "GitHub API rate limit remaining.",
	})

	// PropertiesCheckedTotal counts repos where custom properties were evaluated.
	PropertiesCheckedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "repo_guardian_properties_checked_total",
		Help: "Total repositories where custom properties were evaluated.",
	})

	// PropertiesPRsCreatedTotal counts PRs created for custom properties.
	PropertiesPRsCreatedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "repo_guardian_properties_prs_created_total",
		Help: "Total pull requests created for custom properties.",
	})

	// PropertiesSetTotal counts repos where properties were set via API (api mode only).
	PropertiesSetTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "repo_guardian_properties_set_total",
		Help: "Total repositories where custom properties were set via API.",
	})

	// PropertiesAlreadyCorrectTotal counts repos where properties already matched.
	PropertiesAlreadyCorrectTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "repo_guardian_properties_already_correct_total",
		Help: "Total repositories where custom properties already matched desired values.",
	})

	// CustomPropertyClearedTotal counts individual managed custom
	// properties cleared (set to JSON null) because their source
	// annotation was removed from catalog-info.yaml (DESIGN-0019 full
	// state sync). Labeled by org so a spike in clears is auditable per
	// organization.
	CustomPropertyClearedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_custom_property_cleared_total",
		Help: "Total managed custom properties cleared because their source annotation was removed.",
	}, []string{labelOrg})

	// CustomPropertyMissingSchemaTotal counts sync attempts for a
	// managed property that the org's custom-property schema does not
	// define (DESIGN-0019 preflight). Labeled by org and property so
	// operators can see exactly which mapped property needs a schema
	// definition, and how often the mismatch recurs.
	CustomPropertyMissingSchemaTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_custom_property_missing_schema_total",
		Help: "Total sync attempts for a managed custom property absent from the org's property schema.",
	}, []string{labelOrg, labelProperty})

	// PropertySchemaMissing reports whether an org's custom-property
	// schema currently lacks a definition for a managed property:
	// 1 = missing, 0 = defined. Set at each schema-preflight cache
	// refresh (30-minute TTL), which is the only moment the answer is
	// known (DESIGN-0022 finding B — GitHub-owned posture).
	//
	// It is the posture counterpart to CustomPropertyMissingSchemaTotal.
	// The counter answers "how often did we try to sync a property the
	// schema lacks"; it goes quiet when the affected repos stop being
	// reconciled, even though the gap is still there. The gauge answers
	// "is the gap there now", which is the question a compliance
	// dashboard asks.
	//
	// A failed schema fetch leaves these series untouched rather than
	// clearing them: not being able to ask does not mean the answer
	// changed.
	PropertySchemaMissing = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "repo_guardian_property_schema_missing",
		Help: "1 when an org's custom-property schema does not define a managed property, 0 when it does.",
	}, []string{labelOrg, labelProperty})

	// CatalogParseFailedTotal counts custom-properties reconciles
	// skipped because catalog-info.yaml could not be parsed (INV-0011
	// A1). A parse failure is never treated as desired state: the
	// reconcile skips without touching GitHub properties and retries
	// on the next sweep. Labeled by org so a repo publishing malformed
	// catalog-info is attributable per organization.
	CatalogParseFailedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_catalog_parse_failed_total",
		Help: "Total custom-properties reconciles skipped because catalog-info.yaml failed to parse.",
	}, []string{labelOrg})

	// WebhookRejectedTotal counts webhook requests rejected by the IP allowlist.
	WebhookRejectedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_webhook_rejected_total",
		Help: "Webhook requests rejected by IP allowlist.",
	}, []string{labelReason})

	// IgnoredTotal counts repos or rules skipped by ignore lists, by scope and org.
	IgnoredTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_ignored_total",
		Help: "Repos or rules skipped by ignore lists.",
	}, []string{"scope", labelOrg})

	// SettingsCheckedTotal counts setting rules evaluated per rule name and org.
	SettingsCheckedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_settings_checked_total",
		Help: "Setting rules evaluated.",
	}, []string{labelRuleName, labelOrg})

	// SettingsMismatchedTotal counts setting rules that found a mismatch.
	SettingsMismatchedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_settings_mismatched_total",
		Help: "Setting rules that found a mismatch.",
	}, []string{labelRuleName, labelOrg})

	// SettingsRemediatedTotal counts setting rules that were remediated.
	SettingsRemediatedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_settings_remediated_total",
		Help: "Setting rules remediated via API.",
	}, []string{labelRuleName, labelOrg})

	// BranchProtectionCheckedTotal counts branch protection rules evaluated.
	BranchProtectionCheckedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_branch_protection_checked_total",
		Help: "Branch protection rules evaluated.",
	}, []string{labelRuleName, labelOrg})

	// BranchProtectionRemediatedTotal counts branch protection rules remediated.
	BranchProtectionRemediatedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_branch_protection_remediated_total",
		Help: "Branch protection rules remediated via rulesets API.",
	}, []string{labelRuleName, labelOrg})

	// OutOfScopeTotal counts rule evaluations skipped by strict-mode scope.
	// level="policy" means the top-level policy scope rejected the repo
	// (incremented once per enabled rule across all rule types). level="rule"
	// means the per-rule scope rejected the repo (incremented once per rule).
	OutOfScopeTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_out_of_scope_total",
		Help: "Rule evaluations skipped by strict-mode scope, by level (policy or rule) and org.",
	}, []string{"level", labelOrg})

	// StoreQuerySeconds records the time taken by individual Store
	// queries. Labeled by operation (get, update, stale, migrate) and
	// outcome (ok, error). Registered in IMPL-0011 Phase 2; wiring into
	// the postgres Store is deferred to Phase 5 to keep observability
	// changes ring-fenced.
	StoreQuerySeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "repo_guardian_store_query_seconds",
		Help:    "Duration of Store queries in seconds.",
		Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5},
	}, []string{"operation", labelOutcome})

	// QueueDepth tracks the current pending-job count of the work
	// queue, labeled by queue (jobs or in-flight) for the Valkey
	// backend.
	QueueDepth = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "repo_guardian_queue_depth",
		Help: "Pending jobs in the work queue.",
	}, []string{"queue"})

	// QueueDelayedDepth tracks jobs parked in the delayed set awaiting
	// promotion (IMPL-0022). Published by EVERY pod's reaper tick via
	// ZCARD, before the leader lock — a leader-only gauge would go
	// stale on non-leader replicas and pin depth-based alerts firing
	// across a leadership flap.
	QueueDelayedDepth = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "repo_guardian_queue_delayed_depth",
		Help: "Jobs parked in the delayed set awaiting promotion.",
	})

	// QueueEnqueuedTotal counts jobs enqueued by trigger
	// (webhook, sweep, push).
	QueueEnqueuedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_queue_enqueued_total",
		Help: "Total jobs enqueued.",
	}, []string{"trigger"})

	// QueueClaimedTotal counts jobs claimed (BRPOP + ZADD in-flight).
	QueueClaimedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "repo_guardian_queue_claimed_total",
		Help: "Total jobs claimed by a worker.",
	})

	// QueueAckedTotal counts handler returns by outcome (success,
	// error, or deferred). A `success` ack means ZREM in-flight
	// succeeded; an `error` ack means the handler returned an error
	// and the entry was left in-flight for the reaper; a `deferred`
	// ack means the handler returned RetryAfterError and the job
	// moved to the delayed set (IMPL-0022).
	QueueAckedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_queue_acked_total",
		Help: "Total jobs acknowledged by a worker, by outcome.",
	}, []string{labelOutcome})

	// QueueReapedTotal counts in-flight jobs requeued by the reaper.
	QueueReapedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "repo_guardian_queue_reaped_total",
		Help: "Total in-flight jobs requeued by the reaper.",
	})

	// QueueAttemptsExhaustedTotal counts jobs dropped at the
	// MAX_JOB_ATTEMPTS cap with a terminal StatusError written to
	// repo_state (IMPL-0022). The next stale sweep re-enqueues the
	// repo naturally if it is still due, so a sustained rate here
	// means a persistently failing installation or repo.
	QueueAttemptsExhaustedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_queue_attempts_exhausted_total",
		Help: "Total jobs dropped after exceeding the attempt cap.",
	}, []string{labelInstallationID})

	// queueRetrySecondsBuckets is the shared layout for the deferral
	// and wait histograms (IMPL-0022 OQ4): 1s → 4h, matched to
	// rate-limit reset windows (≤1h) and the 30m backoff cap. Expect
	// top-bucket skew in queue_wait_seconds during fleet onboarding
	// or a policy-version bump — see docs/operations/scaling.md.
	queueRetrySecondsBuckets = []float64{1, 5, 15, 60, 300, 900, 3600, 14400}

	// QueueDelayedTotal counts deferrals into the delayed set by
	// reason and installation (IMPL-0022) — "how often is work
	// deferred, and why". The single source for deferral counting;
	// it replaced the github_rate_limit_waits pair (INV-0013 G).
	QueueDelayedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_queue_delayed_total",
		Help: "Total jobs deferred into the delayed set, by reason.",
	}, []string{labelReason, labelInstallationID})

	// QueueDelaySeconds records how far in the future deferred jobs
	// are parked, by reason — "how long are the deferrals".
	QueueDelaySeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "repo_guardian_queue_delay_seconds",
		Help:    "Deferral horizon in seconds (due time minus now) at defer time.",
		Buckets: queueRetrySecondsBuckets,
	}, []string{labelReason})

	// QueueWaitSeconds records enqueue→claim latency per installation
	// — the DESIGN-0015 go/no-go datum for per-installation queue
	// partitioning. Observed at claim time as now − EnqueuedAt; a
	// deferred job's parked time counts, deliberately, because the
	// tenant experienced it as queue wait.
	QueueWaitSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "repo_guardian_queue_wait_seconds",
		Help:    "Enqueue-to-claim latency in seconds, per installation.",
		Buckets: queueRetrySecondsBuckets,
	}, []string{labelInstallationID})

	// SchedulerSweepBatchSize records the count of repos enqueued per
	// sweep handler invocation. Useful for spotting partial-enumeration
	// bugs (consistently 0 batches → upstream API error or
	// listInstallations failure).
	SchedulerSweepBatchSize = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "repo_guardian_scheduler_sweep_batch_size",
		Help:    "Repos enqueued per sweep invocation.",
		Buckets: []float64{0, 1, 5, 10, 25, 50, 100, 250, 500, 1000},
	})

	// RateLimitRemaining tracks the GitHub API rate-limit remaining
	// budget per installation, scraped from X-RateLimit-Remaining on
	// every response. Replaces the singleton GitHubRateRemaining gauge
	// for installation-scoped clients.
	RateLimitRemaining = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "repo_guardian_rate_limit_remaining",
		Help: "GitHub API rate limit remaining, per installation.",
	}, []string{labelInstallationID})

	// InstallationInfo is a constant-1 info gauge pairing an
	// installation ID with the org that installed the App. It carries no
	// measurement of its own; it exists so dashboards can `group_left`
	// the installation_id-keyed series (RateLimitRemaining,
	// DiscoveryAPICallsTotal, QueueAttemptsExhaustedTotal) into per-org
	// rows (DESIGN-0022 finding E2).
	//
	// Every replica that touches an installation emits it, so the series
	// differ by scrape target. Join against
	// `max by (installation_id, org) (repo_guardian_installation_info)`,
	// not the raw vector, or the group_left is many-to-many.
	InstallationInfo = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "repo_guardian_installation_info",
		Help: "Constant 1, labeled with the org that owns each installation. Join label only.",
	}, []string{labelInstallationID, labelOrg})

	// SchedulerIsLeader is a gauge labeled by pod that exports 1 when
	// the named pod holds the scheduler leader lock and 0 otherwise.
	// Registered in IMPL-0011 Phase 4; wiring deferred to Phase 5.
	SchedulerIsLeader = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "repo_guardian_scheduler_is_leader",
		Help: "1 when this pod holds the scheduler leader lock for the named handler, 0 otherwise.",
	}, []string{"name", "pod"})

	// PROpenWithEmptyActionableTotal counts reconcile passes where
	// an open repo-guardian PR exists but the actionable rule set is
	// empty — the drift surface identified in INV-0005. Incremented
	// inside checkRepoWithPolicy. A non-zero rate after the
	// IMPL-0013 Phase 3 fix lands indicates the convergence path is
	// not working as expected.
	PROpenWithEmptyActionableTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_pr_open_with_empty_actionable_total",
		Help: "Reconcile passes where an open repo-guardian PR existed and the actionable rule set was empty.",
	}, []string{labelOrg})

	// OpenPRsByRule tracks the count of currently-open repo-guardian
	// PRs labeled by org, rule, and age bucket. Populated by the
	// sweep handler; reset to zero for {org, rule} combinations whose
	// count drops between sweeps to avoid phantom non-zero series.
	// Age buckets are hard-coded to keep cardinality bounded — see
	// PRAgeBucket helper.
	OpenPRsByRule = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "repo_guardian_open_prs_by_rule",
		Help: "Open repo-guardian PRs by org, rule, and age bucket.",
	}, []string{labelOrg, "rule", "age_bucket"})

	// PRsClosedTotal counts pull requests closed by repo-guardian
	// labeled by org and reason. IMPL-0013 Phase 3 introduces the
	// reason="satisfied" path (auto-close when every file rule has
	// been satisfied on the default branch); future reasons can be
	// added without changing the metric name.
	PRsClosedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_prs_closed_total",
		Help: "Pull requests closed by repo-guardian, by reason.",
	}, []string{labelOrg, labelReason})

	// RepoDiscoveredTotal counts new rows inserted by the Discoverer
	// via Store.UpsertIfMissing. Per-installation. Increments on
	// first sighting of a repo; subsequent runs are idempotent
	// (created=false on the upsert) and do not increment.
	RepoDiscoveredTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_repo_discovered_total",
		Help: "Repositories discovered by the Discoverer (UpsertIfMissing → created=true).",
	}, []string{labelInstallationID})

	// DiscoveryDurationSeconds records the wall-clock duration of a
	// single Discoverer.Discover invocation.
	DiscoveryDurationSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "repo_guardian_discovery_duration_seconds",
		Help:    "Duration of a single Discoverer.Discover invocation in seconds.",
		Buckets: []float64{0.1, 0.5, 1, 5, 10, 30, 60, 120, 300},
	})

	// DiscoveryAPICallsTotal counts GitHub API calls the Discoverer
	// made, labeled by installation_id and endpoint
	// (list_installations / list_installation_repos). Lets operators
	// see exactly which endpoint is burning their budget.
	DiscoveryAPICallsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_discovery_api_calls_total",
		Help: "GitHub API calls made by the Discoverer, by installation and endpoint.",
	}, []string{labelInstallationID, "endpoint"})

	// StoreWritebackTotal counts persistent state write-back attempts
	// from the worker pool, labeled by installation_id and outcome
	// ("ok" or "error"). Introduced by IMPL-0015 Phase 0 — every
	// processed job's final UpdateRepoState call increments this
	// counter. A non-zero rate at outcome="error" indicates the worker
	// completed work that the stale-sweeper will re-enqueue on the
	// next tick because the persisted state never converged.
	StoreWritebackTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_store_writeback_total",
		Help: "Persistent-state write-back attempts from the worker pool, by installation and outcome.",
	}, []string{labelInstallationID, labelOutcome})

	// StoreWritebackDurationSeconds records the latency of worker-pool
	// write-back attempts. Same call site as StoreWritebackTotal;
	// observed on both success and error paths so percentile latency
	// reflects the full distribution (failed writes that take 5s to
	// time out are a real signal, not a censored sample).
	StoreWritebackDurationSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "repo_guardian_store_writeback_duration_seconds",
		Help:    "Latency of worker-pool persistent-state write-backs in seconds.",
		Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5},
	})

	// PROrphanLeftTotal counts orphan files that repo-guardian
	// attempted to delete from a reconcile branch but couldn't
	// (typically a transient GitHub API failure). A non-zero rate
	// indicates the next sweep needs to retry; sustained non-zero
	// values across many sweeps point at a permission or branch-
	// protection misconfiguration.
	PROrphanLeftTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_pr_orphan_left_total",
		Help: "Orphan files that could not be deleted from the reconcile branch.",
	}, []string{labelOrg})
)

// Hard-coded age bucket labels for the OpenPRsByRule gauge.
const (
	PRAgeBucketLT1d  = "<1d"
	PRAgeBucket1To7  = "1-7d"
	PRAgeBucket7To30 = "7-30d"
	PRAgeBucketGT30  = "30d+"
)

// PRAgeBuckets lists all valid age buckets in ascending order. Useful
// for resetting the OpenPRsByRule gauge across every bucket for a
// given {org, rule} pair.
var PRAgeBuckets = [...]string{
	PRAgeBucketLT1d,
	PRAgeBucket1To7,
	PRAgeBucket7To30,
	PRAgeBucketGT30,
}

// ResetOpenPRsByRule wipes every series of the OpenPRsByRule gauge.
// Sweepers call this at the start of each iteration so that
// {org, rule} combinations whose count drops to zero between sweeps
// stop reporting phantom non-zero series. Workers re-populate the
// gauge as they process enqueued jobs; the gauge converges within
// one sweep cycle.
func ResetOpenPRsByRule() {
	OpenPRsByRule.Reset()
}

// PRAgeBucket returns the hard-coded age bucket label for the given
// number of days since the PR was opened.
func PRAgeBucket(ageDays float64) string {
	switch {
	case ageDays < 1:
		return PRAgeBucketLT1d
	case ageDays < 7:
		return PRAgeBucket1To7
	case ageDays < 30:
		return PRAgeBucket7To30
	default:
		return PRAgeBucketGT30
	}
}

// SetInstallationInfo records the org that owns an installation so
// dashboards can join installation_id-keyed series into per-org rows.
// Callers pass the org they already have in hand; a blank org is
// dropped rather than published, because a series labeled org="" joins
// nothing and would only widen cardinality.
func SetInstallationInfo(installationID int64, org string) {
	if org == "" {
		return
	}

	InstallationInfo.
		WithLabelValues(strconv.FormatInt(installationID, 10), org).
		Set(1)
}

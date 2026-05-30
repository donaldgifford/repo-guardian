// Package metrics defines Prometheus metrics for repo-guardian observability.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// All repo-guardian Prometheus metrics.
var (
	// ReposCheckedTotal counts the total number of repositories processed.
	ReposCheckedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_repos_checked_total",
		Help: "Total repositories processed.",
	}, []string{"trigger", "org"})

	// PRsCreatedTotal counts the total number of PRs created, by org.
	PRsCreatedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_prs_created_total",
		Help: "Total pull requests created.",
	}, []string{"org"})

	// PRsUpdatedTotal counts the total number of existing PRs updated, by org.
	PRsUpdatedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_prs_updated_total",
		Help: "Total existing pull requests updated.",
	}, []string{"org"})

	// FilesMissingTotal counts missing files detected, labeled by rule name and org.
	FilesMissingTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_files_missing_total",
		Help: "Missing files detected.",
	}, []string{"rule_name", "org"})

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
	}, []string{"operation", "org"})

	// GitHubRateRemaining tracks the GitHub API rate limit remaining.
	GitHubRateRemaining = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "repo_guardian_github_rate_remaining",
		Help: "GitHub API rate limit remaining.",
	})

	// GitHubRateLimitWaitsTotal counts rate limit waits by reason.
	GitHubRateLimitWaitsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_github_rate_limit_waits_total",
		Help: "Total rate limit waits by reason.",
	}, []string{"reason"})

	// GitHubRateLimitWaitSeconds records the duration of rate limit waits.
	GitHubRateLimitWaitSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "repo_guardian_github_rate_limit_wait_seconds",
		Help:    "Duration of rate limit waits in seconds.",
		Buckets: []float64{0.1, 0.5, 1, 5, 10, 30, 60, 120, 300},
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

	// WebhookRejectedTotal counts webhook requests rejected by the IP allowlist.
	WebhookRejectedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_webhook_rejected_total",
		Help: "Webhook requests rejected by IP allowlist.",
	}, []string{"reason"})

	// IgnoredTotal counts repos or rules skipped by ignore lists, by scope and org.
	IgnoredTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_ignored_total",
		Help: "Repos or rules skipped by ignore lists.",
	}, []string{"scope", "org"})

	// SettingsCheckedTotal counts setting rules evaluated per rule name and org.
	SettingsCheckedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_settings_checked_total",
		Help: "Setting rules evaluated.",
	}, []string{"rule_name", "org"})

	// SettingsMismatchedTotal counts setting rules that found a mismatch.
	SettingsMismatchedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_settings_mismatched_total",
		Help: "Setting rules that found a mismatch.",
	}, []string{"rule_name", "org"})

	// SettingsRemediatedTotal counts setting rules that were remediated.
	SettingsRemediatedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_settings_remediated_total",
		Help: "Setting rules remediated via API.",
	}, []string{"rule_name", "org"})

	// BranchProtectionCheckedTotal counts branch protection rules evaluated.
	BranchProtectionCheckedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_branch_protection_checked_total",
		Help: "Branch protection rules evaluated.",
	}, []string{"rule_name", "org"})

	// BranchProtectionRemediatedTotal counts branch protection rules remediated.
	BranchProtectionRemediatedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_branch_protection_remediated_total",
		Help: "Branch protection rules remediated via rulesets API.",
	}, []string{"rule_name", "org"})

	// OutOfScopeTotal counts rule evaluations skipped by strict-mode scope.
	// level="policy" means the top-level policy scope rejected the repo
	// (incremented once per enabled rule across all rule types). level="rule"
	// means the per-rule scope rejected the repo (incremented once per rule).
	OutOfScopeTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_out_of_scope_total",
		Help: "Rule evaluations skipped by strict-mode scope, by level (policy or rule) and org.",
	}, []string{"level", "org"})

	// StoreQuerySeconds records the time taken by individual Store
	// queries. Labeled by operation (get, update, stale, migrate) and
	// outcome (ok, error). Registered in IMPL-0011 Phase 2; wiring into
	// the postgres Store is deferred to Phase 5 to keep observability
	// changes ring-fenced.
	StoreQuerySeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "repo_guardian_store_query_seconds",
		Help:    "Duration of Store queries in seconds.",
		Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5},
	}, []string{"operation", "outcome"})

	// QueueDepth tracks the current pending-job count of the work
	// queue, labeled by queue (jobs or in-flight) for the Valkey
	// backend.
	QueueDepth = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "repo_guardian_queue_depth",
		Help: "Pending jobs in the work queue.",
	}, []string{"queue"})

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

	// QueueAckedTotal counts handler returns by outcome (success or
	// error). A `success` ack means ZREM in-flight succeeded; an
	// `error` ack means the handler returned an error and the entry
	// was left in-flight for the reaper.
	QueueAckedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_queue_acked_total",
		Help: "Total jobs acknowledged by a worker, by outcome.",
	}, []string{"outcome"})

	// QueueReapedTotal counts in-flight jobs requeued by the reaper.
	QueueReapedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "repo_guardian_queue_reaped_total",
		Help: "Total in-flight jobs requeued by the reaper.",
	})

	// SchedulerSweepBatchSize records the count of repos enqueued per
	// sweep handler invocation. Useful for spotting partial-enumeration
	// bugs (consistently 0 batches → upstream API error or
	// listInstallations failure).
	SchedulerSweepBatchSize = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "repo_guardian_scheduler_sweep_batch_size",
		Help:    "Repos enqueued per sweep invocation.",
		Buckets: []float64{0, 1, 5, 10, 25, 50, 100, 250, 500, 1000},
	})

	// RateLimitReserveBlockedTotal counts repos skipped during sweep
	// because the GitHub API rate-limit reserve gate triggered for
	// the installation.
	RateLimitReserveBlockedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_rate_limit_reserve_blocked_total",
		Help: "Repos skipped by the sweep rate-limit reserve gate, by installation.",
	}, []string{"installation_id"})

	// RateLimitRemaining tracks the GitHub API rate-limit remaining
	// budget per installation, scraped from X-RateLimit-Remaining on
	// every response. Replaces the singleton GitHubRateRemaining gauge
	// for installation-scoped clients.
	RateLimitRemaining = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "repo_guardian_rate_limit_remaining",
		Help: "GitHub API rate limit remaining, per installation.",
	}, []string{"installation_id"})

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
	}, []string{"org"})

	// OpenPRsByRule tracks the count of currently-open repo-guardian
	// PRs labeled by org, rule, and age bucket. Populated by the
	// sweep handler; reset to zero for {org, rule} combinations whose
	// count drops between sweeps to avoid phantom non-zero series.
	// Age buckets are hard-coded to keep cardinality bounded — see
	// PRAgeBucket helper.
	OpenPRsByRule = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "repo_guardian_open_prs_by_rule",
		Help: "Open repo-guardian PRs by org, rule, and age bucket.",
	}, []string{"org", "rule", "age_bucket"})

	// PRsClosedTotal counts pull requests closed by repo-guardian
	// labeled by org and reason. IMPL-0013 Phase 3 introduces the
	// reason="satisfied" path (auto-close when every file rule has
	// been satisfied on the default branch); future reasons can be
	// added without changing the metric name.
	PRsClosedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_prs_closed_total",
		Help: "Pull requests closed by repo-guardian, by reason.",
	}, []string{"org", "reason"})

	// PROrphanLeftTotal counts orphan files that repo-guardian
	// attempted to delete from a reconcile branch but couldn't
	// (typically a transient GitHub API failure). A non-zero rate
	// indicates the next sweep needs to retry; sustained non-zero
	// values across many sweeps point at a permission or branch-
	// protection misconfiguration.
	PROrphanLeftTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_pr_orphan_left_total",
		Help: "Orphan files that could not be deleted from the reconcile branch.",
	}, []string{"org"})
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

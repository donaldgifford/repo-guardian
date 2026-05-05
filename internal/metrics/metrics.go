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

	// QueueDepth tracks the current pending-job count, by backend
	// label (memory or valkey). Registered in IMPL-0011 Phase 3; wiring
	// into the Valkey queue is deferred to Phase 5.
	QueueDepth = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "repo_guardian_queue_depth",
		Help: "Pending jobs in the work queue.",
	}, []string{"backend"})

	// QueueEnqueuedTotal counts jobs enqueued.
	QueueEnqueuedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_queue_enqueued_total",
		Help: "Total jobs enqueued.",
	}, []string{"backend"})

	// QueueClaimedTotal counts jobs claimed (BRPOP + ZADD in-flight).
	QueueClaimedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_queue_claimed_total",
		Help: "Total jobs claimed by a worker.",
	}, []string{"backend"})

	// QueueAckedTotal counts jobs successfully acknowledged.
	QueueAckedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_queue_acked_total",
		Help: "Total jobs acknowledged by a worker.",
	}, []string{"backend"})

	// QueueReapedTotal counts in-flight jobs requeued by the reaper.
	QueueReapedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "repo_guardian_queue_reaped_total",
		Help: "Total in-flight jobs requeued by the reaper.",
	})
)

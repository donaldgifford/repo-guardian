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
	}, []string{"trigger"})

	// PRsCreatedTotal counts the total number of PRs created.
	PRsCreatedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "repo_guardian_prs_created_total",
		Help: "Total pull requests created.",
	})

	// PRsUpdatedTotal counts the total number of existing PRs updated.
	PRsUpdatedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "repo_guardian_prs_updated_total",
		Help: "Total existing pull requests updated.",
	})

	// FilesMissingTotal counts missing files detected, labeled by rule name.
	FilesMissingTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_files_missing_total",
		Help: "Missing files detected.",
	}, []string{"rule_name"})

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

	// ErrorsTotal counts errors, labeled by operation.
	ErrorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_errors_total",
		Help: "Errors encountered.",
	}, []string{"operation"})

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

	// IgnoredTotal counts repos or rules skipped by ignore lists.
	IgnoredTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_ignored_total",
		Help: "Repos or rules skipped by ignore lists.",
	}, []string{"scope"})

	// SettingsCheckedTotal counts setting rules evaluated per rule name.
	SettingsCheckedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_settings_checked_total",
		Help: "Setting rules evaluated.",
	}, []string{"rule_name"})

	// SettingsMismatchedTotal counts setting rules that found a mismatch.
	SettingsMismatchedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_settings_mismatched_total",
		Help: "Setting rules that found a mismatch.",
	}, []string{"rule_name"})

	// SettingsRemediatedTotal counts setting rules that were remediated.
	SettingsRemediatedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_settings_remediated_total",
		Help: "Setting rules remediated via API.",
	}, []string{"rule_name"})

	// BranchProtectionCheckedTotal counts branch protection rules evaluated.
	BranchProtectionCheckedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_branch_protection_checked_total",
		Help: "Branch protection rules evaluated.",
	}, []string{"rule_name"})

	// BranchProtectionRemediatedTotal counts branch protection rules remediated.
	BranchProtectionRemediatedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_guardian_branch_protection_remediated_total",
		Help: "Branch protection rules remediated via rulesets API.",
	}, []string{"rule_name"})
)

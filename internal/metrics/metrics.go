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
)

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
)

// Package-level drift observability helpers added in IMPL-0013
// Phase 1. The counter wiring lives in engine_policy.go; this file
// hosts the gauge population logic and a thin reset facade used by
// both sweep paths.

package checker

import (
	"time"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/policy"
)

// recordOpenPRsByRule increments the OpenPRsByRule gauge by one for
// each rule in the actionable set, labeled by the PR's age bucket.
// Callers must only invoke this when the PR is repo-guardian's own
// (i.e., findOurPR(openPRs) returned non-nil).
//
// Phase 1 attributes the PR to the rules currently failing. Phase 3
// will refine this to include orphan rules (rules previously claimed
// on the reconcile branch whose check now passes on main).
func recordOpenPRsByRule(pr *ghclient.PullRequest, actionable []policy.FileRuleConfig, owner string) {
	if pr == nil {
		return
	}

	bucket := pullRequestAgeBucket(pr)

	for i := range actionable {
		metrics.OpenPRsByRule.WithLabelValues(owner, actionable[i].Name, bucket).Inc()
	}
}

// pullRequestAgeBucket returns the metrics age bucket for the given
// PR. Falls back to the <1d bucket if CreatedAt is zero (e.g., a test
// fixture that didn't populate it).
func pullRequestAgeBucket(pr *ghclient.PullRequest) string {
	if pr.CreatedAt.IsZero() {
		return metrics.PRAgeBucketLT1d
	}

	ageDays := time.Since(pr.CreatedAt).Hours() / 24

	return metrics.PRAgeBucket(ageDays)
}

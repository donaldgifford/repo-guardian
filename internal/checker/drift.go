// Package-level drift observability helpers added in IMPL-0013
// Phase 1. The counter wiring lives in engine_policy.go; this file
// hosts the gauge population logic and a thin reset facade used by
// both sweep paths.

package checker

import (
	"context"
	"fmt"
	"log/slog"
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

// orphanFile pairs a rule's target path with its blob sha on the
// reconcile branch. Returned by discoverOrphans for cleanupOrphans
// to consume.
type orphanFile struct {
	RuleName string
	Path     string
	SHA      string
}

// discoverOrphans returns files on the reconcile branch authored for
// rules that are no longer actionable. Per IMPL-0013 Resolved
// Decisions Q9, GetContentsOnBranch errors are treated as "still
// actionable" — the file is omitted from the orphan list so the
// downstream cleanup never deletes a file under a transient API
// glitch. Errors are logged at Warn so operators can spot
// systematic failures.
func discoverOrphans(
	ctx context.Context,
	log *slog.Logger,
	client ghclient.Client,
	allRules []policy.FileRuleConfig,
	actionable []policy.FileRuleConfig,
	owner, repo string,
) []orphanFile {
	actionableSet := make(map[string]struct{}, len(actionable))
	for i := range actionable {
		actionableSet[actionable[i].Name] = struct{}{}
	}

	var orphans []orphanFile

	for i := range allRules {
		r := &allRules[i]

		if !r.IsEnabled() {
			continue
		}

		if _, stillActionable := actionableSet[r.Name]; stillActionable {
			continue
		}

		if r.Target == "" {
			continue
		}

		sha, exists, err := client.GetContentsOnBranch(ctx, owner, repo, r.Target, BranchName)
		if err != nil {
			log.Warn("orphan discovery: GetContentsOnBranch failed, treating as still-actionable",
				"rule", r.Name, "path", r.Target, "err", err)

			continue
		}

		if !exists {
			continue
		}

		orphans = append(orphans, orphanFile{
			RuleName: r.Name,
			Path:     r.Target,
			SHA:      sha,
		})
	}

	return orphans
}

// cleanupOrphans deletes the given orphan files from the reconcile
// branch. Partial failures increment PROrphanLeftTotal and are
// logged at Warn but do not abort the caller — the next sweep will
// retry. Returns the count of successful deletes.
func cleanupOrphans(
	ctx context.Context,
	log *slog.Logger,
	client ghclient.Client,
	orphans []orphanFile,
	owner, repo string,
) int {
	deleted := 0

	for i := range orphans {
		o := orphans[i]
		msg := fmt.Sprintf("chore: remove %s (rule %q satisfied on default branch)", o.Path, o.RuleName)

		if err := client.DeleteFile(ctx, owner, repo, BranchName, o.Path, o.SHA, msg); err != nil {
			metrics.PROrphanLeftTotal.WithLabelValues(owner).Inc()
			log.Warn("orphan cleanup: DeleteFile failed; will retry next sweep",
				"rule", o.RuleName, "path", o.Path, "err", err)

			continue
		}

		log.Info("orphan cleanup: deleted file",
			"rule", o.RuleName, "path", o.Path)

		deleted++
	}

	return deleted
}

// reconcileLogMarker is the row-1 marker that identifies the sticky
// reconcile-log PR comment. Versioned (v1) so future schema changes
// don't break the upsert match.
const reconcileLogMarker = "<!-- repo-guardian:reconcile-log:v1 -->"

// autoClosePR posts a final sticky comment, closes the PR, and
// deletes the reconcile branch. Each step is independently
// best-effort — if posting the comment fails we still close, if
// branch delete fails we still consider the close successful (the
// branch will be cleaned up on the next sweep that finds actionable
// rules via the existing stale-branch path).
func autoClosePR(
	ctx context.Context,
	log *slog.Logger,
	client ghclient.Client,
	owner, repo string,
	pr *ghclient.PullRequest,
) error {
	closeBody := "All file rules referenced by this PR are now satisfied on the default branch. " +
		"repo-guardian is auto-closing this PR per the `auto_close_pr` policy. " +
		"Set `AUTO_CLOSE_PR=false` or `guardian { auto_close_pr = false }` to opt out."

	if err := client.UpsertPRComment(ctx, owner, repo, pr.Number, reconcileLogMarker, closeBody); err != nil {
		log.Warn("auto-close: posting close comment failed; closing anyway",
			"pr_number", pr.Number, "err", err)
	}

	if err := client.ClosePullRequest(ctx, owner, repo, pr.Number); err != nil {
		return fmt.Errorf("close PR #%d: %w", pr.Number, err)
	}

	metrics.PRsClosedTotal.WithLabelValues(owner, "satisfied").Inc()
	log.Info("auto-closed PR — all file rules satisfied on default branch",
		"pr_number", pr.Number)

	if err := client.DeleteBranch(ctx, owner, repo, BranchName); err != nil {
		log.Warn("auto-close: branch delete failed; stale branch will be cleaned up on next sweep with actionable rules",
			"branch", BranchName, "err", err)
	}

	return nil
}

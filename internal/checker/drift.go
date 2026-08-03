// Package-level drift observability helpers added in IMPL-0013
// Phase 1. The counter wiring lives in engine_policy.go; this file
// hosts the gauge population logic and a thin reset facade used by
// both sweep paths.

package checker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
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

// discoverOrphans returns files on the reconcile branch that
// repo-guardian itself added for rules that are no longer actionable.
//
// A file qualifies only when it is absent from the default branch and
// present on the reconcile branch. Both halves are load-bearing. The
// branch is cut from the default branch, so presence on the branch alone
// is not evidence of authorship — that mistake shipped in IMPL-0013 and
// caused repo-guardian to propose deleting files repositories legitimately
// owned (INV-0014). Absence from the default branch is what makes
// repo-guardian the only party that could have placed the file there.
//
// Every API error is fail-safe in the same direction: treat the rule as
// still actionable and omit it from the orphan list, so a transient glitch
// can never cause a deletion. Errors are logged at Warn so operators can
// spot systematic failures (IMPL-0013 Resolved Decisions Q9).
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

		// INV-0014: a path that exists on the default branch is NEVER an
		// orphan. The reconcile branch is cut from the default branch (and
		// re-merged with it by updateReconcileBranch), so "the file is on
		// the branch" does not mean "we put it there" — it is equally the
		// signature of a file the repository has always owned. Deleting one
		// of those makes the PR propose removing a legitimate file.
		//
		// Probing default FIRST also makes the common case — a rule
		// satisfied because the file is on the default branch — cost one
		// API call instead of two.
		onDefault, err := client.GetContents(ctx, owner, repo, r.Target)
		if err != nil {
			log.Warn("orphan discovery: default-branch probe failed, treating as still-actionable",
				"rule", r.Name, "path", r.Target, "err", err)

			continue
		}

		if onDefault {
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
// retry. Returns the names of rules whose orphan files were
// successfully removed (for the reconcile-log sticky comment).
func cleanupOrphans(
	ctx context.Context,
	log *slog.Logger,
	client ghclient.Client,
	orphans []orphanFile,
	owner, repo string,
) []string {
	removed := make([]string, 0, len(orphans))

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

		removed = append(removed, o.RuleName)
	}

	return removed
}

// restoreInverseOrphans restores forbidden files that an absent rule
// previously deleted from the reconcile branch but which should no longer
// be removed because the rule is no longer actionable (its when-gate
// closed, or the file is gone from the default branch). It is the mirror
// image of discoverOrphans/cleanupOrphans: instead of deleting a
// no-longer-wanted added file, it re-adds a no-longer-forbidden file.
//
// Fail-safe (mirrors cleanupOrphans / IMPL-0013 Q9): any API error leaves
// the path untouched, logs at Warn, increments PROrphanLeftTotal, and the
// next sweep retries. Returns the names of rules whose files were restored.
func restoreInverseOrphans(
	ctx context.Context,
	log *slog.Logger,
	client ghclient.Client,
	allRules, actionable []policy.FileRuleConfig,
	owner, repo string,
) []string {
	actionableSet := make(map[string]struct{}, len(actionable))
	for i := range actionable {
		actionableSet[actionable[i].Name] = struct{}{}
	}

	var restored []string

	for i := range allRules {
		r := &allRules[i]

		if !r.IsEnabled() || r.CheckMode() != policy.CheckAbsent {
			continue
		}

		if _, stillActionable := actionableSet[r.Name]; stillActionable {
			continue
		}

		if restoreRulePaths(ctx, log, client, r, owner, repo) {
			restored = append(restored, r.Name)
		}
	}

	return restored
}

// restoreRulePaths restores every path of a no-longer-actionable absent
// rule that is present on the default branch but missing from the
// reconcile branch. Returns true if at least one path was restored. Every
// API error is fail-safe: the path is left untouched and retried next sweep.
func restoreRulePaths(
	ctx context.Context,
	log *slog.Logger,
	client ghclient.Client,
	r *policy.FileRuleConfig,
	owner, repo string,
) bool {
	var restoredAny bool

	for _, path := range r.Paths {
		onDefault, err := client.GetContents(ctx, owner, repo, path)
		if err != nil {
			logRestoreSkip(log, owner, r.Name, path, "default-branch probe failed", err)

			continue
		}

		if !onDefault {
			continue // nothing to restore — the file is gone from main too
		}

		_, onBranch, err := client.GetContentsOnBranch(ctx, owner, repo, path, BranchName)
		if err != nil {
			logRestoreSkip(log, owner, r.Name, path, "branch probe failed", err)

			continue
		}

		if onBranch {
			continue // branch still has it — nothing was deleted to restore
		}

		content, err := client.GetFileContent(ctx, owner, repo, path)
		if err != nil {
			logRestoreSkip(log, owner, r.Name, path, "fetching default content failed", err)

			continue
		}

		msg := fmt.Sprintf("chore: restore %s (rule %q no longer applies)", path, r.Name)
		if err := client.CreateOrUpdateFile(ctx, owner, repo, BranchName, path, content, msg); err != nil {
			logRestoreSkip(log, owner, r.Name, path, "restore write failed", err)

			continue
		}

		log.Info("inverse-orphan: restored file", "rule", r.Name, "path", path)

		restoredAny = true
	}

	return restoredAny
}

// logRestoreSkip logs a fail-safe inverse-orphan skip and counts it.
func logRestoreSkip(log *slog.Logger, owner, rule, path, reason string, err error) {
	metrics.PROrphanLeftTotal.WithLabelValues(owner).Inc()
	log.Warn("inverse-orphan: "+reason+"; leaving as-is, will retry next sweep",
		"rule", rule, "path", path, "err", err)
}

// reconcileLogMarker is the row-1 marker that identifies the sticky
// reconcile-log PR comment. Versioned (v1) so future schema changes
// don't break the upsert match.
const reconcileLogMarker = "<!-- repo-guardian:reconcile-log:v1 -->"

// reconcileLogEvent captures one rule's state in the current sweep.
type reconcileLogEvent struct {
	Rule   string
	Status string // "still actionable" | "satisfied on main" | "orphan removed from branch"
}

// upsertReconcileLog posts (or edits) the sticky reconcile-log
// comment on the given PR. The body lists every rule's current
// status — handy for operators reading the PR's comment history to
// reconstruct what repo-guardian decided.
//
// Skips the upsert when an existing reconcile-log comment already
// reflects the same per-rule state (matched via a content-hash
// HTML comment embedded in the body). The body's timestamp is
// excluded from the hash so identical-state sweeps converge to a
// single API call.
func upsertReconcileLog(
	ctx context.Context,
	log *slog.Logger,
	client ghclient.Client,
	owner, repo string,
	pr *ghclient.PullRequest,
	events []reconcileLogEvent,
) {
	if pr == nil {
		return
	}

	body := renderReconcileLog(events)
	hashTag := reconcileLogHashTag(events)

	existing, err := client.ListPRComments(ctx, owner, repo, pr.Number)
	if err != nil {
		log.Warn("reconcile-log: ListPRComments failed; will retry next sweep",
			"pr_number", pr.Number, "err", err)

		return
	}

	for i := range existing {
		c := existing[i]
		if strings.HasPrefix(c.Body, reconcileLogMarker) && strings.Contains(c.Body, hashTag) {
			// State unchanged since the prior reconcile — skip upsert.
			return
		}
	}

	if err := client.UpsertPRComment(ctx, owner, repo, pr.Number, reconcileLogMarker, body); err != nil {
		log.Warn("reconcile-log: UpsertPRComment failed; will retry next sweep",
			"pr_number", pr.Number, "err", err)
	}
}

// reconcileLogHashTag returns the HTML-comment hash tag embedded in
// every rendered reconcile-log body. The hash covers the rule-status
// pairs only; the human-readable timestamp is deliberately excluded
// so identical-state sweeps produce identical hashes.
func reconcileLogHashTag(events []reconcileLogEvent) string {
	h := sha256.New()
	for i := range events {
		_, _ = h.Write([]byte(events[i].Rule + "=" + events[i].Status + "\n"))
	}

	return "<!-- repo-guardian:reconcile-log:hash:" + hex.EncodeToString(h.Sum(nil)[:8]) + " -->"
}

func renderReconcileLog(events []reconcileLogEvent) string {
	var sb strings.Builder

	sb.WriteString(reconcileLogHashTag(events))
	sb.WriteString("\n## repo-guardian reconcile log\n\n")
	sb.WriteString("Last reconciled at ")
	sb.WriteString(time.Now().UTC().Format(time.RFC3339))
	sb.WriteString(".\n\n")

	if len(events) == 0 {
		sb.WriteString("_No file rules in scope for this repository._\n")

		return sb.String()
	}

	sb.WriteString("| Rule | Status |\n")
	sb.WriteString("|------|--------|\n")

	for i := range events {
		e := events[i]
		fmt.Fprintf(&sb, "| `%s` | %s |\n", e.Rule, e.Status)
	}

	return sb.String()
}

// mapContains returns true when the key exists in the set.
func mapContains(set map[string]struct{}, key string) bool {
	_, ok := set[key]

	return ok
}

// buildReconcileLogEvents joins the current actionable set, the orphans
// removed this sweep, the inverse-orphans restored this sweep, and the
// memoized gate outcomes into a flat per-rule status list. Absent rules
// report removal-oriented statuses; a gate-closed rule reports why it was
// skipped. gate may be nil (e.g. the auto-close path) — gate statuses are
// simply omitted then.
func buildReconcileLogEvents(
	allRules, actionable []policy.FileRuleConfig,
	removedOrphans, restoredRules []string,
	gate *gateEvaluator,
) []reconcileLogEvent {
	actionableNames := make(map[string]struct{}, len(actionable))
	for i := range actionable {
		actionableNames[actionable[i].Name] = struct{}{}
	}

	orphanNames := stringSet(removedOrphans)
	restoredNames := stringSet(restoredRules)

	events := make([]reconcileLogEvent, 0, len(allRules))

	for i := range allRules {
		r := &allRules[i]
		if !r.IsEnabled() {
			continue
		}

		status := reconcileStatus(r, actionableNames, orphanNames, restoredNames, gate)
		events = append(events, reconcileLogEvent{Rule: r.Name, Status: status})
	}

	return events
}

// reconcileStatus returns the reconcile-log status string for one rule.
func reconcileStatus(
	r *policy.FileRuleConfig,
	actionable, orphans, restored map[string]struct{},
	gate *gateEvaluator,
) string {
	absent := r.CheckMode() == policy.CheckAbsent

	switch {
	case mapContains(orphans, r.Name):
		return "orphan removed from branch"
	case mapContains(restored, r.Name):
		return "restored to branch (rule no longer applies)"
	case mapContains(actionable, r.Name):
		if absent {
			return "present on main, pending removal"
		}

		return "still actionable"
	}

	if gate != nil {
		if closed, referee, reason := gate.gateStatus(r); closed {
			if reason == gateReasonError {
				return fmt.Sprintf("skipped (when-gate error: %s)", referee)
			}

			return fmt.Sprintf("skipped (when-gate closed: %s not satisfied)", referee)
		}
	}

	if absent {
		return "absent from main"
	}

	return "satisfied on main"
}

// stringSet collects a slice of names into a set for membership tests.
func stringSet(names []string) map[string]struct{} {
	set := make(map[string]struct{}, len(names))
	for _, n := range names {
		set[n] = struct{}{}
	}

	return set
}

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
	allRules []policy.FileRuleConfig,
) error {
	events := buildReconcileLogEvents(allRules, nil, nil, nil, nil)

	closeBody := renderReconcileLog(events) +
		"\n_All file rules satisfied on the default branch. " +
		"Auto-closing per `auto_close_pr` (set to `false` to opt out)._\n"

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

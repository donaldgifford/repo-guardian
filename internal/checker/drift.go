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

// buildReconcileLogEvents joins the current actionable set and the
// list of orphans removed this sweep into a flat status list.
// Anything in allRules not in `actionable` is implicitly satisfied
// on main; orphans are called out separately so operators see the
// cleanup action.
func buildReconcileLogEvents(allRules, actionable []policy.FileRuleConfig, removedOrphans []string) []reconcileLogEvent {
	actionableNames := make(map[string]struct{}, len(actionable))
	for i := range actionable {
		actionableNames[actionable[i].Name] = struct{}{}
	}

	orphanNames := make(map[string]struct{}, len(removedOrphans))
	for _, n := range removedOrphans {
		orphanNames[n] = struct{}{}
	}

	events := make([]reconcileLogEvent, 0, len(allRules))

	for i := range allRules {
		r := &allRules[i]
		if !r.IsEnabled() {
			continue
		}

		switch {
		case mapContains(orphanNames, r.Name):
			events = append(events, reconcileLogEvent{Rule: r.Name, Status: "orphan removed from branch"})
		case mapContains(actionableNames, r.Name):
			events = append(events, reconcileLogEvent{Rule: r.Name, Status: "still actionable"})
		default:
			events = append(events, reconcileLogEvent{Rule: r.Name, Status: "satisfied on main"})
		}
	}

	return events
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
	events := buildReconcileLogEvents(allRules, nil, nil)

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

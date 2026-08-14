package checker

import (
	"context"
	"strings"
	"testing"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/policy"
)

// Phase 4 — sticky reconcile-log comment tests.

func TestStickyComment_CreatedOnFirstReconcileWithExistingPR(t *testing.T) {
	cfg := policy.BuiltinDefaults()
	engine := testPolicyEngine(cfg)

	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.branchSHAs["org/repo/main"] = "main-sha"
	client.branchSHAs["org/repo/"+BranchName] = "branch-sha"
	client.openPRs = []*ghclient.PullRequest{
		{Number: 1, Title: PRTitle, Head: BranchName, State: "open"},
	}

	if _, err := engine.CheckRepo(context.Background(), client, "org", "repo"); err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if len(client.upsertedComments) == 0 {
		t.Fatal("expected at least one sticky comment upsert")
	}

	c := client.upsertedComments[0]
	if c.PRNumber != 1 {
		t.Errorf("comment PR = %d, want 1", c.PRNumber)
	}
	if c.Marker != reconcileLogMarker {
		t.Errorf("marker = %q, want %q", c.Marker, reconcileLogMarker)
	}
	if !strings.Contains(c.Body, "repo-guardian reconcile log") {
		t.Errorf("body missing header: %q", c.Body)
	}
}

func TestStickyComment_NotSentWhenNoExistingPR(t *testing.T) {
	cfg := policy.BuiltinDefaults()
	engine := testPolicyEngine(cfg)

	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.branchSHAs["org/repo/main"] = "main-sha"

	// No existing PR; the engine will create one but should not
	// post a reconcile-log comment until subsequent sweeps find
	// the PR in openPRs.
	if _, err := engine.CheckRepo(context.Background(), client, "org", "repo"); err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if len(client.upsertedComments) != 0 {
		t.Errorf("expected no comments on first-creation sweep, got %d", len(client.upsertedComments))
	}
}

func TestStickyComment_ConvergentStateMentionsSatisfiedRules(t *testing.T) {
	cfg := policy.BuiltinDefaults()
	engine := testPolicyEngine(cfg)

	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.branchSHAs["org/repo/main"] = "main-sha"
	client.contents["org/repo/CODEOWNERS"] = true
	client.contents["org/repo/.github/dependabot.yml"] = true
	client.openPRs = []*ghclient.PullRequest{
		{Number: 7, Title: PRTitle, Head: BranchName, State: "open"},
	}

	if _, err := engine.CheckRepo(context.Background(), client, "org", "repo"); err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if len(client.upsertedComments) == 0 {
		t.Fatal("expected close comment from auto-close path")
	}

	c := client.upsertedComments[0]
	if !strings.Contains(c.Body, "satisfied on main") {
		t.Errorf("close-comment body missing convergent status: %q", c.Body)
	}
	if !strings.Contains(c.Body, "Auto-closing") {
		t.Errorf("close-comment body missing auto-close footer: %q", c.Body)
	}
}

// TestStickyComment_NoUpsertOnIdenticalState verifies the
// skip-on-match path in upsertReconcileLog: two reconciles against
// identical per-rule state must result in exactly one
// UpsertPRComment API call. Closes the Phase-4 testing-plan gap
// flagged after the go-style review.
func TestStickyComment_NoUpsertOnIdenticalState(t *testing.T) {
	cfg := policy.BuiltinDefaults()
	engine := testPolicyEngine(cfg)

	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.branchSHAs["org/repo/main"] = "main-sha"
	client.branchSHAs["org/repo/"+BranchName] = "branch-sha"
	client.openPRs = []*ghclient.PullRequest{
		{Number: 1, Title: PRTitle, Head: BranchName, State: "open"},
	}

	for sweep := 1; sweep <= 2; sweep++ {
		if _, err := engine.CheckRepo(context.Background(), client, "org", "repo"); err != nil {
			t.Fatalf("sweep %d: CheckRepo: %v", sweep, err)
		}
	}

	// Auto-close ran on each sweep (PR was open with empty actionable
	// in this fixture? no — actionable is non-empty, so auto-close
	// does not trigger). The only upsert path that runs is the
	// reconcile-log upsert in createOrUpdatePRFromPolicy. Two
	// identical sweeps → one upsert; second is suppressed by the
	// hash-tag match.
	if client.upsertCommentCalls != 1 {
		t.Errorf("expected exactly 1 UpsertPRComment call across 2 identical sweeps, got %d",
			client.upsertCommentCalls)
	}

	if len(client.upsertedComments) != 1 {
		t.Errorf("expected 1 stored comment, got %d", len(client.upsertedComments))
	}
}

func TestBuildReconcileLogEvents_DistinguishesOrphansAndActionable(t *testing.T) {
	t.Parallel()

	enabled := true
	allRules := []policy.FileRuleConfig{
		{Type: "file", Name: "codeowners", Enabled: &enabled},
		{Type: "file", Name: "dependabot", Enabled: &enabled},
		{Type: "file", Name: "renovate", Enabled: &enabled},
	}
	actionable := []policy.FileRuleConfig{
		{Type: "file", Name: "renovate", Enabled: &enabled},
	}
	removed := []string{"codeowners"}

	events := buildReconcileLogEvents(allRules, actionable, removed, nil, nil)

	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d: %v", len(events), events)
	}

	want := map[string]string{
		"codeowners": "orphan removed from branch",
		"dependabot": "satisfied on main",
		"renovate":   "still actionable",
	}

	for _, e := range events {
		if got, ok := want[e.Rule]; !ok {
			t.Errorf("unexpected rule %q in events", e.Rule)
		} else if e.Status != got {
			t.Errorf("rule %q status = %q, want %q", e.Rule, e.Status, got)
		}
	}
}

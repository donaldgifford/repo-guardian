package checker

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/policy"
)

// TestCheckRepoPolicy_DriftCounter_IncrementsWhenPROpenAndNoActionable
// covers the IMPL-0013 Phase 1 drift surface: when an open
// repo-guardian PR exists but every file rule is satisfied on the
// default branch, the policy engine should increment
// PROpenWithEmptyActionableTotal{org=...} so operators can size the
// drift before the Phase 3 convergence fix lands.
func TestCheckRepoPolicy_DriftCounter_IncrementsWhenPROpenAndNoActionable(t *testing.T) {
	const org = "drift-org"

	metrics.PROpenWithEmptyActionableTotal.Reset()

	cfg := policy.BuiltinDefaults()
	engine := testPolicyEngine(cfg)

	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: org, Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.branchSHAs[org+"/repo/main"] = "abc123"
	// Every built-in rule's file is present on the default branch.
	client.contents[org+"/repo/CODEOWNERS"] = true
	client.contents[org+"/repo/.github/dependabot.yml"] = true
	// An open repo-guardian PR exists despite the satisfied state.
	client.openPRs = []*ghclient.PullRequest{
		{Number: 7, Title: PRTitle, Head: BranchName, State: "open"},
	}

	if err := engine.CheckRepo(context.Background(), client, org, "repo"); err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if got := testutil.ToFloat64(metrics.PROpenWithEmptyActionableTotal.WithLabelValues(org)); got != 1 {
		t.Errorf("PROpenWithEmptyActionableTotal{org=%q} = %v, want 1", org, got)
	}
}

// TestCheckRepoPolicy_DriftCounter_DoesNotIncrement_WhenNoPROpen
// confirms the increment is gated on an open repo-guardian PR.
// Convergent state without a PR is the happy path and should not
// touch the counter.
func TestCheckRepoPolicy_DriftCounter_DoesNotIncrement_WhenNoPROpen(t *testing.T) {
	const org = "happy-org"

	metrics.PROpenWithEmptyActionableTotal.Reset()

	cfg := policy.BuiltinDefaults()
	engine := testPolicyEngine(cfg)

	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: org, Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.branchSHAs[org+"/repo/main"] = "abc123"
	client.contents[org+"/repo/CODEOWNERS"] = true
	client.contents[org+"/repo/.github/dependabot.yml"] = true

	if err := engine.CheckRepo(context.Background(), client, org, "repo"); err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if got := testutil.ToFloat64(metrics.PROpenWithEmptyActionableTotal.WithLabelValues(org)); got != 0 {
		t.Errorf("PROpenWithEmptyActionableTotal{org=%q} = %v, want 0", org, got)
	}
}

// TestCheckRepoPolicy_OpenPRsByRule_PopulatedWithActionableRules
// verifies that when an open repo-guardian PR exists with at least
// one actionable rule, every actionable rule is recorded in the
// OpenPRsByRule gauge under the PR's age bucket.
func TestCheckRepoPolicy_OpenPRsByRule_PopulatedWithActionableRules(t *testing.T) {
	const org = "gauge-org"

	metrics.OpenPRsByRule.Reset()

	cfg := policy.BuiltinDefaults()
	engine := testPolicyEngine(cfg)

	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: org, Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.branchSHAs[org+"/repo/main"] = "abc123"
	client.branchSHAs[org+"/repo/"+BranchName] = "def456"
	// Both rules actionable; PR opened 10 days ago (7-30d bucket).
	client.openPRs = []*ghclient.PullRequest{
		{
			Number: 9, Title: PRTitle, Head: BranchName, State: "open",
			CreatedAt: time.Now().Add(-10 * 24 * time.Hour),
		},
	}

	if err := engine.CheckRepo(context.Background(), client, org, "repo"); err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	for _, ruleName := range []string{"codeowners", "dependabot"} {
		got := testutil.ToFloat64(metrics.OpenPRsByRule.WithLabelValues(org, ruleName, metrics.PRAgeBucket7To30))
		if got != 1 {
			t.Errorf("OpenPRsByRule{org=%q, rule=%q, bucket=%q} = %v, want 1",
				org, ruleName, metrics.PRAgeBucket7To30, got)
		}
	}
}

// TestResetOpenPRsByRule_WipesAllSeries verifies that the reset
// helper clears every series. Sweepers call this at iteration start;
// stale {org, rule} combinations should disappear from the gauge.
func TestResetOpenPRsByRule_WipesAllSeries(t *testing.T) {
	t.Parallel()

	metrics.OpenPRsByRule.WithLabelValues("acme", "codeowners", metrics.PRAgeBucket1To7).Set(3)
	metrics.OpenPRsByRule.WithLabelValues("globex", "dependabot", metrics.PRAgeBucketGT30).Set(7)

	metrics.ResetOpenPRsByRule()

	for _, labels := range [][3]string{
		{"acme", "codeowners", metrics.PRAgeBucket1To7},
		{"globex", "dependabot", metrics.PRAgeBucketGT30},
	} {
		got := testutil.ToFloat64(metrics.OpenPRsByRule.WithLabelValues(labels[0], labels[1], labels[2]))
		if got != 0 {
			t.Errorf("after Reset, OpenPRsByRule{%v} = %v, want 0", labels, got)
		}
	}
}

// TestCheckRepoPolicy_DriftCounter_DoesNotIncrement_WhenActionable
// confirms the increment is gated on an empty actionable set.
// An open PR with at least one missing rule is the normal pre-merge
// state and should not touch the counter.
func TestCheckRepoPolicy_DriftCounter_DoesNotIncrement_WhenActionable(t *testing.T) {
	const org = "actionable-org"

	metrics.PROpenWithEmptyActionableTotal.Reset()

	cfg := policy.BuiltinDefaults()
	engine := testPolicyEngine(cfg)

	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: org, Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.branchSHAs[org+"/repo/main"] = "abc123"
	client.branchSHAs[org+"/repo/"+BranchName] = "def456"
	// CODEOWNERS still missing; Dependabot present. One rule actionable.
	client.contents[org+"/repo/.github/dependabot.yml"] = true
	client.openPRs = []*ghclient.PullRequest{
		{Number: 8, Title: PRTitle, Head: BranchName, State: "open"},
	}

	if err := engine.CheckRepo(context.Background(), client, org, "repo"); err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if got := testutil.ToFloat64(metrics.PROpenWithEmptyActionableTotal.WithLabelValues(org)); got != 0 {
		t.Errorf("PROpenWithEmptyActionableTotal{org=%q} = %v, want 0", org, got)
	}
}

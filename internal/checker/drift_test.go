package checker

import (
	"context"
	"testing"

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

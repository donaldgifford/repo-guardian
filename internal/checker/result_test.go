package checker

// Contract tests for the *CheckResult returned by CheckRepo
// (IMPL-0023 task 1.2). These pin the three properties the posture
// pipeline downstream depends on and cannot re-derive:
//
//  1. every *evaluated* rule produces an outcome, across all three
//     rule kinds — not just the actionable ones, because the satisfied
//     ones are the denominator of every compliance percentage;
//  2. every *skipped* rule produces none, so scope and ignore lists do
//     not dilute that denominator with repos the rule never applied to;
//  3. skip paths return an empty-but-non-nil result while error paths
//     return nil, because those two drive opposite reconciliations in
//     the worker write-back (clear this repo's rows vs. touch nothing).

import (
	"context"
	"errors"
	"sort"
	"testing"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/policy"
)

// outcomeKey renders an outcome as "kind/name=actionable" for
// order-independent set comparison.
func outcomeKeys(t *testing.T, res *CheckResult) []string {
	t.Helper()

	if res == nil {
		t.Fatal("CheckResult is nil, want a result")
	}

	keys := make([]string, 0, len(res.Outcomes))
	for _, o := range res.Outcomes {
		state := "satisfied"
		if o.Actionable {
			state = "actionable"
		}

		keys = append(keys, string(o.Kind)+"/"+o.RuleName+"="+state)
	}

	sort.Strings(keys)

	return keys
}

func assertOutcomes(t *testing.T, res *CheckResult, want []string) {
	t.Helper()

	got := outcomeKeys(t, res)

	sort.Strings(want)

	if len(got) != len(want) {
		t.Fatalf("outcomes = %v, want %v", got, want)
	}

	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("outcomes = %v, want %v", got, want)
		}
	}
}

// mixedKindPolicy builds a policy with one rule of each kind so a
// single CheckRepo exercises all three outcome sources.
func mixedKindPolicy() *policy.PolicyConfig {
	return &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		FileRules: []policy.FileRuleConfig{
			{Name: "codeowners", Type: "file", Paths: []string{"CODEOWNERS"}, Template: "codeowners.tmpl"},
		},
		SettingRules: []policy.SettingRuleConfig{
			{Name: "enable_issues", Property: "has_issues", Expected: true},
		},
		BranchProtectionRules: []policy.BranchProtectionRuleConfig{
			{Name: "protect_main", Branch: "main", RequirePR: true},
		},
	}
}

func TestCheckResult_CoversAllThreeRuleKinds(t *testing.T) {
	t.Parallel()

	engine := testPolicyEngine(mixedKindPolicy())
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.branchSHAs["org/repo/main"] = "abc123"
	// CODEOWNERS present -> file rule satisfied.
	client.contents["org/repo/CODEOWNERS"] = true
	// has_issues true -> setting rule satisfied.
	client.repoSettings = &ghclient.RepoSettings{HasIssues: true}
	// No rulesets at all -> branch protection actionable. This verdict
	// has never existed before: BP emitted checked/remediated counters
	// but nothing that said "still wrong" (INV-0013 Finding B).
	client.rulesets = nil

	res, err := engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	assertOutcomes(t, res, []string{
		"file/codeowners=satisfied",
		"setting/enable_issues=satisfied",
		"branch_protection/protect_main=actionable",
	})
}

func TestCheckResult_FileRuleActionableWhenMissing(t *testing.T) {
	t.Parallel()

	cfg := mixedKindPolicy()
	cfg.SettingRules = nil
	cfg.BranchProtectionRules = nil

	engine := testPolicyEngine(cfg)
	engine.dryRun = true // keep the test off the PR-creation path

	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.branchSHAs["org/repo/main"] = "abc123"

	res, err := engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	assertOutcomes(t, res, []string{"file/codeowners=actionable"})
}

// TestCheckResult_RemediatedSettingIsNotActionable locks the semantic
// that separates posture from event counting: a mismatch this pass
// fixed leaves the repo compliant, so it must not be reported as
// failing. Reporting it would stamp actionable_since on one tick and
// clear it on the next, manufacturing a flap out of self-healing.
func TestCheckResult_RemediatedSettingIsNotActionable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		remediate  bool
		dryRun     bool
		wantAction bool
	}{
		{name: "mismatch remediated in-band", remediate: true, dryRun: false, wantAction: false},
		{name: "mismatch left standing", remediate: false, dryRun: false, wantAction: true},
		{name: "mismatch dry-run only", remediate: true, dryRun: true, wantAction: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := &policy.PolicyConfig{
				Guardian: policy.BuiltinDefaults().Guardian,
				SettingRules: []policy.SettingRuleConfig{
					{Name: "enable_issues", Property: "has_issues", Expected: true, Remediate: tt.remediate},
				},
			}
			cfg.Guardian.DryRun = tt.dryRun

			engine := testPolicyEngine(cfg)
			client := newMockClient()
			client.repo = &ghclient.Repository{
				Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
			}
			client.repoSettings = &ghclient.RepoSettings{HasIssues: false}

			res, err := engine.CheckRepo(context.Background(), client, "org", "repo")
			if err != nil {
				t.Fatalf("CheckRepo: %v", err)
			}

			want := "setting/enable_issues=satisfied"
			if tt.wantAction {
				want = "setting/enable_issues=actionable"
			}

			assertOutcomes(t, res, []string{want})
		})
	}
}

// TestCheckResult_IgnoredRuleProducesNoOutcome guards the denominator:
// a rule that does not apply to a repo must not appear as "tracked and
// compliant", or every per-rule compliance percentage silently inflates
// with the repos that rule was never meant to cover.
func TestCheckResult_IgnoredRuleProducesNoOutcome(t *testing.T) {
	t.Parallel()

	cfg := mixedKindPolicy()
	cfg.SettingRules = nil
	cfg.BranchProtectionRules = nil
	cfg.FileRules[0].Ignore = &policy.IgnoreConfig{Repos: []string{"org/repo"}}

	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.branchSHAs["org/repo/main"] = "abc123"

	res, err := engine.CheckRepo(context.Background(), client, "org", "repo")
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	assertOutcomes(t, res, nil)
}

// TestCheckResult_SkipPathsReturnEmptyNotNil pins the distinction the
// write-back reconciliation depends on. An empty result means "this
// repo has no applicable rules" and clears its rows; nil means "we
// learned nothing" and leaves them alone. Collapsing the two either
// strands rows for out-of-scope repos forever or wipes them on every
// transient API error.
//
// Archived and forked repos used to be a case here. INV-0015 made them
// DURABLE skips, which return a *SkippedError and a nil result so the
// worker can park the row — the empty-result-clears-posture call moved
// with them, to Pool.park. TestCheckRepo_Skips covers the engine half
// and TestPool_ArchivedRepo_ClearsPosture the worker half; what is left
// here is the skips that stay in the normal flow.
func TestCheckResult_SkipPathsReturnEmptyNotNil(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(*policy.PolicyConfig, *mockClient)
	}{
		{
			name: "global ignore list",
			setup: func(cfg *policy.PolicyConfig, _ *mockClient) {
				cfg.IgnoreList = policy.IgnoreConfig{Repos: []string{"org/repo"}}
			},
		},
		{
			name: "out of policy scope",
			setup: func(cfg *policy.PolicyConfig, _ *mockClient) {
				cfg.Scope = &policy.ScopeConfig{Orgs: []string{"someotherorg"}}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := mixedKindPolicy()
			cfg.FileRules[0].Scope = &policy.ScopeConfig{Orgs: []string{"*"}}
			cfg.SettingRules[0].Scope = &policy.ScopeConfig{Orgs: []string{"*"}}
			cfg.BranchProtectionRules[0].Scope = &policy.ScopeConfig{Orgs: []string{"*"}}

			client := newMockClient()
			client.repo = &ghclient.Repository{
				Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
			}
			client.branchSHAs["org/repo/main"] = "abc123"

			tt.setup(cfg, client)

			res, err := testPolicyEngine(cfg).CheckRepo(context.Background(), client, "org", "repo")
			if err != nil {
				t.Fatalf("CheckRepo: %v", err)
			}

			if res == nil {
				t.Fatal("CheckRepo() result = nil on a skip path, want empty-but-non-nil so the repo's rule rows get reconciled away")
			}

			if len(res.Outcomes) != 0 {
				t.Errorf("CheckRepo() outcomes = %v, want none on a skip path", outcomeKeys(t, res))
			}
		})
	}
}

// TestCheckResult_ErrorPathReturnsNil is the other half of the pair
// above: a check that failed mid-way has no trustworthy verdict for the
// rules it never reached, so it must not hand back a partial set that
// delete-not-in would read as "these rules no longer apply".
func TestCheckResult_ErrorPathReturnsNil(t *testing.T) {
	t.Parallel()

	engine := testPolicyEngine(mixedKindPolicy())
	client := newMockClient()
	client.getRepoErr = errors.New("boom")

	res, err := engine.CheckRepo(context.Background(), client, "org", "repo")
	if err == nil {
		t.Fatal("CheckRepo() error = nil, want the injected failure")
	}

	if res != nil {
		t.Errorf("CheckRepo() result = %+v on an error path, want nil", res)
	}
}

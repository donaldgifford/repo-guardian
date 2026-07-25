package checker

import (
	"testing"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/policy"
)

// Existing-PR detection tests for INV-0011 B4 (IMPL-0021 task 6.4).

func ruleWithSearchTerms(terms ...string) *policy.FileRuleConfig {
	return &policy.FileRuleConfig{
		Type:  "file",
		Name:  "codeowners",
		Check: string(policy.CheckExists),
		Paths: []string{"CODEOWNERS"},
		PR:    &policy.PRConfig{SearchTerms: terms},
	}
}

func TestForeignPRForRule(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		rule      *policy.FileRuleConfig
		openPRs   []*ghclient.PullRequest
		wantPR    int
		wantTerm  string
		wantMatch bool
	}{
		{
			name: "human PR matching on title",
			rule: ruleWithSearchTerms("codeowners"),
			openPRs: []*ghclient.PullRequest{
				{Number: 12, Title: "Add CODEOWNERS for the platform team", Head: "alice/owners"},
			},
			wantPR:    12,
			wantTerm:  "codeowners",
			wantMatch: true,
		},
		{
			name: "human PR matching on branch name",
			rule: ruleWithSearchTerms("codeowners"),
			openPRs: []*ghclient.PullRequest{
				{Number: 13, Title: "chore: tidy up", Head: "bob/add-codeowners"},
			},
			wantPR:    13,
			wantTerm:  "codeowners",
			wantMatch: true,
		},
		{
			// The whole point of B4: our own reconcile PR is handled by
			// the converge path, never by search-term suppression.
			name: "our own reconcile PR is skipped",
			rule: ruleWithSearchTerms("codeowners"),
			openPRs: []*ghclient.PullRequest{
				{Number: 1, Title: "chore: add CODEOWNERS", Head: BranchName},
			},
			wantMatch: false,
		},
		{
			// Era collision: an absent rule removing what an add-era
			// rule installed shares vocabulary with the add-era PR
			// title by construction. Both PRs are ours.
			name: "our own add-era PR does not suppress a removal rule",
			rule: ruleWithSearchTerms("dependabot"),
			openPRs: []*ghclient.PullRequest{
				{Number: 1, Title: "chore: add missing repo configuration files (dependabot)", Head: BranchName},
			},
			wantMatch: false,
		},
		{
			name: "our PR skipped but a human PR still matches",
			rule: ruleWithSearchTerms("codeowners"),
			openPRs: []*ghclient.PullRequest{
				{Number: 1, Title: "chore: add CODEOWNERS", Head: BranchName},
				{Number: 22, Title: "Own the CODEOWNERS file properly", Head: "carol/owners"},
			},
			wantPR:    22,
			wantTerm:  "codeowners",
			wantMatch: true,
		},
		{
			// Belt-and-braces for rules constructed in Go rather than
			// parsed from HCL, where load validation never ran.
			name: "blank search term never matches",
			rule: ruleWithSearchTerms(""),
			openPRs: []*ghclient.PullRequest{
				{Number: 31, Title: "unrelated work", Head: "dave/whatever"},
			},
			wantMatch: false,
		},
		{
			name:      "rule with no pr block",
			rule:      &policy.FileRuleConfig{Type: "file", Name: "codeowners"},
			openPRs:   []*ghclient.PullRequest{{Number: 41, Title: "codeowners", Head: "x"}},
			wantMatch: false,
		},
		{
			name:      "no open PRs",
			rule:      ruleWithSearchTerms("codeowners"),
			wantMatch: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pr, term := foreignPRForRule(tt.openPRs, tt.rule)

			if got := pr != nil; got != tt.wantMatch {
				t.Fatalf("foreignPRForRule(...) matched = %v, want %v (pr=%v)", got, tt.wantMatch, pr)
			}

			if !tt.wantMatch {
				return
			}

			if pr.Number != tt.wantPR {
				t.Errorf("foreignPRForRule(...) PR number = %d, want %d", pr.Number, tt.wantPR)
			}

			if term != tt.wantTerm {
				t.Errorf("foreignPRForRule(...) term = %q, want %q", term, tt.wantTerm)
			}
		})
	}
}

// End-to-end: a rule whose custom PR title contains its own search term
// used to match the PR it had just opened, emptying the actionable set
// and auto-closing the PR — which the next sweep re-opened, forever.
func TestForeignPRForRule_CustomTitleDoesNotSelfSuppress(t *testing.T) {
	defaults := policy.BuiltinDefaults()

	// Exactly one rule, so a self-match empties the actionable set
	// outright and the auto-close path is reachable.
	var only policy.FileRuleConfig

	for i := range defaults.FileRules {
		if defaults.FileRules[i].Name == "codeowners" {
			only = defaults.FileRules[i]
		}
	}

	only.PR = &policy.PRConfig{SearchTerms: []string{"codeowners"}}

	cfg := &policy.PolicyConfig{
		Guardian:  defaults.Guardian,
		FileRules: []policy.FileRuleConfig{only},
	}

	s := newStagedConvergenceWithPolicy(t, cfg)

	// Sweep 1 opens the PR; simulate GitHub returning it with a title
	// that happens to carry the rule's own search term.
	s.sweep()
	s.client.openPRs = []*ghclient.PullRequest{
		{Number: 1, Title: "chore: add CODEOWNERS", Head: s.branch, State: "open"},
	}
	s.client.branchSHAs[s.org+"/"+s.repo+"/"+s.branch] = "branch-sha"
	s.client.closedPRNumber = 0

	// Sweep 2: CODEOWNERS is still missing on the default branch, so
	// the rule must stay actionable and the PR must stay open.
	s.sweep()

	if s.client.closedPRNumber != 0 {
		t.Errorf("PR #%d auto-closed while its rule was still unsatisfied", s.client.closedPRNumber)
	}
}

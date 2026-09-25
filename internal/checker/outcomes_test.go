package checker

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/findings"
	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/policy"
)

// Golden outcomes: one engine scenario per reason code and per
// remediation value (IMPL-0025 task 4.11). Each pins the full recorded
// facets and the evidence JSON the store will persist.
//
// Two values are covered elsewhere by construction: migrated_from_v1 is
// written only by the v1 backfill (Phase 7), never by the engine, and
// gate_error needs a referee evaluation that fails while the rest of
// the check succeeds, which TestOutcomeGolden_GateError pins at the
// gateClosedDetail seam.

const (
	goldenOwner = "org"
	goldenRepo  = "repo"
)

type goldenOutcome struct {
	status      findings.Status
	reason      findings.Reason
	remediation findings.Remediation
	evidence    string // JSON; "null" when there is none
	pr          string // JSON of PR or ForeignPR; "" when neither
}

func codeownersRule() policy.FileRuleConfig {
	return policy.FileRuleConfig{
		Type: "file", Name: "codeowners", Check: "exists",
		Paths: []string{".github/CODEOWNERS"}, Target: ".github/CODEOWNERS", Template: "codeowners",
	}
}

func goldenClient() *mockClient {
	client := newMockClient()
	client.repo = &ghclient.Repository{Owner: goldenOwner, Name: goldenRepo, HasBranch: true, DefaultRef: "main"}
	client.branchSHAs["org/repo/main"] = "abc123"

	return client
}

func fileOnly(rules ...policy.FileRuleConfig) *policy.PolicyConfig {
	return &policy.PolicyConfig{Guardian: policy.BuiltinDefaults().Guardian, FileRules: rules}
}

func settingPolicy(remediate, dryRun bool) *policy.PolicyConfig {
	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		SettingRules: []policy.SettingRuleConfig{
			{Name: "enable_issues", Property: "has_issues", Expected: true, Remediate: remediate},
		},
	}
	cfg.Guardian.DryRun = dryRun

	return cfg
}

func bpPolicy() *policy.PolicyConfig {
	return &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		BranchProtectionRules: []policy.BranchProtectionRuleConfig{
			{Name: "protect_main", Branch: "main", RequirePR: true, RequiredApprovals: 1},
		},
	}
}

var goldenPRCreated = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

func TestOutcomeGolden(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		cfg   func() *policy.PolicyConfig
		setup func(*mockClient)
		rule  string
		want  goldenOutcome
	}{
		{
			name: "compliant, remediation none",
			cfg:  func() *policy.PolicyConfig { return fileOnly(codeownersRule()) },
			setup: func(c *mockClient) {
				c.contents["org/repo/.github/CODEOWNERS"] = true
			},
			rule: "codeowners",
			want: goldenOutcome{status: findings.StatusCompliant, remediation: findings.RemediationNone, evidence: "null"},
		},
		{
			name: "file_missing",
			cfg:  func() *policy.PolicyConfig { return fileOnly(codeownersRule()) },
			rule: "codeowners",
			want: goldenOutcome{
				status: findings.StatusNonCompliant, reason: findings.ReasonFileMissing, remediation: findings.RemediationNone,
				evidence: `{"paths_checked":[".github/CODEOWNERS"]}`,
			},
		},
		{
			name: "assertion_failed",
			cfg: func() *policy.PolicyConfig {
				r := codeownersRule()
				r.Name, r.Check, r.Paths, r.Target = "catalog", "contains", []string{"catalog-info.yaml"}, "catalog-info.yaml"
				r.Assertions = []policy.AssertionConfig{{YAMLPath: "spec.owner", Contains: "team", Message: "must have team owner"}}

				return fileOnly(r)
			},
			setup: func(c *mockClient) {
				c.contents["org/repo/catalog-info.yaml"] = true
				c.fileContents["org/repo/catalog-info.yaml"] = "spec:\n  owner: alice\n"
			},
			rule: "catalog",
			want: goldenOutcome{
				status: findings.StatusNonCompliant, reason: findings.ReasonAssertionFailed, remediation: findings.RemediationNone,
				evidence: `{"path":"catalog-info.yaml","message":"must have team owner"}`,
			},
		},
		{
			name: "content_differs",
			cfg: func() *policy.PolicyConfig {
				r := codeownersRule()
				r.Check = "exact"

				return fileOnly(r)
			},
			setup: func(c *mockClient) {
				c.contents["org/repo/.github/CODEOWNERS"] = true
				c.fileContents["org/repo/.github/CODEOWNERS"] = "* @someone-else\n"
			},
			rule: "codeowners",
			want: goldenOutcome{
				status: findings.StatusNonCompliant, reason: findings.ReasonContentDiffers, remediation: findings.RemediationNone,
				evidence: `{"path":".github/CODEOWNERS","comparison":"bytes"}`,
			},
		},
		{
			name: "forbidden_present",
			cfg:  func() *policy.PolicyConfig { return fileOnly(absentRule("no_dependabot")) },
			setup: func(c *mockClient) {
				c.contents["org/repo/.github/dependabot.yml"] = true
			},
			rule: "no_dependabot",
			want: goldenOutcome{
				status: findings.StatusNonCompliant, reason: findings.ReasonForbiddenPresent, remediation: findings.RemediationNone,
				evidence: `{"path":".github/dependabot.yml"}`,
			},
		},
		{
			name: "foreign_pr_open with foreign_pr",
			cfg: func() *policy.PolicyConfig {
				r := codeownersRule()
				r.PR = &policy.PRConfig{SearchTerms: []string{"codeowners"}}

				return fileOnly(r)
			},
			setup: func(c *mockClient) {
				c.openPRs = []*ghclient.PullRequest{{
					Number: 7, Title: "Add CODEOWNERS", Head: "human/codeowners", State: "open",
					HTMLURL: "https://github.com/org/repo/pull/7",
				}}
			},
			rule: "codeowners",
			want: goldenOutcome{
				status: findings.StatusNonCompliant, reason: findings.ReasonForeignPROpen, remediation: findings.RemediationForeignPR,
				evidence: `{}`,
				pr:       `{"number":7,"url":"https://github.com/org/repo/pull/7","head":"human/codeowners","matched_term":"codeowners"}`,
			},
		},
		{
			name: "pr_open",
			cfg:  func() *policy.PolicyConfig { return fileOnly(codeownersRule()) },
			setup: func(c *mockClient) {
				c.openPRs = []*ghclient.PullRequest{{
					Number: 9, Title: PRTitle, Head: BranchName, State: "open",
					HTMLURL: "https://github.com/org/repo/pull/9", CreatedAt: goldenPRCreated,
				}}
				c.branchSHAs["org/repo/"+BranchName] = "branch-sha"
			},
			rule: "codeowners",
			want: goldenOutcome{
				status: findings.StatusNonCompliant, reason: findings.ReasonFileMissing, remediation: findings.RemediationPROpen,
				evidence: `{"paths_checked":[".github/CODEOWNERS"]}`,
				pr:       `{"number":9,"url":"https://github.com/org/repo/pull/9","created_at":"2026-09-01T00:00:00Z"}`,
			},
		},
		{
			name: "file rule dry_run",
			cfg: func() *policy.PolicyConfig {
				cfg := fileOnly(codeownersRule())
				cfg.Guardian.DryRun = true

				return cfg
			},
			rule: "codeowners",
			want: goldenOutcome{
				status: findings.StatusNonCompliant, reason: findings.ReasonFileMissing, remediation: findings.RemediationDryRun,
				evidence: `{"paths_checked":[".github/CODEOWNERS"]}`,
			},
		},
		{
			name: "setting_mismatch with disabled",
			cfg:  func() *policy.PolicyConfig { return settingPolicy(false, false) },
			setup: func(c *mockClient) {
				c.repoSettings = &ghclient.RepoSettings{HasIssues: false}
			},
			rule: "enable_issues",
			want: goldenOutcome{
				status: findings.StatusNonCompliant, reason: findings.ReasonSettingMismatch, remediation: findings.RemediationDisabled,
				evidence: `{"property":"has_issues","expected":"true","actual":"false"}`,
			},
		},
		{
			name: "setting dry_run",
			cfg:  func() *policy.PolicyConfig { return settingPolicy(true, true) },
			setup: func(c *mockClient) {
				c.repoSettings = &ghclient.RepoSettings{HasIssues: false}
			},
			rule: "enable_issues",
			want: goldenOutcome{
				status: findings.StatusNonCompliant, reason: findings.ReasonSettingMismatch, remediation: findings.RemediationDryRun,
				evidence: `{"property":"has_issues","expected":"true","actual":"false"}`,
			},
		},
		{
			name: "setting applied",
			cfg:  func() *policy.PolicyConfig { return settingPolicy(true, false) },
			setup: func(c *mockClient) {
				c.repoSettings = &ghclient.RepoSettings{HasIssues: false}
			},
			rule: "enable_issues",
			want: goldenOutcome{status: findings.StatusCompliant, remediation: findings.RemediationApplied, evidence: "null"},
		},
		{
			name: "ruleset_missing",
			cfg:  bpPolicy,
			rule: "protect_main",
			want: goldenOutcome{
				status: findings.StatusNonCompliant, reason: findings.ReasonRulesetMissing, remediation: findings.RemediationDisabled,
				evidence: `{"branch":"main"}`,
			},
		},
		{
			name: "ruleset_mismatch",
			cfg:  bpPolicy,
			setup: func(c *mockClient) {
				c.rulesets = []*ghclient.Ruleset{{
					ID: 11, Conditions: &ghclient.RulesetConditions{IncludePatterns: []string{"refs/heads/main"}},
					RequirePullRequest: &ghclient.RulesetPullRequest{RequiredApprovals: 0},
				}}
			},
			rule: "protect_main",
			want: goldenOutcome{
				status: findings.StatusNonCompliant, reason: findings.ReasonRulesetMismatch, remediation: findings.RemediationDisabled,
				evidence: `{"branch":"main","ruleset_id":11,"mismatches":["required_approvals: got 0, want 1"]}`,
			},
		},
		{
			name: "branch_missing",
			cfg: func() *policy.PolicyConfig {
				cfg := bpPolicy()
				cfg.BranchProtectionRules[0].Branch = "develop"

				return cfg
			},
			rule: "protect_main",
			want: goldenOutcome{
				status: findings.StatusNotApplicable, reason: findings.ReasonBranchMissing, remediation: findings.RemediationNone,
				evidence: `{"branch":"develop"}`,
			},
		},
		{
			name: "out_of_scope_policy",
			cfg: func() *policy.PolicyConfig {
				r := codeownersRule()
				r.Scope = &policy.ScopeConfig{Orgs: []string{"*"}}
				cfg := fileOnly(r)
				cfg.Scope = &policy.ScopeConfig{Orgs: []string{"elsewhere"}}

				return cfg
			},
			rule: "codeowners",
			want: goldenOutcome{
				status: findings.StatusNotApplicable, reason: findings.ReasonOutOfScopePolicy, remediation: findings.RemediationNone,
				evidence: `{}`,
			},
		},
		{
			name: "out_of_scope_rule",
			cfg: func() *policy.PolicyConfig {
				r := codeownersRule()
				r.Scope = &policy.ScopeConfig{Orgs: []string{"elsewhere"}}
				cfg := fileOnly(r)
				cfg.Scope = &policy.ScopeConfig{Orgs: []string{goldenOwner, "elsewhere"}}

				return cfg
			},
			rule: "codeowners",
			want: goldenOutcome{
				status: findings.StatusNotApplicable, reason: findings.ReasonOutOfScopeRule, remediation: findings.RemediationNone,
				evidence: `{}`,
			},
		},
		{
			name: "ignored_global",
			cfg: func() *policy.PolicyConfig {
				cfg := fileOnly(codeownersRule())
				cfg.IgnoreList = policy.IgnoreConfig{Repos: []string{"org/*"}}

				return cfg
			},
			rule: "codeowners",
			want: goldenOutcome{
				status: findings.StatusNotApplicable, reason: findings.ReasonIgnoredGlobal, remediation: findings.RemediationNone,
				evidence: `{"pattern":"org/*"}`,
			},
		},
		{
			name: "ignored_rule",
			cfg: func() *policy.PolicyConfig {
				r := codeownersRule()
				r.Ignore = &policy.IgnoreConfig{Repos: []string{"org/repo"}}

				return fileOnly(r)
			},
			rule: "codeowners",
			want: goldenOutcome{
				status: findings.StatusNotApplicable, reason: findings.ReasonIgnoredRule, remediation: findings.RemediationNone,
				evidence: `{"pattern":"org/repo"}`,
			},
		},
		{
			name: "gate_closed",
			cfg:  renovateFirstPolicy,
			rule: "no_dependabot",
			want: goldenOutcome{
				status: findings.StatusNotApplicable, reason: findings.ReasonGateClosed, remediation: findings.RemediationNone,
				evidence: `{"referee":"renovate_config"}`,
			},
		},
		{
			name: "empty_repository",
			cfg:  func() *policy.PolicyConfig { return fileOnly(codeownersRule()) },
			setup: func(c *mockClient) {
				c.repo.HasBranch, c.repo.DefaultRef = false, ""
			},
			rule: "codeowners",
			want: goldenOutcome{
				status: findings.StatusNotApplicable, reason: findings.ReasonEmptyRepository, remediation: findings.RemediationNone,
				evidence: `{}`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := goldenClient()
			if tt.setup != nil {
				tt.setup(client)
			}

			res, err := testPolicyEngine(tt.cfg()).CheckRepo(context.Background(), client, goldenOwner, goldenRepo)
			if err != nil {
				t.Fatalf("CheckRepo: %v", err)
			}

			assertGolden(t, findOutcome(t, res, tt.rule), &tt.want)
		})
	}
}

func TestOutcomeGolden_GateError(t *testing.T) {
	t.Parallel()

	g := &gateEvaluator{memo: map[string]gateResult{
		"renovate_config": {reason: gateReasonError, err: errors.New("GET contents: 502")},
	}}

	d := gateClosedDetail(g, "renovate_config")
	o := d.outcome("no_dependabot", RuleKindFile)

	assertGolden(t, &o, &goldenOutcome{
		status: findings.StatusUnknown, reason: findings.ReasonGateError,
		evidence: `{"referee":"renovate_config","error":"GET contents: 502"}`,
	})
}

func findOutcome(t *testing.T, res *CheckResult, rule string) *RuleOutcome {
	t.Helper()

	if res == nil {
		t.Fatal("CheckResult is nil")
	}

	for i := range res.Outcomes {
		if res.Outcomes[i].RuleName == rule {
			return &res.Outcomes[i]
		}
	}

	t.Fatalf("no outcome for rule %q in %+v", rule, res.Outcomes)

	return nil
}

func assertGolden(t *testing.T, got *RuleOutcome, want *goldenOutcome) {
	t.Helper()

	if got.Status != want.status || got.Reason != want.reason || got.Remediation != want.remediation {
		t.Errorf("facets = %s/%s/%s, want %s/%s/%s",
			got.Status, got.Reason, got.Remediation, want.status, want.reason, want.remediation)
	}

	if want.status != findings.StatusCompliant && got.Evidence != nil && got.Evidence.Reason() != got.Reason {
		t.Errorf("evidence type %T explains %q, but the reason is %q", got.Evidence, got.Evidence.Reason(), got.Reason)
	}

	ev, err := json.Marshal(got.Evidence)
	if err != nil {
		t.Fatalf("marshal evidence: %v", err)
	}

	if string(ev) != want.evidence {
		t.Errorf("evidence = %s, want %s", ev, want.evidence)
	}

	var pr any
	switch {
	case got.PR != nil:
		pr = got.PR
	case got.ForeignPR != nil:
		pr = got.ForeignPR
	}

	gotPR := ""
	if pr != nil {
		b, err := json.Marshal(pr)
		if err != nil {
			t.Fatalf("marshal pr: %v", err)
		}

		gotPR = string(b)
	}

	if gotPR != want.pr {
		t.Errorf("pr = %s, want %s", gotPR, want.pr)
	}
}

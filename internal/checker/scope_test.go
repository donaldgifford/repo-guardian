package checker

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/policy"
)

// outOfScopeCount returns the current value of OutOfScopeTotal for the
// given level/org pair. The Prometheus testutil package returns a float;
// the engine only ever increments by integer counts.
func outOfScopeCount(t *testing.T, level, org string) float64 {
	t.Helper()

	return testutil.ToFloat64(metrics.OutOfScopeTotal.WithLabelValues(level, org))
}

func resetOutOfScope(t *testing.T) {
	t.Helper()
	metrics.OutOfScopeTotal.Reset()
}

func boolPtr(b bool) *bool { return &b }

func TestPolicyEngine_LegacyMode_NoScopeFiltering(t *testing.T) {
	resetOutOfScope(t)

	// Legacy mode: cfg.Scope == nil, no rule has Scope set. Existing
	// behavior must be preserved — repo is processed normally.
	cfg := policy.BuiltinDefaults()
	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "anyorg", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.branchSHAs["anyorg/repo/main"] = "abc123"

	if err := engine.CheckRepo(context.Background(), client, "anyorg", "repo"); err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if client.createdPR == nil {
		t.Error("expected PR for missing files in legacy mode")
	}

	if got := outOfScopeCount(t, "policy", "anyorg"); got != 0 {
		t.Errorf("policy out_of_scope counter = %v, want 0", got)
	}

	if got := outOfScopeCount(t, "rule", "anyorg"); got != 0 {
		t.Errorf("rule out_of_scope counter = %v, want 0", got)
	}
}

func TestPolicyEngine_StrictMode_PolicyScopeRejectsRepo(t *testing.T) {
	resetOutOfScope(t)

	// Strict mode: cfg.Scope rejects the repo before any rule evaluates.
	// Counter should be incremented once per enabled rule across all
	// rule types (3 file rules + 1 setting rule + 1 bp rule = 5).
	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		Scope:    &policy.ScopeConfig{Orgs: []string{"myorg-*"}},
		FileRules: []policy.FileRuleConfig{
			{
				Type: "file", Name: "codeowners",
				Paths:    []string{"CODEOWNERS"},
				Target:   "CODEOWNERS",
				Template: "codeowners",
				Scope:    &policy.ScopeConfig{Orgs: []string{"*"}},
			},
			{
				Type: "file", Name: "dependabot",
				Paths:    []string{".github/dependabot.yml"},
				Target:   ".github/dependabot.yml",
				Template: "dependabot",
				Scope:    &policy.ScopeConfig{Orgs: []string{"*"}},
			},
			{
				Type: "file", Name: "disabled-rule",
				Paths:    []string{"disabled.txt"},
				Target:   "disabled.txt",
				Template: "codeowners",
				Enabled:  boolPtr(false),
				Scope:    &policy.ScopeConfig{Orgs: []string{"*"}},
			},
		},
		SettingRules: []policy.SettingRuleConfig{
			{
				Name: "issues_on", Property: "has_issues", Expected: true,
				Scope: &policy.ScopeConfig{Orgs: []string{"*"}},
			},
		},
		BranchProtectionRules: []policy.BranchProtectionRuleConfig{
			{
				Name: "main_ruleset", Branch: "main",
				Scope: &policy.ScopeConfig{Orgs: []string{"*"}},
			},
		},
	}

	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "otherorg", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.branchSHAs["otherorg/repo/main"] = "abc123"

	if err := engine.CheckRepo(context.Background(), client, "otherorg", "repo"); err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if client.createdPR != nil {
		t.Error("expected no PR when repo is out of policy scope")
	}

	// 2 enabled file rules + 1 setting rule + 1 bp rule = 4.
	const wantPolicyCount = 4
	if got := outOfScopeCount(t, "policy", "otherorg"); got != wantPolicyCount {
		t.Errorf("policy out_of_scope counter = %v, want %v", got, wantPolicyCount)
	}

	if got := outOfScopeCount(t, "rule", "otherorg"); got != 0 {
		t.Errorf("rule out_of_scope counter = %v, want 0", got)
	}
}

func TestPolicyEngine_StrictMode_RuleUniversalApplies(t *testing.T) {
	resetOutOfScope(t)

	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		Scope:    &policy.ScopeConfig{Orgs: []string{"myorg-*"}},
		FileRules: []policy.FileRuleConfig{
			{
				Type: "file", Name: "codeowners",
				Paths:    []string{"CODEOWNERS"},
				Target:   "CODEOWNERS",
				Template: "codeowners",
				Scope:    &policy.ScopeConfig{Orgs: []string{"*"}},
			},
		},
	}

	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "myorg-prod", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.branchSHAs["myorg-prod/repo/main"] = "abc123"

	if err := engine.CheckRepo(context.Background(), client, "myorg-prod", "repo"); err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if client.createdPR == nil {
		t.Error("expected PR when universal rule applies to in-scope repo")
	}

	if got := outOfScopeCount(t, "policy", "myorg-prod"); got != 0 {
		t.Errorf("policy out_of_scope counter = %v, want 0", got)
	}

	if got := outOfScopeCount(t, "rule", "myorg-prod"); got != 0 {
		t.Errorf("rule out_of_scope counter = %v, want 0", got)
	}
}

func TestPolicyEngine_StrictMode_RuleSubsetApplies(t *testing.T) {
	resetOutOfScope(t)

	// Top-level scope admits both prod and staging. The rule is scoped
	// only to prod. A staging repo passes the policy gate but is rejected
	// by the rule gate.
	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		Scope:    &policy.ScopeConfig{Orgs: []string{"myorg-prod", "myorg-staging"}},
		FileRules: []policy.FileRuleConfig{
			{
				Type: "file", Name: "codeowners",
				Paths:    []string{"CODEOWNERS"},
				Target:   "CODEOWNERS",
				Template: "codeowners",
				Scope:    &policy.ScopeConfig{Orgs: []string{"myorg-prod"}},
			},
		},
	}

	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "myorg-staging", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.branchSHAs["myorg-staging/repo/main"] = "abc123"

	if err := engine.CheckRepo(context.Background(), client, "myorg-staging", "repo"); err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if client.createdPR != nil {
		t.Error("expected no PR when rule scope excludes the org")
	}

	if got := outOfScopeCount(t, "policy", "myorg-staging"); got != 0 {
		t.Errorf("policy out_of_scope counter = %v, want 0", got)
	}

	if got := outOfScopeCount(t, "rule", "myorg-staging"); got != 1 {
		t.Errorf("rule out_of_scope counter = %v, want 1", got)
	}
}

func TestPolicyEngine_StrictMode_OutOfScopeWinsOverIgnore(t *testing.T) {
	resetOutOfScope(t)

	// Rule has both scope (excludes the org) and ignore (matches the repo).
	// Scope is checked first, so out_of_scope increments and ignore does not.
	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		Scope:    &policy.ScopeConfig{Orgs: []string{"myorg-prod", "myorg-staging"}},
		FileRules: []policy.FileRuleConfig{
			{
				Type: "file", Name: "codeowners",
				Paths:    []string{"CODEOWNERS"},
				Target:   "CODEOWNERS",
				Template: "codeowners",
				Scope:    &policy.ScopeConfig{Orgs: []string{"myorg-prod"}},
				Ignore:   &policy.IgnoreConfig{Repos: []string{"myorg-staging/repo"}},
			},
		},
	}

	beforeIgnored := testutil.ToFloat64(metrics.IgnoredTotal.WithLabelValues("rule", "myorg-staging"))

	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "myorg-staging", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.branchSHAs["myorg-staging/repo/main"] = "abc123"

	if err := engine.CheckRepo(context.Background(), client, "myorg-staging", "repo"); err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if got := outOfScopeCount(t, "rule", "myorg-staging"); got != 1 {
		t.Errorf("rule out_of_scope counter = %v, want 1", got)
	}

	afterIgnored := testutil.ToFloat64(metrics.IgnoredTotal.WithLabelValues("rule", "myorg-staging"))
	if afterIgnored != beforeIgnored {
		t.Errorf("ignored counter incremented (%v -> %v); scope should short-circuit ignore",
			beforeIgnored, afterIgnored)
	}
}

func TestPolicyEngine_StrictMode_InScopeButIgnored(t *testing.T) {
	resetOutOfScope(t)

	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		Scope:    &policy.ScopeConfig{Orgs: []string{"myorg-prod"}},
		FileRules: []policy.FileRuleConfig{
			{
				Type: "file", Name: "codeowners",
				Paths:    []string{"CODEOWNERS"},
				Target:   "CODEOWNERS",
				Template: "codeowners",
				Scope:    &policy.ScopeConfig{Orgs: []string{"*"}},
				Ignore:   &policy.IgnoreConfig{Repos: []string{"myorg-prod/skipped"}},
			},
		},
	}

	beforeIgnored := testutil.ToFloat64(metrics.IgnoredTotal.WithLabelValues("rule", "myorg-prod"))

	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "myorg-prod", Name: "skipped", HasBranch: true, DefaultRef: "main",
	}
	client.branchSHAs["myorg-prod/skipped/main"] = "abc123"

	if err := engine.CheckRepo(context.Background(), client, "myorg-prod", "skipped"); err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if got := outOfScopeCount(t, "rule", "myorg-prod"); got != 0 {
		t.Errorf("rule out_of_scope counter = %v, want 0", got)
	}

	afterIgnored := testutil.ToFloat64(metrics.IgnoredTotal.WithLabelValues("rule", "myorg-prod"))
	if afterIgnored != beforeIgnored+1 {
		t.Errorf("ignored counter = %v, want %v (one increment)", afterIgnored, beforeIgnored+1)
	}
}

func TestPolicyEngine_StrictMode_OutOfScopeCounterByLevel(t *testing.T) {
	resetOutOfScope(t)

	// Two repos, two outcomes: one rejected at policy level, one at
	// rule level. Verify the level label disambiguates correctly.
	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		Scope:    &policy.ScopeConfig{Orgs: []string{"myorg-*"}},
		FileRules: []policy.FileRuleConfig{
			{
				Type: "file", Name: "codeowners",
				Paths:    []string{"CODEOWNERS"},
				Target:   "CODEOWNERS",
				Template: "codeowners",
				Scope:    &policy.ScopeConfig{Orgs: []string{"myorg-prod"}},
			},
		},
	}

	engine := testPolicyEngine(cfg)

	// First repo: policy-level rejection (org not myorg-*).
	policyClient := newMockClient()
	policyClient.repo = &ghclient.Repository{
		Owner: "external", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	policyClient.branchSHAs["external/repo/main"] = "abc"

	if err := engine.CheckRepo(context.Background(), policyClient, "external", "repo"); err != nil {
		t.Fatalf("policy-level CheckRepo: %v", err)
	}

	// Second repo: passes policy gate (myorg-*), fails rule gate (not prod).
	ruleClient := newMockClient()
	ruleClient.repo = &ghclient.Repository{
		Owner: "myorg-staging", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	ruleClient.branchSHAs["myorg-staging/repo/main"] = "abc"

	if err := engine.CheckRepo(context.Background(), ruleClient, "myorg-staging", "repo"); err != nil {
		t.Fatalf("rule-level CheckRepo: %v", err)
	}

	if got := outOfScopeCount(t, "policy", "external"); got != 1 {
		t.Errorf("policy out_of_scope{external} = %v, want 1", got)
	}

	if got := outOfScopeCount(t, "rule", "myorg-staging"); got != 1 {
		t.Errorf("rule out_of_scope{myorg-staging} = %v, want 1", got)
	}

	if got := outOfScopeCount(t, "rule", "external"); got != 0 {
		t.Errorf("rule out_of_scope{external} = %v, want 0", got)
	}
}

func TestRuleScopeAllows_LegacyMode_AlwaysTrue(t *testing.T) {
	t.Parallel()

	if !ruleScopeAllows(nil, "anyorg", false) {
		t.Error("legacy mode with nil scope should allow")
	}

	rs := &policy.ScopeConfig{Orgs: []string{"someorg"}}
	if !ruleScopeAllows(rs, "anyorg", false) {
		t.Error("legacy mode with non-matching scope should still allow")
	}
}

func TestRuleScopeAllows_StrictMode_NilScope_Rejects(t *testing.T) {
	t.Parallel()

	if ruleScopeAllows(nil, "myorg", true) {
		t.Error("strict mode with nil rule scope should reject")
	}
}

func TestPolicyScopeAllows_NilConfig_Allows(t *testing.T) {
	t.Parallel()

	if !policyScopeAllows(nil, "anyorg") {
		t.Error("nil config should allow")
	}
}

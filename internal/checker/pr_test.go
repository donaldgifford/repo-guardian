package checker

import (
	"context"
	"strings"
	"testing"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/policy"
	tmpl "github.com/donaldgifford/repo-guardian/internal/template"
)

// compile parses body via a fresh Renderer for tests that need to
// hand-construct a *PRConfig with compiled templates without going
// through the policy.Load path.
func compile(t *testing.T, name, body string) *tmpl.Compiled {
	t.Helper()

	c, err := tmpl.NewRenderer().Parse(name, body)
	if err != nil {
		t.Fatalf("compiling %q: %v", name, err)
	}

	return c
}

func newPolicyMockRepo() *ghclient.Repository {
	return &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
}

func newCodeownersRule() policy.FileRuleConfig {
	enabled := true
	return policy.FileRuleConfig{
		Type:     "file",
		Name:     "codeowners",
		Enabled:  &enabled,
		Check:    "exists",
		Paths:    []string{"CODEOWNERS"},
		Target:   ".github/CODEOWNERS",
		Template: "codeowners",
	}
}

func TestEnginePR_RuleCustomTitle_RendersInPR(t *testing.T) {
	t.Parallel()

	rule := newCodeownersRule()
	titleStr := "chore({{ .Repo }}): codeowners hygiene"
	rule.PR = &policy.PRConfig{
		Title:         &titleStr,
		CompiledTitle: compile(t, "rule.pr.title", titleStr),
	}

	cfg := &policy.PolicyConfig{
		Guardian:  policy.BuiltinDefaults().Guardian,
		FileRules: []policy.FileRuleConfig{rule},
	}

	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.repo = newPolicyMockRepo()
	client.branchSHAs["org/repo/main"] = "abc123"

	if _, err := engine.CheckRepo(context.Background(), client, "org", "repo"); err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if client.createdPR == nil {
		t.Fatal("expected PR to be created")
	}

	want := "chore(repo): codeowners hygiene"
	if client.createdPR.Title != want {
		t.Errorf("PR title: got %q, want %q", client.createdPR.Title, want)
	}
}

func TestEnginePR_RuleCustomLabels_AppliedToPR(t *testing.T) {
	t.Parallel()

	rule := newCodeownersRule()
	rule.PR = &policy.PRConfig{
		Labels:    []string{"automated", "guardian"},
		LabelsSet: true,
	}

	cfg := &policy.PolicyConfig{
		Guardian:  policy.BuiltinDefaults().Guardian,
		FileRules: []policy.FileRuleConfig{rule},
	}

	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.repo = newPolicyMockRepo()
	client.branchSHAs["org/repo/main"] = "abc123"

	if _, err := engine.CheckRepo(context.Background(), client, "org", "repo"); err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if len(client.addedLabels) != 2 {
		t.Fatalf("expected 2 labels added, got %v", client.addedLabels)
	}

	if client.addedLabels[0] != "automated" || client.addedLabels[1] != "guardian" {
		t.Errorf("unexpected labels: %v", client.addedLabels)
	}
}

func TestEnginePR_BundleConflict_FallsBackToDefaults(t *testing.T) {
	t.Parallel()

	defTitle := "chore(guardian): bundle update"

	rule1 := newCodeownersRule()
	titleA := "chore: A wins"
	rule1.PR = &policy.PRConfig{Title: &titleA, CompiledTitle: compile(t, "ruleA.title", titleA)}

	rule2 := func() policy.FileRuleConfig {
		enabled := true
		titleB := "chore: B wins"

		return policy.FileRuleConfig{
			Type:     "file",
			Name:     "dependabot",
			Enabled:  &enabled,
			Check:    "exists",
			Paths:    []string{".github/dependabot.yml"},
			Target:   ".github/dependabot.yml",
			Template: "dependabot",
			PR: &policy.PRConfig{
				Title:         &titleB,
				CompiledTitle: compile(t, "ruleB.title", titleB),
			},
		}
	}()

	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		Defaults: &policy.DefaultsConfig{
			PR: &policy.PRConfig{
				Title:         &defTitle,
				CompiledTitle: compile(t, "defaults.title", defTitle),
			},
		},
		FileRules: []policy.FileRuleConfig{rule1, rule2},
	}

	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.repo = newPolicyMockRepo()
	client.branchSHAs["org/repo/main"] = "abc123"

	if _, err := engine.CheckRepo(context.Background(), client, "org", "repo"); err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if client.createdPR == nil {
		t.Fatal("expected PR to be created")
	}

	if client.createdPR.Title != defTitle {
		t.Errorf("expected fallback to defaults.pr.title %q on conflict, got %q", defTitle, client.createdPR.Title)
	}
}

func TestEnginePR_InheritsFalse_ShortCircuitsToBuiltin(t *testing.T) {
	t.Parallel()

	defTitle := "chore(guardian): default title"

	rule := newCodeownersRule()
	inheritsFalse := false
	rule.PR = &policy.PRConfig{Inherits: &inheritsFalse}

	cfg := &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		Defaults: &policy.DefaultsConfig{
			PR: &policy.PRConfig{
				Title:         &defTitle,
				CompiledTitle: compile(t, "defaults.title", defTitle),
			},
		},
		FileRules: []policy.FileRuleConfig{rule},
	}

	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.repo = newPolicyMockRepo()
	client.branchSHAs["org/repo/main"] = "abc123"

	if _, err := engine.CheckRepo(context.Background(), client, "org", "repo"); err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if client.createdPR == nil {
		t.Fatal("expected PR")
	}

	// inherits=false on rule.pr stops propagation from defaults; rule
	// has no own title, so engine built-in PRTitle is used.
	if client.createdPR.Title != PRTitle {
		t.Errorf("expected engine built-in PRTitle (%q) when inherits=false, got %q", PRTitle, client.createdPR.Title)
	}
}

func TestEnginePR_BodyTruncation_AppendsMarker(t *testing.T) {
	t.Parallel()

	bigBody := strings.Repeat("x", maxPRBodyChars+500)

	rule := newCodeownersRule()
	rule.PR = &policy.PRConfig{
		Body:         &bigBody,
		CompiledBody: compile(t, "rule.body", bigBody),
	}

	cfg := &policy.PolicyConfig{
		Guardian:  policy.BuiltinDefaults().Guardian,
		FileRules: []policy.FileRuleConfig{rule},
	}

	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.repo = newPolicyMockRepo()
	client.branchSHAs["org/repo/main"] = "abc123"

	if _, err := engine.CheckRepo(context.Background(), client, "org", "repo"); err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if !strings.Contains(client.createdPRBody, "truncated by repo-guardian") {
		t.Errorf("expected truncation marker in PR body, got len=%d", len(client.createdPRBody))
	}

	if len(client.createdPRBody) > maxPRBodyChars+200 {
		t.Errorf("body still exceeds max+marker headroom: len=%d", len(client.createdPRBody))
	}
}

// TestEnginePR_JiraStyleTitle_FromTemplatingVars locks in the
// IMPL-0012 Phase 7.4 homelab smoke acceptance criterion as a
// deterministic unit test. The chart's `templating.vars.JIRA_PROJECT`
// surfaces as an env var on the process; the `env` template helper
// reads it and the resulting PR title must be `[PLAT-CHORE] add
// CODEOWNERS`.
//
// Cannot run in parallel because t.Setenv() panics under t.Parallel
// in Go 1.25+.
func TestEnginePR_JiraStyleTitle_FromTemplatingVars(t *testing.T) {
	t.Setenv("JIRA_PROJECT", "PLAT")

	rule := newCodeownersRule()
	titleStr := `[{{ env "JIRA_PROJECT" }}-CHORE] add CODEOWNERS`
	rule.PR = &policy.PRConfig{
		Title:         &titleStr,
		CompiledTitle: compile(t, `rule "codeowners".pr.title`, titleStr),
	}

	cfg := &policy.PolicyConfig{
		Guardian:  policy.BuiltinDefaults().Guardian,
		FileRules: []policy.FileRuleConfig{rule},
	}

	engine := testPolicyEngine(cfg)
	client := newMockClient()
	client.repo = newPolicyMockRepo()
	client.branchSHAs["org/repo/main"] = "abc123"

	if _, err := engine.CheckRepo(context.Background(), client, "org", "repo"); err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if client.createdPR == nil {
		t.Fatal("expected PR to be created")
	}

	want := "[PLAT-CHORE] add CODEOWNERS"
	if client.createdPR.Title != want {
		t.Errorf("PR title: got %q, want %q", client.createdPR.Title, want)
	}
}

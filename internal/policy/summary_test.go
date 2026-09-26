package policy

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/findings"
)

func TestSummarize(t *testing.T) {
	t.Parallel()

	cfg := loadVersionFixture(t, "guardian.hcl")
	cfg.Scope = &ScopeConfig{Orgs: []string{"acme"}}
	cfg.FileRules[0].Ignore = &IgnoreConfig{Repos: []string{"acme/legacy"}}

	sum := Summarize(cfg)

	type key struct {
		kind findings.RuleKind
		name string
	}

	want := map[key]string{
		{findings.RuleKindFile, fixtureCodeowners}:  "exists",
		{findings.RuleKindFile, fixtureRenovate}:    "contains",
		{findings.RuleKindSetting, "vuln_alerts"}:   "",
		{findings.RuleKindBranchProtection, "main"}: "",
	}

	if len(sum.Rules) != len(want) {
		t.Fatalf("rules = %+v, want %d", sum.Rules, len(want))
	}

	for _, r := range sum.Rules {
		mode, ok := want[key{r.Kind, r.Name}]
		if !ok || r.CheckMode != mode {
			t.Errorf("rule %s/%s check mode %q: unexpected", r.Kind, r.Name, r.CheckMode)
		}

		if r.Description == "" || len(r.Scope) != 1 || r.Scope[0] != "acme" {
			t.Errorf("rule %s/%s = %+v, want a description and the top-level scope", r.Kind, r.Name, r)
		}
	}

	if got := sum.Rules[0].Ignore; len(got) != 1 || got[0] != "acme/legacy" {
		t.Errorf("codeowners ignore = %v", got)
	}

	b, err := json.Marshal(sum)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	for _, body := range versionV2Templates {
		if strings.Contains(string(b), strings.TrimSpace(body)) {
			t.Errorf("summary carries a template body: %s", b)
		}
	}

	if strings.Contains(string(b), "extends") {
		t.Errorf("summary carries an assertion pattern: %s", b)
	}
}

func TestSummarize_SkipsDisabledRules(t *testing.T) {
	t.Parallel()

	cfg := loadVersionFixture(t, "guardian.hcl")
	off := false
	cfg.FileRules[0].Enabled = &off

	for _, r := range Summarize(cfg).Rules {
		if r.Name == cfg.FileRules[0].Name {
			t.Errorf("disabled rule %s summarized", r.Name)
		}
	}
}

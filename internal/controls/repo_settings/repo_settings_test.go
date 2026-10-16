package repo_settings

import (
	"strings"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/control"
	"github.com/donaldgifford/repo-guardian/internal/control/controltest"
)

func definition() control.Definition {
	return control.Definition{
		ID:   control.ID{Slug: "repo_settings", Version: 1},
		Type: Name,
		Rules: []control.Rule{
			{ID: "no-wiki", Kind: KindEquals, Remediate: true, Params: map[string]any{ParamProperty: "has_wiki", ParamValue: false}},
			{
				ID:        "delete-branch",
				Kind:      KindEquals,
				Remediate: true,
				Params:    map[string]any{ParamProperty: "delete_branch_on_merge", ParamValue: true},
			},
			{
				ID:        "squash-title",
				Kind:      KindEquals,
				Remediate: true,
				Params:    map[string]any{ParamProperty: "squash_merge_commit_title", ParamValue: "PR_TITLE"},
			},
			{
				ID:        "alerts",
				Kind:      KindEquals,
				Remediate: true,
				Params:    map[string]any{ParamProperty: control.SettingVulnerabilityAlerts, ParamValue: true},
			},
		},
	}
}

func ptr[T any](v T) *T { return &v }

func settings(wiki, del bool, title string) control.RepositorySettings {
	return control.RepositorySettings{HasWiki: ptr(wiki), DeleteBranchOnMerge: ptr(del), SquashMergeCommitTitle: ptr(title)}
}

func TestConformance(t *testing.T) {
	def := definition()
	p, f, u := control.Pass, control.Fail, control.Unknown

	compliant := &controltest.FakeRepo{Settings: settings(false, true, "PR_TITLE"), VulnerabilityAlerts: true}
	drifted := &controltest.FakeRepo{Settings: settings(true, false, "COMMIT_OR_PR_TITLE")}
	denied := &controltest.FakeRepo{
		Settings: settings(false, true, "PR_TITLE"),
		Errors:   map[string]error{controltest.ErrKeyVulnerabilityAlerts: control.ErrPermission},
	}
	partial := &controltest.FakeRepo{Settings: control.RepositorySettings{HasWiki: ptr(true)}, VulnerabilityAlerts: true}

	controltest.Run(t, Type{}, []controltest.Fixture{
		{
			Name: "compliant",
			Def:  def,
			Repo: compliant,
			Want: map[control.RuleID]control.RuleStatus{"no-wiki": p, "delete-branch": p, "squash-title": p, "alerts": p},
		},
		{
			Name: "every setting drifted",
			Def:  def,
			Repo: drifted,
			Want: map[control.RuleID]control.RuleStatus{"no-wiki": f, "delete-branch": f, "squash-title": f, "alerts": f},
		},
		{
			Name: "alerts read refused is unknown for that rule alone", Def: def, Repo: denied,
			Want: map[control.RuleID]control.RuleStatus{"no-wiki": p, "delete-branch": p, "squash-title": p, "alerts": u},
		},
		{
			Name: "a field the response lacks is unknown", Def: def, Repo: partial,
			Want: map[control.RuleID]control.RuleStatus{"no-wiki": f, "delete-branch": u, "squash-title": u, "alerts": p},
		},
	})
}

func TestBuild_Errors(t *testing.T) {
	t.Parallel()

	rule := func(prop string, value any, remediate bool) control.Definition {
		d := definition()
		d.Rules = []control.Rule{{ID: "r", Kind: KindEquals, Remediate: remediate, Params: map[string]any{ParamProperty: prop, ParamValue: value}}}

		return d
	}

	dup := definition()
	dup.Rules = append(dup.Rules, control.Rule{ID: "again", Kind: KindEquals, Params: map[string]any{ParamProperty: "has_wiki", ParamValue: true}})

	tests := []struct {
		name string
		def  control.Definition
		want string
	}{
		{"unknown property", rule("has_rockets", true, false), "unknown property"},
		{"wrong value kind", rule("has_wiki", "no", false), "takes a bool value"},
		{"property twice", dup, "already pinned"},
		{"remediating default_branch", rule("default_branch", "main", true), "not remediated"},
		{"security_and_analysis is not a setting", rule("security_and_analysis", "x", false), "unknown property"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := (Type{}).Build(tt.def, control.BuildEnv{}); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Build() err = %v, want %q", err, tt.want)
			}
		})
	}
}

const goldenRevision = "a202af731e9fbd07982c67bb4b1ffda88a5863eac0c8a4c68af40cb1b1bf8610"

func TestRevisionGolden(t *testing.T) {
	t.Parallel()

	got, err := control.Revision(Type{}, definition(), control.BuildEnv{})
	if err != nil {
		t.Fatal(err)
	}

	if got != goldenRevision {
		t.Errorf("Revision() = %s, want %s", got, goldenRevision)
	}
}

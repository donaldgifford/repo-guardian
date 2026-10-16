package dependency_updates

import (
	"os"
	"slices"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/control"
	"github.com/donaldgifford/repo-guardian/internal/control/controltest"
	"github.com/donaldgifford/repo-guardian/internal/template"
)

type templates map[string]string

func (m templates) Lookup(name string) (string, bool) {
	b, ok := m[name]

	return b, ok
}

func env(t *testing.T) control.BuildEnv {
	t.Helper()

	tm := templates{}

	for name, path := range map[string]string{"renovate": "../../rules/templates/renovate.tmpl", "dependabot": "../../rules/templates/dependabot.tmpl"} {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}

		tm[name] = string(b)
	}

	return control.BuildEnv{Templates: tm, Renderer: template.NewRenderer()}
}

const (
	preset         = "github>{{ .Owner }}/renovate-config"
	githubRenovate = ".github/renovate.json"
	renovateJSON5  = "renovate.json5"
)

func renovateDef() control.Definition {
	return control.Definition{
		ID: control.ID{Slug: "dependency_updates", Version: 1}, Type: Name, Template: "renovate",
		Params: map[string]any{ParamTool: ToolRenovate, ParamRenovate: map[string]any{keyExtends: preset}},
		Rules: []control.Rule{
			{ID: "configured", Kind: KindConfigured, Remediate: true},
			{ID: "extends", Kind: KindExtends, Remediate: true},
			{ID: "exclusive", Kind: KindExclusive, Remediate: true},
		},
	}
}

func dependabotDef() control.Definition {
	return control.Definition{
		ID: control.ID{Slug: "dependency_updates", Version: 1}, Type: Name, Template: "dependabot",
		Params: map[string]any{ParamTool: ToolDependabot},
		Rules: []control.Rule{
			{ID: "configured", Kind: KindConfigured, Remediate: true},
			{ID: "exclusive", Kind: KindExclusive, Remediate: true},
		},
	}
}

func repo(files map[string]string) *controltest.FakeRepo { return &controltest.FakeRepo{Files: files} }

func want(c, e, x control.RuleStatus) map[control.RuleID]control.RuleStatus {
	out := map[control.RuleID]control.RuleStatus{"configured": c, "exclusive": x}
	if e != "" {
		out["extends"] = e
	}

	return out
}

const (
	withPreset  = "{\n  \"$schema\": \"https://docs.renovatebot.com/renovate-schema.json\",\n  \"extends\": [\n    \"github>example-org/renovate-config\"\n  ]\n}\n"
	otherPreset = "{\n  \"extends\": [\n    \"config:recommended\"\n  ],\n  \"labels\": [\"deps\"]\n}\n"
	dependabot  = "version: 2\nupdates: []\n"
)

func TestConformance(t *testing.T) {
	e := env(t)
	reno, dep := renovateDef(), dependabotDef()
	p, f, u := control.Pass, control.Fail, control.Unknown

	rendered, err := e.Renderer.Parse("renovate", mustLookup(t, e, "renovate"))
	if err != nil {
		t.Fatal(err)
	}

	renovateBody, err := rendered.Render(control.SampleVars)
	if err != nil {
		t.Fatal(err)
	}

	controltest.Run(t, Type{}, []controltest.Fixture{
		{Name: "renovate: empty", Def: reno, Env: e, Want: want(f, f, p), WantFiles: map[string]string{"renovate.json": renovateBody}},
		{
			Name: "renovate: configured with the preset",
			Def:  reno,
			Env:  e,
			Repo: repo(map[string]string{"renovate.json": withPreset}),
			Want: want(p, p, p),
		},
		{
			Name: "renovate: preset added to a multi-line list", Def: reno, Env: e,
			Repo: repo(map[string]string{githubRenovate: otherPreset}), Want: want(p, f, p),
			WantFiles: map[string]string{
				githubRenovate: "{\n  \"extends\": [\n    \"config:recommended\",\n    \"github>example-org/renovate-config\"\n  ],\n  \"labels\": [\"deps\"]\n}\n",
			},
		},
		{
			Name: "renovate: extends key added",
			Def:  reno,
			Env:  e,
			Repo: repo(map[string]string{"renovate.json": "{\n  \"labels\": [\"deps\"]\n}\n"}),
			Want: want(p, f, p),
			WantFiles: map[string]string{
				"renovate.json": "{\n  \"extends\": [\"github>example-org/renovate-config\"],\n  \"labels\": [\"deps\"]\n}\n",
			},
		},
		{
			Name: "renovate: nothing configured, dependabot present", Def: reno, Env: e,
			Repo: repo(map[string]string{".github/dependabot.yml": dependabot}), Want: want(f, f, f),
			WantFiles: map[string]string{"renovate.json": renovateBody},
		},
		{
			Name: "renovate: JSON5 missing the preset beside dependabot (partial)", Def: reno, Env: e,
			Repo: repo(map[string]string{
				".github/dependabot.yaml": dependabot,
				renovateJSON5:             "// team config\n{\n  extends: ['config:recommended',],\n}\n",
			}),
			Want: want(p, f, f),
		},
		{
			Name: "renovate: JSON5 with the preset", Def: reno, Env: e,
			Repo: repo(map[string]string{".renovaterc.json5": "{\n  /* ours */ extends: ['github>example-org/renovate-config'], // pinned\n}\n"}),
			Want: want(p, p, p),
		},
		{
			Name: "renovate: package.json config", Def: reno, Env: e,
			Repo: repo(map[string]string{"package.json": `{"name":"x","renovate":{"extends":["github>example-org/renovate-config"]}}`}),
			Want: want(p, p, p),
		},
		{
			Name: "renovate: package.json config without the preset is manual", Def: reno, Env: e,
			Repo: repo(map[string]string{"package.json": `{"name":"x","renovate":{"extends":[]}}`}), Want: want(p, f, p),
		},
		{
			Name: "renovate: unparseable config is blocked", Def: reno, Env: e,
			Repo: repo(map[string]string{"renovate.json": "{ nope"}), Want: want(p, u, p),
		},
		{Name: "dependabot: empty", Def: dep, Env: e, Want: want(f, "", p)},
		{
			Name: "dependabot: renovate configs deleted", Def: dep, Env: e,
			Repo: repo(map[string]string{".github/dependabot.yml": dependabot, "renovate.json": withPreset, ".renovaterc": "{}"}),
			Want: want(p, "", f),
		},
		{
			Name: "dependabot: package.json renovate key is manual", Def: dep, Env: e,
			Repo: repo(map[string]string{".github/dependabot.yml": dependabot, "package.json": `{"renovate":{}}`}),
			Want: want(p, "", f),
		},
	})
}

func mustLookup(t *testing.T, e control.BuildEnv, name string) string {
	t.Helper()

	body, ok := e.Templates.Lookup(name)
	if !ok {
		t.Fatalf("no %s template", name)
	}

	return body
}

func TestBuild_Errors(t *testing.T) {
	t.Parallel()

	noTool := renovateDef()
	delete(noTool.Params, ParamTool)

	extendsOnDependabot := dependabotDef()
	extendsOnDependabot.Rules = append(extendsOnDependabot.Rules, control.Rule{ID: "extends", Kind: KindExtends})

	noPreset := renovateDef()
	delete(noPreset.Params, ParamRenovate)

	dup := renovateDef()
	dup.Rules = append(dup.Rules, control.Rule{ID: "again", Kind: KindExclusive})

	for name, def := range map[string]control.Definition{
		"no tool": noTool, "extends on dependabot": extendsOnDependabot, "no preset": noPreset, "a kind twice": dup,
	} {
		if _, err := (Type{}).Build(def, env(t)); err == nil {
			t.Errorf("Build(%s) err = nil, want an error", name)
		}
	}
}

const goldenRevision = "c7ba1d15459cc58fb7bf09fe376de02f70a0e04e16acd612412e5070da53df87"

func TestRevisionGolden(t *testing.T) {
	t.Parallel()

	got, err := control.Revision(Type{}, renovateDef(), env(t))
	if err != nil {
		t.Fatal(err)
	}

	if got != goldenRevision {
		t.Errorf("Revision() = %s, want %s", got, goldenRevision)
	}
}

// TestPartialRemediation pins DESIGN-0031 D14's example: a Dependabot
// config beside a JSON5 Renovate config missing the preset deletes the
// Dependabot file (exclusive fixed) and notes the preset (extends
// manual), in one change set.
func TestPartialRemediation(t *testing.T) {
	t.Parallel()

	c, err := Type{}.Build(renovateDef(), env(t))
	if err != nil {
		t.Fatal(err)
	}

	fake := repo(map[string]string{PathDependabot: dependabot, renovateJSON5: "{ extends: [] }\n"})
	in := control.EvalInput{Repo: control.RepoContext{Vars: control.SampleVars}, Reader: control.NewEvalReader(fake, nil)}

	ev, err := c.Evaluate(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}

	cs, err := c.Remediate(t.Context(), control.RemediationInput{EvalInput: in, Evaluation: ev})
	if err != nil {
		t.Fatal(err)
	}

	if len(cs.Files) != 1 || cs.Files[0].Path != PathDependabot || !cs.Files[0].Delete {
		t.Errorf("Files = %+v, want only the dependabot delete", cs.Files)
	}

	if !slices.Equal(cs.Fixed, []control.RuleID{"exclusive"}) || !slices.Equal(cs.Manual, []control.RuleID{"extends"}) || len(cs.Notes) != 1 {
		t.Errorf("Fixed = %v, Manual = %v, Notes = %v; want exclusive fixed, extends manual with a note", cs.Fixed, cs.Manual, cs.Notes)
	}
}

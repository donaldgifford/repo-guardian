package codeowners

import (
	"os"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/control"
	"github.com/donaldgifford/repo-guardian/internal/control/controltest"
	"github.com/donaldgifford/repo-guardian/internal/template"
)

// fallbackPath is the embedded fallback template the template source
// serves when the operator supplies none (IMPL-0028 spike 7). The test
// reads the shipped bytes so the conformance suite and the golden cover
// the real fallback.
const fallbackPath = "../../rules/templates/codeowners.tmpl"

type templates map[string]string

func (m templates) Lookup(name string) (string, bool) {
	b, ok := m[name]

	return b, ok
}

func fixtureEnv(t *testing.T) control.BuildEnv {
	t.Helper()

	body, err := os.ReadFile(fallbackPath)
	if err != nil {
		t.Fatal(err)
	}

	return control.BuildEnv{Templates: templates{"codeowners.tmpl": string(body)}, Renderer: template.NewRenderer()}
}

func definition() control.Definition {
	return control.Definition{
		ID:       control.ID{Slug: "codeowners", Version: 1},
		Title:    PathRoot,
		Type:     Name,
		Template: "codeowners.tmpl",
		Rules:    []control.Rule{{ID: "exists", Number: "1", Title: "A CODEOWNERS file exists", Remediate: true}},
	}
}

func repo(files map[string]string) *controltest.FakeRepo {
	return &controltest.FakeRepo{Files: files}
}

func TestConformance(t *testing.T) {
	env := fixtureEnv(t)
	def := definition()

	pass := map[control.RuleID]control.RuleStatus{"exists": control.Pass}
	fail := map[control.RuleID]control.RuleStatus{"exists": control.Fail}

	fallback, err := os.ReadFile(fallbackPath)
	if err != nil {
		t.Fatal(err)
	}

	controltest.Run(t, Type{}, []controltest.Fixture{
		{Name: "no file", Def: def, Env: env, Want: fail, WantFiles: map[string]string{PathGitHub: string(fallback)}},
		{Name: ".github/CODEOWNERS", Def: def, Env: env, Repo: repo(map[string]string{PathGitHub: "* @acme/a\n"}), Want: pass},
		{Name: "CODEOWNERS", Def: def, Env: env, Repo: repo(map[string]string{PathRoot: "* @acme/a\n"}), Want: pass},
		{Name: "docs/CODEOWNERS", Def: def, Env: env, Repo: repo(map[string]string{PathDocs: "* @acme/a\n"}), Want: pass},
		{Name: "comment only", Def: def, Env: env, Repo: repo(map[string]string{PathRoot: "# nobody yet\n\n  # still nobody\n"}), Want: fail},
		{
			Name: "two files, the first wins", Def: def, Env: env,
			Repo: repo(map[string]string{PathGitHub: "# empty\n", PathRoot: "* @acme/a\n"}), Want: fail,
		},
		{
			Name: "two files, both valid", Def: def, Env: env,
			Repo: repo(map[string]string{PathGitHub: "* @acme/a\n", PathDocs: "* @acme/b\n"}), Want: pass,
		},
	})
}

// TestExistingFileNeverEdited pins DESIGN-0031: with a file present the
// change set writes nothing, even when the rule fails.
func TestExistingFileNeverEdited(t *testing.T) {
	t.Parallel()

	c, err := Type{}.Build(definition(), fixtureEnv(t))
	if err != nil {
		t.Fatal(err)
	}

	r := control.NewEvalReader(repo(map[string]string{PathDocs: "# todo\n"}), nil)
	in := control.EvalInput{Repo: control.RepoContext{Vars: control.SampleVars}, Reader: r}

	ev, err := c.Evaluate(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}

	cs, err := c.Remediate(t.Context(), control.RemediationInput{EvalInput: in, Evaluation: ev})
	if err != nil {
		t.Fatal(err)
	}

	if !cs.IsRecommendation() || len(cs.Manual) != 1 || len(cs.Notes) != 1 {
		t.Errorf("change set = %+v, want no writes and exists in Manual with a note", cs)
	}

	want := []string{PathGitHub, PathRoot, PathDocs}
	for i, rr := range r.Reads() {
		if rr.Resource.Key != want[i] {
			t.Errorf("read %d = %s, want %s (every location is read)", i, rr.Resource, want[i])
		}
	}
}

// goldenRevision is the revision of definition() over the shipped
// fallback template. It moves when a rule, a template byte, apply or the
// type's Semantics changes; it must not move for a title or pr {} edit.
const goldenRevision = "33f507506b7c135bb9d1f559d47a84c617b987ed66b001b1c9e9306c0ecf3220"

func TestRevisionGolden(t *testing.T) {
	t.Parallel()

	env := fixtureEnv(t)

	got, err := control.Revision(Type{}, definition(), env)
	if err != nil {
		t.Fatal(err)
	}

	if got != goldenRevision {
		t.Errorf("Revision() = %s, want %s", got, goldenRevision)
	}

	cosmetic := definition()
	cosmetic.Title = "Code owners"
	cosmetic.Rules[0].Title = "renamed"
	cosmetic.PR.Title = new("a pr title")

	if again, _ := control.Revision(Type{}, cosmetic, env); again != got {
		t.Errorf("a cosmetic edit moved the revision: %s → %s", got, again)
	}

	noFix := definition()
	noFix.Rules[0].Remediate = false

	edited := fixtureEnv(t)
	edited.Templates = templates{"codeowners.tmpl": "* @acme/owners\n"}

	for name, rev := range map[string]func() (string, error){
		"remediate":     func() (string, error) { return control.Revision(Type{}, noFix, env) },
		"template byte": func() (string, error) { return control.Revision(Type{}, definition(), edited) },
	} {
		if moved, err := rev(); err != nil || moved == got {
			t.Errorf("changing %s left the revision at %s (err %v)", name, moved, err)
		}
	}
}

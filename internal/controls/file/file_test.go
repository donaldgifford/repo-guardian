package file

import (
	"strings"
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

const (
	yamlPath = ".github/settings.yml"
	textPath = "SECURITY.md"
	yamlBody = "owner: {{ .Org }}\nlabels: [a, b]\n"
	textBody = "Report issues to {{ .Org }}.\n"
)

func env() control.BuildEnv {
	return control.BuildEnv{
		Templates: templates{"settings.yml": yamlBody, "security.md": textBody},
		Renderer:  template.NewRenderer(),
	}
}

func def(p, tmpl string, kind control.RuleKind) control.Definition {
	return control.Definition{
		ID:       control.ID{Slug: "f", Version: 1},
		Type:     Name,
		Template: tmpl,
		Params:   map[string]any{ParamPath: p},
		Rules:    []control.Rule{{ID: control.RuleID(kind), Kind: kind, Remediate: true}},
	}
}

func repo(files map[string]string) *controltest.FakeRepo { return &controltest.FakeRepo{Files: files} }

func TestConformance(t *testing.T) {
	exists := def(textPath, "security.md", KindExists)
	exactText := def(textPath, "security.md", KindExact)
	exactYAML := def(yamlPath, "settings.yml", KindExact)

	pass := func(k control.RuleKind) map[control.RuleID]control.RuleStatus {
		return map[control.RuleID]control.RuleStatus{control.RuleID(k): control.Pass}
	}
	fail := func(k control.RuleKind) map[control.RuleID]control.RuleStatus {
		return map[control.RuleID]control.RuleStatus{control.RuleID(k): control.Fail}
	}

	text := "Report issues to example-org.\n"
	yml := "owner: example-org\nlabels: [a, b]\n"

	controltest.Run(t, Type{}, []controltest.Fixture{
		{Name: "exists, absent", Def: exists, Env: env(), Want: fail(KindExists), WantFiles: map[string]string{textPath: text}},
		{Name: "exists, any content", Def: exists, Env: env(), Repo: repo(map[string]string{textPath: "x\n"}), Want: pass(KindExists)},
		{Name: "exact text, equal", Def: exactText, Env: env(), Repo: repo(map[string]string{textPath: text}), Want: pass(KindExact)},
		{
			Name: "exact text, a byte differs", Def: exactText, Env: env(),
			Repo: repo(map[string]string{textPath: strings.TrimSuffix(text, "\n")}), Want: fail(KindExact),
			WantFiles: map[string]string{textPath: text},
		},
		{
			Name: "exact yaml, reordered and restyled", Def: exactYAML, Env: env(),
			Repo: repo(map[string]string{yamlPath: "labels:\n  - a\n  - b\nowner: example-org # us\n"}), Want: pass(KindExact),
		},
		{
			Name: "exact yaml, a value differs", Def: exactYAML, Env: env(),
			Repo: repo(map[string]string{yamlPath: "owner: someone\nlabels: [a, b]\n"}), Want: fail(KindExact),
			WantFiles: map[string]string{yamlPath: yml},
		},
		{
			Name: "exact yaml, not YAML", Def: exactYAML, Env: env(),
			Repo: repo(map[string]string{yamlPath: "owner: [\n"}), Want: fail(KindExact),
			WantFiles: map[string]string{yamlPath: yml},
		},
		{Name: "exact yaml, absent", Def: exactYAML, Env: env(), Want: fail(KindExact), WantFiles: map[string]string{yamlPath: yml}},
	})
}

func TestBuild_Errors(t *testing.T) {
	t.Parallel()

	both := def(textPath, "security.md", KindExists)
	both.Rules = append(both.Rules, control.Rule{ID: "exact", Kind: KindExact})

	noPath := def(textPath, "security.md", KindExists)
	delete(noPath.Params, ParamPath)

	badMode := def(textPath, "security.md", KindExists)
	badMode.Params[ParamMode] = "exact"

	exactNoTemplate := def(textPath, "", KindExact)
	exactNoTemplate.Rules[0].Remediate = false

	tests := []struct {
		name string
		def  control.Definition
		want string
	}{
		{"both rules", both, "exactly one rule"},
		{"no path", noPath, `parameter "path"`},
		{"mode mismatch", badMode, "does not match"},
		{"exact without a template", exactNoTemplate, "needs a template"},
		{"non-canonical path", def("./a/../SECURITY.md", "security.md", KindExists), "f@1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := (Type{}).Build(tt.def, env()); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Build() err = %v, want %q", err, tt.want)
			}
		})
	}
}

// TestUnreadableFileIsBlocked: a permission failure is unknown and the
// change set writes nothing.
func TestUnreadableFileIsBlocked(t *testing.T) {
	t.Parallel()

	c, err := Type{}.Build(def(textPath, "security.md", KindExists), env())
	if err != nil {
		t.Fatal(err)
	}

	fake := &controltest.FakeRepo{Errors: map[string]error{"file:" + textPath: control.ErrPermission}}
	in := control.EvalInput{Repo: control.RepoContext{Vars: control.SampleVars}, Reader: control.NewEvalReader(fake, nil)}

	ev, err := c.Evaluate(t.Context(), in)
	if err != nil || ev.Results[0].Status != control.Unknown || ev.Results[0].Evidence["reason"] != control.ReasonPermission {
		t.Fatalf("Evaluate() = %+v, %v; want unknown{permission}", ev, err)
	}

	cs, err := c.Remediate(t.Context(), control.RemediationInput{EvalInput: in, Evaluation: ev})
	if err != nil || !cs.IsRecommendation() || len(cs.Blocked) != 1 {
		t.Errorf("Remediate() = %+v, %v; want exists blocked, no writes", cs, err)
	}
}

// goldenRevision pins the revision of the exact YAML definition.
const goldenRevision = "e01cfeb63dd86746b2429c8b5524b0567f71a30a40d13aab52d3ab192197f36f"

func TestRevisionGolden(t *testing.T) {
	t.Parallel()

	got, err := control.Revision(Type{}, def(yamlPath, "settings.yml", KindExact), env())
	if err != nil {
		t.Fatal(err)
	}

	if got != goldenRevision {
		t.Errorf("Revision() = %s, want %s", got, goldenRevision)
	}
}

package catalog_info

import (
	"os"
	"strings"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/control"
	"github.com/donaldgifford/repo-guardian/internal/control/controltest"
	"github.com/donaldgifford/repo-guardian/internal/template"
)

// fallbackPath is the embedded fallback template, read so the suite
// covers the shipped bytes.
const fallbackPath = "../../rules/templates/catalog-info.tmpl"

type templates map[string]string

func (m templates) Lookup(name string) (string, bool) {
	b, ok := m[name]

	return b, ok
}

func env(t *testing.T) control.BuildEnv {
	t.Helper()

	body, err := os.ReadFile(fallbackPath)
	if err != nil {
		t.Fatal(err)
	}

	return control.BuildEnv{Templates: templates{"catalog-info": string(body)}, Renderer: template.NewRenderer()}
}

func definition() control.Definition {
	return control.Definition{
		ID:       control.ID{Slug: "catalog_info", Version: 1},
		Type:     Name,
		Template: "catalog-info",
		Rules: []control.Rule{
			{ID: "exists", Kind: KindExists, Remediate: true},
			{ID: "kind", Kind: KindKind, Remediate: true},
			{ID: "owner", Kind: KindFieldSet, Params: map[string]any{ParamField: "spec.owner"}},
			{ID: "team-owner", Kind: KindFieldSet, Params: map[string]any{ParamField: "spec.owner", ParamContains: "team-"}},
			{ID: "jira", Kind: KindAnnotationSet, Params: map[string]any{ParamAnnotation: "jira/project-key"}},
			{ID: "real-values", Kind: KindNoPlaceholders},
		},
	}
}

const filled = `apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: payments
  annotations:
    jira/project-key: PAY
spec:
  owner: team-payments
  lifecycle: production
`

func statuses(pairs ...any) map[control.RuleID]control.RuleStatus {
	out := make(map[control.RuleID]control.RuleStatus)
	for i := 0; i < len(pairs); i += 2 {
		out[control.RuleID(pairs[i].(string))] = pairs[i+1].(control.RuleStatus)
	}

	return out
}

func repo(path, body string) *controltest.FakeRepo {
	return &controltest.FakeRepo{Files: map[string]string{path: body}}
}

func TestConformance(t *testing.T) {
	e := env(t)
	def := definition()
	p, f, u := control.Pass, control.Fail, control.Unknown

	rendered, err := e.Renderer.Parse("catalog-info", mustLookup(t, e))
	if err != nil {
		t.Fatal(err)
	}

	body, err := rendered.Render(control.SampleVars)
	if err != nil {
		t.Fatal(err)
	}

	controltest.Run(t, Type{}, []controltest.Fixture{
		{
			Name: "absent: template written, field rules manual", Def: def, Env: e,
			Want:      statuses("exists", f, "kind", f, "owner", f, "team-owner", f, "jira", f, "real-values", f),
			WantFiles: map[string]string{PathYAML: body},
		},
		{
			Name: "filled in", Def: def, Env: e, Repo: repo(PathYAML, filled),
			Want: statuses("exists", p, "kind", p, "owner", p, "team-owner", p, "jira", p, "real-values", p),
		},
		{
			Name: "the .yml location", Def: def, Env: e, Repo: repo(PathYML, filled),
			Want: statuses("exists", p, "kind", p, "owner", p, "team-owner", p, "jira", p, "real-values", p),
		},
		{
			Name: "the template left as is", Def: def, Env: e, Repo: repo(PathYAML, body),
			Want: statuses("exists", p, "kind", p, "owner", p, "team-owner", f, "jira", p, "real-values", f),
		},
		{
			Name: "an API entity is never rewritten", Def: def, Env: e,
			Repo: repo(PathYAML, strings.Replace(filled, "kind: Component", "kind: API", 1)),
			Want: statuses("exists", p, "kind", f, "owner", p, "team-owner", p, "jira", p, "real-values", p),
		},
		{
			Name: "missing owner and annotation", Def: def, Env: e,
			Repo: repo(PathYAML, "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: x\nspec:\n  owner: \"\"\n"),
			Want: statuses("exists", p, "kind", p, "owner", f, "team-owner", f, "jira", f, "real-values", p),
		},
		{
			Name: "angle-bracket placeholder", Def: def, Env: e,
			Repo: repo(PathYAML, strings.Replace(filled, "team-payments", "<team-name>", 1)),
			Want: statuses("exists", p, "kind", p, "owner", p, "team-owner", p, "jira", p, "real-values", f),
		},
		{
			Name: "unparseable: every rule unknown and blocked", Def: def, Env: e, Repo: repo(PathYAML, "kind: [\n"),
			Want: statuses("exists", u, "kind", u, "owner", u, "team-owner", u, "jira", u, "real-values", u),
		},
	})
}

func mustLookup(t *testing.T, e control.BuildEnv) string {
	t.Helper()

	body, ok := e.Templates.Lookup("catalog-info")
	if !ok {
		t.Fatal("no catalog-info template")
	}

	return body
}

func TestPlaceholders(t *testing.T) {
	t.Parallel()

	for v, want := range map[string]bool{
		"TODO":                    true,
		"todo: add the owner":     true,
		"TBD later":               true,
		"<team>":                  true,
		"team-todo":               false,
		"TODOS":                   false,
		"production":              false,
		"<>":                      false,
		"  CHANGEME  ":            true,
		"Replace_Me: owning team": true,
	} {
		if got := isPlaceholder(v, DefaultPlaceholders); got != want {
			t.Errorf("isPlaceholder(%q) = %v, want %v", v, got, want)
		}
	}
}

const goldenRevision = "cf831391cc1c57078860940de890ad209de63c3423bbd69c1410642c368e374f"

func TestRevisionGolden(t *testing.T) {
	t.Parallel()

	got, err := control.Revision(Type{}, definition(), env(t))
	if err != nil {
		t.Fatal(err)
	}

	if got != goldenRevision {
		t.Errorf("Revision() = %s, want %s", got, goldenRevision)
	}
}

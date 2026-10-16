package control

import (
	"strings"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/template"
)

// lineFileType is a Type that builds a lineType file control from the
// definition's template.
type lineFileType struct{ name string }

func (t lineFileType) Name() string { return t.name }
func (lineFileType) Semantics() int { return 1 }

func (lineFileType) RuleKinds() []RuleKindSpec {
	return []RuleKindSpec{
		{Kind: "has", Remediable: true, Params: []ParamSpec{
			{Name: "line", Type: "string", Required: true},
			{Name: "manual", Type: "bool"},
			{Name: "weight", Type: "number"},
			{Name: "tags", Type: "list(string)"},
			{Name: "labels", Type: "map(string)"},
		}},
		{Kind: "report"},
	}
}

func (lineFileType) Build(def Definition, env BuildEnv) (Control, error) { //nolint:gocritic // Type.Build signature
	cfg := FileControlConfig[[]string]{Def: def, Spec: testSpec, Type: lineType{}, MaxBytes: 1 << 10}

	if def.Template != "" {
		body, err := lookup(env, def.Template)
		if err != nil {
			return nil, err
		}

		if cfg.Template, err = env.Renderer.Parse(def.Template, body); err != nil {
			return nil, err
		}
	}

	return NewFileControl(cfg)
}

// workflowType is lineFileType with WorkflowApply.
type workflowType struct{ lineFileType }

func (workflowType) WorkflowFile(Definition, []APIChange) (FileChange, error) {
	return FileChange{}, nil
}

func newTestRegistry(t *testing.T) *Registry {
	t.Helper()

	r := NewRegistry()
	for _, typ := range []Type{lineFileType{name: "line"}, workflowType{lineFileType{name: "wf"}}} {
		if err := r.Register(typ); err != nil {
			t.Fatal(err)
		}
	}

	return r
}

func testEnv(tmpl string) BuildEnv {
	return BuildEnv{Templates: templates{"t": tmpl}, Renderer: template.NewRenderer()}
}

func TestRegistry_RegisterTwiceFails(t *testing.T) {
	t.Parallel()

	r := newTestRegistry(t)
	if err := r.Register(lineFileType{name: "line"}); err == nil {
		t.Error("Register(line) twice: err = nil, want an error")
	}

	if _, ok := r.Type("line"); !ok {
		t.Error(`Type("line") not found`)
	}
}

func TestRegistry_Build(t *testing.T) {
	t.Parallel()

	r := newTestRegistry(t)
	id := ID{Slug: "x", Version: 1}
	hasA := func() Rule {
		return Rule{ID: "a", Kind: "has", Params: map[string]any{"line": "owner example-org"}, Remediate: true}
	}

	with := func(f func(*Definition)) Definition {
		d := Definition{ID: id, Type: "line", Template: "t", Rules: []Rule{hasA()}}
		f(&d)

		return d
	}

	tests := []struct {
		name string
		def  Definition
		tmpl string
		want string // empty: Build succeeds
	}{
		{"valid", with(func(*Definition) {}), "owner {{ .Org }}", ""},
		{"all param types", with(func(d *Definition) {
			d.Rules[0].Params = map[string]any{
				"line": "owner example-org", "manual": false, "weight": 2,
				"tags": []any{"a"}, "labels": map[string]any{"k": "v"},
			}
		}), "owner {{ .Org }}", ""},
		{"kind defaults to id", with(func(d *Definition) { d.Rules = []Rule{{ID: "report"}} }), "", ""},
		{"workflow on WorkflowApply", with(func(d *Definition) { d.Type, d.Apply = "wf", ApplyWorkflow }), "owner {{ .Org }}", ""},
		{"unknown type", with(func(d *Definition) { d.Type = "nope" }), "", `x@1: unknown control type "nope"`},
		{"no rules", with(func(d *Definition) { d.Rules = nil }), "", "x@1: a definition needs at least one rule"},
		{"bad rule id", with(func(d *Definition) { d.Rules[0].ID = "A!" }), "", "x@1/A!: rule id"},
		{"duplicate rule id", with(func(d *Definition) { d.Rules = append(d.Rules, hasA()) }), "", "x@1/a: rule id declared twice"},
		{"unknown kind", with(func(d *Definition) { d.Rules[0].Kind = "nope" }), "", `x@1/a: unknown rule kind "nope"`},
		{"undeclared param", with(func(d *Definition) { d.Rules[0].Params["extra"] = "x" }), "", `x@1/a: parameter "extra" is not one of`},
		{"wrong param type", with(func(d *Definition) { d.Rules[0].Params["line"] = 3 }), "", `x@1/a: parameter "line" must be string`},
		{"wrong list type", with(func(d *Definition) { d.Rules[0].Params["tags"] = []any{1} }), "", `x@1/a: parameter "tags" must be list(string)`},
		{"missing required", with(func(d *Definition) { delete(d.Rules[0].Params, "line") }), "", `x@1/a: missing required parameter "line"`},
		{
			"remediate non-remediable",
			with(func(d *Definition) { d.Rules = []Rule{{ID: "report", Remediate: true}} }),
			"",
			`x@1/report: rule kind "report" is not remediable`,
		},
		{"workflow unsupported", with(func(d *Definition) { d.Apply = ApplyWorkflow }), "", `x@1: type line does not support apply = "workflow"`},
		{"unknown apply", with(func(d *Definition) { d.Apply = "pr" }), "", `x@1: apply = "pr"`},
		{"template fails a rule", with(func(*Definition) {}), "owner someone-else", `x@1: template "t": rule a is fail`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, err := r.Build(tt.def, testEnv(tt.tmpl))

			switch {
			case tt.want == "" && err != nil:
				t.Fatalf("Build() err = %v, want nil", err)
			case tt.want == "":
				if c.ID() != id {
					t.Errorf("Build().ID() = %v, want %v", c.ID(), id)
				}
			case err == nil || !strings.Contains(err.Error(), tt.want):
				t.Errorf("Build() err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

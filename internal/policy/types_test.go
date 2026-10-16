package policy

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/donaldgifford/repo-guardian/internal/control"
)

// testFile is a file control type for policy tests: it owns the paths in
// its "paths" parameter, offers "exists" (remediable) and "contains"
// (parameter text), and renders the definition's template when absent.
type testFile struct{}

func (testFile) Name() string   { return "testfile" }
func (testFile) Semantics() int { return 1 }

func (testFile) RuleKinds() []control.RuleKindSpec {
	return []control.RuleKindSpec{
		{Kind: "exists", Remediable: true},
		{Kind: "contains", Params: []control.ParamSpec{{Name: "text", Type: "string", Required: true}}},
	}
}

func (testFile) Build(def control.Definition, env control.BuildEnv) (control.Control, error) { //nolint:gocritic // Type.Build signature
	raw, _ := def.Params["paths"].([]any)
	if len(raw) == 0 {
		return nil, errors.New(`testfile needs "paths"`)
	}

	paths := make([]string, 0, len(raw))
	for _, p := range raw {
		paths = append(paths, fmt.Sprint(p))
	}

	cfg := control.FileControlConfig[string]{
		Def: def, Spec: control.FileSpec{Locations: paths, Canonical: paths[0]}, Type: textFile{}, MaxBytes: 1 << 10,
	}

	if def.Template != "" {
		body, ok := env.Templates.Lookup(def.Template)
		if !ok {
			return nil, fmt.Errorf("template %q not found", def.Template)
		}

		tmpl, err := env.Renderer.Parse(def.Template, body)
		if err != nil {
			return nil, err
		}

		cfg.Template = tmpl
	}

	return control.NewFileControl(cfg)
}

type textFile struct{}

func (textFile) Parse(b []byte) (string, error) { return string(b), nil }

func (textFile) Check(m string, rule control.Rule) control.RuleResult { //nolint:gocritic // FileType signature
	ok := strings.TrimSpace(m) != ""
	if rule.Kind == "contains" {
		text, _ := rule.Params["text"].(string)
		ok = strings.Contains(m, text)
	}

	res := control.RuleResult{Status: control.Fail, Evidence: map[string]any{control.EvidenceKind: "test", control.EvidenceVersion: 1}}
	if ok {
		res.Status = control.Pass
	}

	return res
}

func (textFile) Fix(m string, _ []control.Rule) (string, []control.RuleID, error) { return m, nil, nil }
func (textFile) Render(m string) ([]byte, error)                                  { return []byte(m), nil }

// testSetting is an API control type owning setting:<property>.
type testSetting struct{}

func (testSetting) Name() string   { return "testsetting" }
func (testSetting) Semantics() int { return 1 }

func (testSetting) RuleKinds() []control.RuleKindSpec {
	return []control.RuleKindSpec{{Kind: "equals", Remediable: true, Params: []control.ParamSpec{
		{Name: "property", Type: "string", Required: true}, {Name: "value", Type: "bool", Required: true},
	}}}
}

func (testSetting) Build(def control.Definition, _ control.BuildEnv) (control.Control, error) { //nolint:gocritic // Type.Build signature
	owns := make([]control.Resource, 0, len(def.Rules))
	for _, r := range def.Rules {
		owns = append(owns, control.MustCanonical(control.KindSetting, fmt.Sprint(r.Params["property"])))
	}

	return &settingControl{def: def, owns: owns}, nil
}

type settingControl struct {
	def  control.Definition
	owns []control.Resource
}

func (c *settingControl) ID() control.ID           { return c.def.ID }
func (c *settingControl) Owns() []control.Resource { return c.owns }
func (*settingControl) Reads() []control.Resource  { return nil }
func (c *settingControl) Rules() []control.Rule    { return c.def.Rules }

func (*settingControl) Evaluate(context.Context, control.EvalInput) (control.Evaluation, error) {
	return control.Evaluation{}, nil
}

func (*settingControl) Remediate(context.Context, control.RemediationInput) (control.ChangeSet, error) {
	return control.ChangeSet{}, nil
}

// testRegistry registers the test types.
func testRegistry() *control.Registry {
	reg := control.NewRegistry()
	for _, t := range []control.Type{testFile{}, testSetting{}} {
		if err := reg.Register(t); err != nil {
			panic(err)
		}
	}

	return reg
}

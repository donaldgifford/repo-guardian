package labels

import (
	"context"
	"strings"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/control"
	"github.com/donaldgifford/repo-guardian/internal/control/controltest"
)

const (
	labelBug    = "bug"
	labelStale  = "wontfix"
	colorRed    = "d73a4a"
	descBug     = "Something isn't working"
	ruleBug     = "bug"
	ruleRetired = "retired"
)

func definition(remediate bool) control.Definition {
	return control.Definition{
		ID:   control.ID{Slug: "labels", Version: 1},
		Type: Name,
		Rules: []control.Rule{
			{
				ID: ruleBug, Kind: KindPresent, Remediate: remediate,
				Params: map[string]any{ParamName: labelBug, ParamColor: "D73A4A", ParamDescription: descBug},
			},
			{ID: ruleRetired, Kind: KindAbsent, Remediate: remediate, Params: map[string]any{ParamName: labelStale}},
		},
	}
}

func TestConformance(t *testing.T) {
	p, f, u := control.Pass, control.Fail, control.Unknown
	want := func(bug, retired control.RuleStatus) map[control.RuleID]control.RuleStatus {
		return map[control.RuleID]control.RuleStatus{ruleBug: bug, ruleRetired: retired}
	}

	good := control.Label{Name: labelBug, Color: colorRed, Description: descBug}

	controltest.Run(t, Type{}, []controltest.Fixture{
		{Name: "compliant", Def: definition(true), Repo: &controltest.FakeRepo{Labels: []control.Label{good}}, Want: want(p, p)},
		{
			Name: "missing and retired present", Def: definition(true),
			Repo: &controltest.FakeRepo{Labels: []control.Label{{Name: "WontFix", Color: "ffffff"}}}, Want: want(f, f),
		},
		{
			Name: "case-only difference is an update", Def: definition(true),
			Repo: &controltest.FakeRepo{Labels: []control.Label{{Name: "Bug", Color: colorRed, Description: descBug}}}, Want: want(f, p),
		},
		{
			Name: "colour and description drifted", Def: definition(true),
			Repo: &controltest.FakeRepo{Labels: []control.Label{{Name: labelBug, Color: "000000", Description: "old"}}}, Want: want(f, p),
		},
		{
			Name: "refused read", Def: definition(true),
			Repo: &controltest.FakeRepo{Errors: map[string]error{controltest.ErrKeyLabels: control.ErrPermission}}, Want: want(u, u),
		},
		{Name: "not remediable", Def: definition(false), Repo: &controltest.FakeRepo{Labels: []control.Label{{Name: labelStale}}}, Want: want(f, f)},
	})
}

func TestRemediate_CaseOnlyIsOneUpdate(t *testing.T) {
	t.Parallel()

	c, err := (Type{}).Build(definition(true), control.BuildEnv{})
	if err != nil {
		t.Fatal(err)
	}

	repo := &controltest.FakeRepo{Labels: []control.Label{{Name: "BUG", Color: colorRed, Description: descBug}}}
	in := control.EvalInput{Reader: repo}

	ev, err := c.Evaluate(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}

	cs, err := c.Remediate(context.Background(), control.RemediationInput{EvalInput: in, Evaluation: ev})
	if err != nil {
		t.Fatal(err)
	}

	if len(cs.API) != 1 || cs.API[0].Op != control.OpSet || cs.API[0].Resource.Key != labelBug {
		t.Fatalf("Remediate() API = %+v, want one set on label:bug", cs.API)
	}

	if l, ok := cs.API[0].Value.(control.Label); !ok || l.Name != labelBug || l.Color != colorRed {
		t.Errorf("Remediate() value = %+v, want the declared name and lower-cased colour", cs.API[0].Value)
	}
}

func TestRemediate_KeepsUndeclaredDescription(t *testing.T) {
	t.Parallel()

	def := definition(true)
	def.Rules = def.Rules[:1]
	delete(def.Rules[0].Params, ParamDescription)

	c, err := (Type{}).Build(def, control.BuildEnv{})
	if err != nil {
		t.Fatal(err)
	}

	in := control.EvalInput{Reader: &controltest.FakeRepo{Labels: []control.Label{{Name: labelBug, Color: "000000", Description: "keep me"}}}}

	ev, err := c.Evaluate(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}

	cs, err := c.Remediate(context.Background(), control.RemediationInput{EvalInput: in, Evaluation: ev})
	if err != nil {
		t.Fatal(err)
	}

	if l, ok := cs.API[0].Value.(control.Label); !ok || l.Description != "keep me" {
		t.Errorf("Remediate() value = %+v, want the existing description kept", cs.API[0].Value)
	}
}

func TestBuild_Errors(t *testing.T) {
	t.Parallel()

	with := func(edit func(*control.Definition)) control.Definition {
		d := definition(true)
		edit(&d)

		return d
	}

	tests := []struct {
		name string
		def  control.Definition
		want string
	}{
		{
			"same label twice, any case",
			with(func(d *control.Definition) {
				d.Rules = append(d.Rules, control.Rule{ID: "again", Kind: KindAbsent, Params: map[string]any{ParamName: "BUG"}})
			}),
			"already managed",
		},
		{"bad colour", with(func(d *control.Definition) { d.Rules[0].Params[ParamColor] = "#d73a4a" }), "six hex digits"},
		{"empty name", with(func(d *control.Definition) { d.Rules[1].Params[ParamName] = " " }), "must not be empty"},
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

const goldenRevision = "885023bb21da5d5cb43836a32f3c52e784ad6f4877cad50c17af0db3f8f991b5"

func TestRevisionGolden(t *testing.T) {
	t.Parallel()

	got, err := control.Revision(Type{}, definition(true), control.BuildEnv{})
	if err != nil {
		t.Fatal(err)
	}

	if got != goldenRevision {
		t.Errorf("Revision() = %s, want %s", got, goldenRevision)
	}
}

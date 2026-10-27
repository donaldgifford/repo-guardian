package branch_ruleset

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/control"
	"github.com/donaldgifford/repo-guardian/internal/control/controltest"
)

const rulesetName = "protect-main"

// declared is the ruleset as HCL decodes it: numbers are ints, not the
// float64 JSON gives back.
func declared() map[string]any {
	return map[string]any{
		fieldConditions: map[string]any{"ref_name": map[string]any{"include": []any{"~DEFAULT_BRANCH"}, "exclude": []any{}}},
		fieldRules: []any{
			map[string]any{"type": "pull_request", "parameters": map[string]any{"required_approving_review_count": 1}},
			map[string]any{"type": "deletion"},
		},
	}
}

func definition(remediate bool) control.Definition {
	return control.Definition{
		ID:     control.ID{Slug: "protect_main", Version: 1},
		Type:   Name,
		Params: map[string]any{ParamName: rulesetName, ParamRuleset: declared()},
		Rules: []control.Rule{
			{ID: "exists", Kind: KindExists, Remediate: remediate},
			{ID: "matches", Kind: KindMatches, Remediate: remediate},
		},
	}
}

// observed is the API's view of a ruleset: rules in another order and
// with the defaults GitHub fills in.
func observed(id int64, enforcement string, approvals int) control.Ruleset {
	return control.Ruleset{
		ID: id, Name: rulesetName, Source: control.RulesetSourceRepository, Enforcement: enforcement, Target: TargetBranch,
		Conditions: json.RawMessage(`{"ref_name":{"include":["~DEFAULT_BRANCH"],"exclude":[]}}`),
		Rules: json.RawMessage(`[{"type":"deletion"},{"type":"pull_request","parameters":{"required_approving_review_count":` +
			itoa(approvals) + `,"dismiss_stale_reviews_on_push":false}}]`),
		BypassActors: json.RawMessage(`[]`),
	}
}

func itoa(n int) string {
	b, err := json.Marshal(n)
	if err != nil {
		panic(err)
	}

	return string(b)
}

func inherited(source control.RulesetSource) control.Ruleset {
	return control.Ruleset{ID: 9, Name: rulesetName, Source: source, Enforcement: EnforcementActive, Target: TargetBranch}
}

func TestConformance(t *testing.T) {
	p, f, u := control.Pass, control.Fail, control.Unknown
	both := func(s control.RuleStatus) map[control.RuleID]control.RuleStatus {
		return map[control.RuleID]control.RuleStatus{"exists": s, "matches": s}
	}

	other := observed(3, EnforcementActive, 1)
	other.Name = "unrelated"

	controltest.Run(t, Type{}, []controltest.Fixture{
		{Name: "absent", Def: definition(true), Repo: &controltest.FakeRepo{Rulesets: []control.Ruleset{other}}, Want: both(f)},
		{
			Name: "present and matching, defaults and order ignored", Def: definition(true),
			Repo: &controltest.FakeRepo{Rulesets: []control.Ruleset{observed(7, EnforcementActive, 1)}}, Want: both(p),
		},
		{
			Name: "drifted rules", Def: definition(true),
			Repo: &controltest.FakeRepo{Rulesets: []control.Ruleset{observed(7, EnforcementActive, 2)}},
			Want: map[control.RuleID]control.RuleStatus{"exists": p, "matches": f},
		},
		{
			Name: "disabled", Def: definition(true),
			Repo: &controltest.FakeRepo{Rulesets: []control.Ruleset{observed(7, "disabled", 1)}}, Want: both(f),
		},
		{
			Name: "inherited from the organization is never written", Def: definition(true),
			Repo: &controltest.FakeRepo{Rulesets: []control.Ruleset{inherited(control.RulesetSourceOrganization)}}, Want: both(u),
		},
		{
			Name: "inherited from the enterprise is never written", Def: definition(true),
			Repo: &controltest.FakeRepo{Rulesets: []control.Ruleset{inherited(control.RulesetSourceEnterprise), observed(7, EnforcementActive, 1)}},
			Want: both(u),
		},
		{
			Name: "refused read", Def: definition(true),
			Repo: &controltest.FakeRepo{Errors: map[string]error{controltest.ErrKeyRulesets: control.ErrPermission}}, Want: both(u),
		},
		{Name: "not remediable", Def: definition(false), Want: both(f)},
	})
}

func TestRemediate_UpsertsByID(t *testing.T) {
	t.Parallel()

	c, err := (Type{}).Build(definition(true), control.BuildEnv{})
	if err != nil {
		t.Fatal(err)
	}

	repo := &controltest.FakeRepo{Rulesets: []control.Ruleset{observed(7, EnforcementActive, 2)}}
	in := control.EvalInput{Reader: repo}

	ev, err := c.Evaluate(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}

	cs, err := c.Remediate(context.Background(), control.RemediationInput{EvalInput: in, Evaluation: ev})
	if err != nil {
		t.Fatal(err)
	}

	if len(cs.API) != 1 {
		t.Fatalf("Remediate() API = %+v, want one upsert", cs.API)
	}

	rs, ok := cs.API[0].Value.(control.Ruleset)
	if !ok || rs.ID != 7 || rs.Source != control.RulesetSourceRepository || rs.Enforcement != EnforcementActive {
		t.Errorf("Remediate() value = %+v, want the declared ruleset updating id 7", cs.API[0].Value)
	}
}

func TestRemediate_InheritedNote(t *testing.T) {
	t.Parallel()

	c, err := (Type{}).Build(definition(true), control.BuildEnv{})
	if err != nil {
		t.Fatal(err)
	}

	in := control.EvalInput{Reader: &controltest.FakeRepo{Rulesets: []control.Ruleset{inherited(control.RulesetSourceOrganization)}}}

	ev, err := c.Evaluate(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}

	cs, err := c.Remediate(context.Background(), control.RemediationInput{EvalInput: in, Evaluation: ev})
	if err != nil {
		t.Fatal(err)
	}

	if len(cs.API) != 0 || len(cs.Blocked) != 2 || !strings.Contains(cs.Notes[0], "inherited from the organization") {
		t.Errorf("Remediate() = %+v, want both rules blocked with an inherited note and no write", cs)
	}
}

func TestBuild_Errors(t *testing.T) {
	t.Parallel()

	with := func(edit func(*control.Definition)) control.Definition {
		d := definition(true)
		d.Params = map[string]any{ParamName: rulesetName, ParamRuleset: declared()}
		edit(&d)

		return d
	}

	tests := []struct {
		name string
		def  control.Definition
		want string
	}{
		{"no name", with(func(d *control.Definition) { delete(d.Params, ParamName) }), "is required"},
		{"no ruleset", with(func(d *control.Definition) { delete(d.Params, ParamRuleset) }), "must be an object"},
		{"unknown field", with(func(d *control.Definition) { d.Params[ParamRuleset].(map[string]any)["owner"] = "x" }), "unknown field"},
		{"no rules", with(func(d *control.Definition) { delete(d.Params[ParamRuleset].(map[string]any), fieldRules) }), "is required"},
		{"rules not a list", with(func(d *control.Definition) { d.Params[ParamRuleset].(map[string]any)[fieldRules] = "x" }), "must be a list"},
		{
			"exists remediating an inactive ruleset",
			with(func(d *control.Definition) { d.Params[ParamRuleset].(map[string]any)[fieldEnforcement] = "evaluate" }),
			"needs enforcement",
		},
		{
			"kind twice",
			with(func(d *control.Definition) { d.Rules = append(d.Rules, control.Rule{ID: "again", Kind: KindExists}) }),
			"declared twice",
		},
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

const goldenRevision = "4daeb7c184fadfe2e02aab58539041e109051132e2c377aa17e3c1c8c3ea359a"

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

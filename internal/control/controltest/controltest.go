package controltest

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/control"
	"github.com/donaldgifford/repo-guardian/internal/template"
)

// OneRule is implemented by a control type whose definitions declare
// exactly one rule, such as the generic file type (exists or exact,
// never both). Property 6 cannot apply to it.
type OneRule interface {
	OneRulePerDefinition() bool
}

// Fixture is one repository state a control type is run against.
type Fixture struct {
	Name string
	// Def is the definition under test; Def.Type defaults to the type's
	// name.
	Def control.Definition
	// Env is passed to Build.
	Env control.BuildEnv
	// Repo is the state; nil is an empty repository.
	Repo *FakeRepo
	// Vars are the template variables; zero means control.SampleVars.
	Vars *template.Vars

	// Want, when set, is the expected status of each rule.
	Want map[control.RuleID]control.RuleStatus
	// WantFiles, when set, is the exact content of every file the
	// remediation writes, by path: the byte-exact minimal-edit check
	// (property 4). A path absent from it is not checked.
	WantFiles map[string]string
}

// Run builds every fixture's definition with typ and runs DESIGN-0031
// properties 2–6 against it:
//
//  2. remediation: after applying remediate(state), every Fixed rule
//     passes and no passing rule fails; every failing rule is accounted
//     for (ValidateChangeSet); a change set with no writes is a
//     recommendation with nothing in Fixed;
//  3. idempotence: remediating the remediated state writes nothing and
//     fixes nothing;
//  4. minimal edits: WantFiles byte-exact, and no write that leaves a
//     file unchanged;
//  5. the template passes its rules: an empty repository remediated with
//     the fixture's variables passes every remediable rule;
//  6. two rules failing together: a type with two or more remediable
//     rule kinds needs a fixture where two remediable rules fail at once,
//     which property 2 then checks pass together. A type whose
//     definitions hold exactly one rule says so with OneRule, and every
//     fixture must then declare one rule instead.
func Run(t *testing.T, typ control.Type, fixtures []Fixture) {
	t.Helper()

	reg := control.NewRegistry()
	if err := reg.Register(typ); err != nil {
		t.Fatal(err)
	}

	twoFailing := false

	for i := range fixtures {
		fx := &fixtures[i]

		t.Run(fx.Name, func(t *testing.T) {
			if runFixture(t, reg, typ, fx) >= 2 {
				twoFailing = true
			}
		})
	}

	if one, ok := typ.(OneRule); ok && one.OneRulePerDefinition() {
		for i := range fixtures {
			if n := len(fixtures[i].Def.Rules); n != 1 {
				t.Errorf("%s: fixture %q declares %d rules, but the type holds one rule per definition", typ.Name(), fixtures[i].Name, n)
			}
		}

		return
	}

	if remediableKinds(typ) >= 2 && !twoFailing {
		t.Errorf("%s has two or more remediable rule kinds but no fixture fails two remediable rules at once (property 6)", typ.Name())
	}
}

// runFixture runs properties 2–5 for one fixture and returns how many
// remediable rules failed in its state.
func runFixture(t *testing.T, reg *control.Registry, typ control.Type, fx *Fixture) int {
	t.Helper()

	def := fx.Def
	if def.Type == "" {
		def.Type = typ.Name()
	}

	c, err := reg.Build(def, fx.Env)
	if err != nil {
		t.Fatalf("Build() err = %v", err)
	}

	vars := control.SampleVars
	if fx.Vars != nil {
		vars = *fx.Vars
	}

	repo := fx.Repo
	if repo == nil {
		repo = &FakeRepo{}
	}

	ev, cs := evaluateAndRemediate(t, c, repo, &vars)

	for id, want := range fx.Want {
		if got := status(ev, id); got != want {
			t.Errorf("rule %s = %s, want %s", id, got, want)
		}
	}

	after := checkRemediation(t, c, repo, &vars, ev, cs)
	checkMinimalEdits(t, repo, cs, fx.WantFiles)

	if after != nil {
		_, again := evaluateAndRemediate(t, c, after, &vars)
		if !again.IsRecommendation() || len(again.Fixed) > 0 {
			t.Errorf("idempotence: remediating the remediated state = %+v, want no writes and nothing fixed", again)
		}
	}

	t.Run("template", func(t *testing.T) {
		checkTemplate(t, c, &vars)
	})

	return countRemediableFailing(c, ev)
}

// evaluateAndRemediate runs Evaluate and Remediate over one EvalReader
// and validates both.
func evaluateAndRemediate(t *testing.T, c control.Control, repo *FakeRepo, vars *template.Vars) (control.Evaluation, control.ChangeSet) {
	t.Helper()

	ctx := context.Background()
	r := control.NewEvalReader(repo, time.Now)
	in := control.EvalInput{
		Repo:   control.RepoContext{Org: vars.Org, Name: vars.Repo, DefaultBranch: vars.DefaultBranch, Vars: *vars},
		Reader: r,
	}

	ev, err := c.Evaluate(ctx, in)
	if err != nil {
		t.Fatalf("Evaluate() err = %v", err)
	}

	ev.Reads = r.Reads()

	if ev, err = control.Validate(c, ev); err != nil {
		t.Fatalf("Validate() err = %v", err)
	}

	cs, err := c.Remediate(ctx, control.RemediationInput{EvalInput: in, Evaluation: ev})
	if err != nil {
		t.Fatalf("Remediate() err = %v", err)
	}

	if err := control.ValidateChangeSet(c, ev, cs); err != nil {
		t.Fatalf("ValidateChangeSet() err = %v", err)
	}

	return ev, cs
}

// checkRemediation is property 2. It returns the remediated state, or
// nil when the change set could not be applied.
//
//nolint:gocritic // hugeParam: test harness
func checkRemediation(
	t *testing.T, c control.Control, repo *FakeRepo, vars *template.Vars, before control.Evaluation, cs control.ChangeSet,
) *FakeRepo {
	t.Helper()

	if cs.IsRecommendation() && len(cs.Fixed) > 0 {
		t.Errorf("a change set with no writes names Fixed rules %v", cs.Fixed)
	}

	after, err := Apply(repo, cs)
	if err != nil {
		t.Errorf("Apply() err = %v", err)

		return nil
	}

	ev, _ := evaluateAndRemediate(t, c, after, vars)

	for _, id := range cs.Fixed {
		if got := status(ev, id); got != control.Pass {
			t.Errorf("rule %s is in Fixed but is %s after the change", id, got)
		}
	}

	for _, res := range before.Results {
		if res.Status == control.Pass && status(ev, res.RuleID) == control.Fail {
			t.Errorf("rule %s passed before the change and fails after it", res.RuleID)
		}
	}

	return after
}

// checkMinimalEdits is property 4.
func checkMinimalEdits(t *testing.T, repo *FakeRepo, cs control.ChangeSet, want map[string]string) { //nolint:gocritic // hugeParam: test harness
	t.Helper()

	for _, fc := range cs.Files {
		if fc.Delete {
			continue
		}

		if old, ok := repo.Files[fc.Path]; ok && bytes.Equal([]byte(old), fc.Content) {
			t.Errorf("%s: a write that leaves the file unchanged", fc.Path)
		}

		if w, ok := want[fc.Path]; ok && w != string(fc.Content) {
			t.Errorf("%s =\n%s\nwant\n%s", fc.Path, fc.Content, w)
		}
	}
}

// checkTemplate is property 5.
func checkTemplate(t *testing.T, c control.Control, vars *template.Vars) {
	t.Helper()

	empty := &FakeRepo{}

	_, cs := evaluateAndRemediate(t, c, empty, vars)

	after, err := Apply(empty, cs)
	if err != nil {
		t.Fatalf("Apply() err = %v", err)
	}

	ev, _ := evaluateAndRemediate(t, c, after, vars)

	for _, rule := range c.Rules() {
		if got := status(ev, rule.ID); rule.Remediate && got != control.Pass {
			t.Errorf("remediable rule %s is %s on the remediated empty repository", rule.ID, got)
		}
	}
}

func status(ev control.Evaluation, id control.RuleID) control.RuleStatus {
	for _, res := range ev.Results {
		if res.RuleID == id {
			return res.Status
		}
	}

	return ""
}

func countRemediableFailing(c control.Control, ev control.Evaluation) int {
	n := 0

	for _, rule := range c.Rules() {
		if rule.Remediate && status(ev, rule.ID) == control.Fail {
			n++
		}
	}

	return n
}

func remediableKinds(typ control.Type) int {
	n := 0

	for _, k := range typ.RuleKinds() {
		if k.Remediable {
			n++
		}
	}

	return n
}

package custom_properties

import (
	"context"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"gopkg.in/yaml.v3"

	"github.com/donaldgifford/repo-guardian/internal/control"
	"github.com/donaldgifford/repo-guardian/internal/control/controltest"
	"github.com/donaldgifford/repo-guardian/internal/metrics"
)

const (
	keyJira   = "JiraProject"
	keyTier   = "Tier"
	ruleMatch = "matches"
	rulePat   = "value_pattern"
	ruleDef   = "defined"
	org       = "example-org"
)

func definition() control.Definition {
	return control.Definition{
		ID:   control.ID{Slug: "custom_properties", Version: 1},
		Type: Name,
		Params: map[string]any{
			ParamAnnotationProperties: map[string]any{"jira/project-key": keyJira},
			ParamProperties: map[string]any{
				KeyOwner: map[string]any{factPattern: "^[a-z0-9-]+$"},
				keyTier:  map[string]any{factSource: "spec.lifecycle", factNormalize: NormalizeLower},
			},
		},
		Rules: []control.Rule{
			{ID: ruleMatch, Kind: KindMatches, Remediate: true},
			{ID: rulePat, Kind: KindValuePattern},
			{ID: ruleDef, Kind: KindDefined},
		},
	}
}

func catalogInfo(owner, jira string) string {
	ann := ""
	if jira != "" {
		ann = "\n  annotations:\n    jira/project-key: " + jira
	}

	return "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: billing" + ann +
		"\nspec:\n  type: service\n  lifecycle: Production\n  owner: " + owner + "\n"
}

func ptr(s string) *string { return &s }

func ptrDef(d control.Definition) *control.Definition { return &d } //nolint:gocritic // hugeParam: test helper

func registry(t *testing.T) *control.Registry {
	t.Helper()

	reg := control.NewRegistry()
	if err := reg.Register(Type{}); err != nil {
		t.Fatal(err)
	}

	return reg
}

var schema = []string{KeyOwner, KeyComponent, keyJira, keyTier}

func synced() map[string]*string {
	return map[string]*string{KeyOwner: ptr("platform"), KeyComponent: ptr("billing"), keyTier: ptr("production"), keyJira: ptr("BILL")}
}

func TestConformance(t *testing.T) {
	p, f, u, na := control.Pass, control.Fail, control.Unknown, control.NotApplicable
	want := func(m, pat, def control.RuleStatus) map[control.RuleID]control.RuleStatus {
		return map[control.RuleID]control.RuleStatus{ruleMatch: m, rulePat: pat, ruleDef: def}
	}

	controltest.Run(t, Type{}, []controltest.Fixture{
		{
			Name: "synced, qualified owner normalised", Def: definition(),
			Repo: &controltest.FakeRepo{
				Files: map[string]string{PathYAML: catalogInfo("group:default/platform", "BILL")}, Properties: synced(), Schema: schema,
			},
			Want: want(p, p, p),
		},
		{
			Name: "source state: mapped annotation removed clears that property", Def: definition(),
			Repo: &controltest.FakeRepo{Files: map[string]string{PathYAML: catalogInfo("platform", "")}, Properties: synced(), Schema: schema},
			Want: want(f, p, p),
		},
		{
			Name: "source state: catalog file absent clears the managed set", Def: definition(),
			Repo: &controltest.FakeRepo{Properties: synced(), Schema: schema},
			Want: want(f, p, p),
		},
		{
			Name: "source state: unparseable file retains every value", Def: definition(),
			Repo: &controltest.FakeRepo{Files: map[string]string{PathYAML: "kind: [unclosed\n"}, Properties: synced(), Schema: schema},
			Want: want(u, u, u),
		},
		{
			Name: "source state: non-Component entity retains every value", Def: definition(),
			Repo: &controltest.FakeRepo{
				Files:      map[string]string{PathYML: "apiVersion: backstage.io/v1alpha1\nkind: System\nmetadata:\n  name: payments\n"},
				Properties: synced(), Schema: schema,
			},
			Want: want(na, na, na),
		},
		{
			Name: "a value failing its pattern withholds that write alone", Def: definition(),
			Repo: &controltest.FakeRepo{Files: map[string]string{PathYAML: catalogInfo("Platform Team", "BILL")}, Schema: schema},
			Want: want(f, f, p),
		},
		{
			Name: "a key the schema does not define is never written",
			Def:  definition(),
			Repo: &controltest.FakeRepo{
				Files:  map[string]string{PathYAML: catalogInfo("platform", "BILL")},
				Schema: []string{KeyOwner, KeyComponent},
			},
			Want: want(f, p, f),
		},
		{
			Name: "properties read refused", Def: definition(),
			Repo: &controltest.FakeRepo{
				Files:  map[string]string{PathYAML: catalogInfo("platform", "BILL")},
				Schema: schema, Errors: map[string]error{controltest.ErrKeyProperties: control.ErrPermission},
			},
			Want: want(u, p, p),
		},
	})
}

// changes runs one evaluation and remediation and returns the writes by
// key, with "<nil>" for a clear.
func changes(
	t *testing.T,
	def *control.Definition,
	repo *controltest.FakeRepo,
	account string,
) (map[string]string, control.ChangeSet, control.Evaluation) {
	t.Helper()

	c, err := registry(t).Build(*def, control.BuildEnv{})
	if err != nil {
		t.Fatal(err)
	}

	in := control.EvalInput{Repo: control.RepoContext{Org: org, AccountType: account}, Reader: repo}

	ev, err := c.Evaluate(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}

	cs, err := c.Remediate(context.Background(), control.RemediationInput{EvalInput: in, Evaluation: ev})
	if err != nil {
		t.Fatal(err)
	}

	out := make(map[string]string)

	for _, ch := range cs.API {
		v, _ := ch.Value.(*string) // nil is a clear
		out[ch.Resource.Key] = "<nil>"

		if v != nil {
			out[ch.Resource.Key] = *v
		}
	}

	return out, cs, ev
}

func TestSourceStates_ExactWrites(t *testing.T) {
	tests := []struct {
		name    string
		repo    *controltest.FakeRepo
		want    map[string]string
		blocked bool
	}{
		{
			name: "annotation removed",
			repo: &controltest.FakeRepo{Files: map[string]string{PathYAML: catalogInfo("platform", "")}, Properties: synced(), Schema: schema},
			want: map[string]string{keyJira: "<nil>"},
		},
		{
			name: "file absent",
			repo: &controltest.FakeRepo{Properties: synced(), Schema: schema},
			want: map[string]string{KeyOwner: "<nil>", KeyComponent: "<nil>", keyJira: "<nil>", keyTier: "<nil>"},
		},
		{
			name:    "malformed file",
			repo:    &controltest.FakeRepo{Files: map[string]string{PathYAML: "kind: [unclosed\n"}, Properties: synced(), Schema: schema},
			want:    map[string]string{},
			blocked: true,
		},
		{
			name: "non-Component entity",
			repo: &controltest.FakeRepo{
				Files: map[string]string{
					PathYAML: "apiVersion: backstage.io/v1alpha1\nkind: Resource\nmetadata:\n  name: db\n",
				},
				Properties: synced(),
				Schema:     schema,
			},
			want: map[string]string{},
		},
		{
			name:    "pattern withholds Owner, siblings sync",
			repo:    &controltest.FakeRepo{Files: map[string]string{PathYAML: catalogInfo("Platform Team", "BILL")}, Schema: schema},
			want:    map[string]string{KeyComponent: "billing", keyJira: "BILL", keyTier: "production"},
			blocked: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, cs, _ := changes(t, ptrDef(definition()), tt.repo, "")

			if len(got) != len(tt.want) {
				t.Fatalf("writes = %v, want %v", got, tt.want)
			}

			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("write %s = %q, want %q", k, got[k], v)
				}
			}

			if isBlocked := len(cs.Blocked) > 0; isBlocked != tt.blocked {
				t.Errorf("Blocked = %v (notes %v), want blocked %v", cs.Blocked, cs.Notes, tt.blocked)
			}
		})
	}
}

func TestEvidence_NamesSourcesAndGates(t *testing.T) {
	repo := &controltest.FakeRepo{
		Files:  map[string]string{PathYAML: catalogInfo("Platform Team", "BILL")},
		Schema: []string{KeyOwner, KeyComponent, keyTier},
	}
	_, _, ev := changes(t, ptrDef(definition()), repo, "")

	m := ev.Results[0].Evidence
	if strings.Join(m["withheld"].([]string), ",") != "JiraProject,Owner" || //nolint:forcetypeassert // the type's own evidence
		strings.Join(m["withheld_gates"].([]string), ",") != "schema,value" { //nolint:forcetypeassert // as above
		t.Errorf("evidence = %v, want JiraProject withheld by the schema and Owner by its value", m)
	}

	if !strings.Contains(strings.Join(m["sources"].([]string), ","), "annotation:jira/project-key") { //nolint:forcetypeassert // as above
		t.Errorf("sources = %v, want the annotation source recorded", m["sources"])
	}
}

func TestUserAccount_NotApplicable(t *testing.T) {
	_, cs, ev := changes(t, ptrDef(definition()), &controltest.FakeRepo{}, control.AccountUser)

	for _, r := range ev.Results {
		if r.Status != control.NotApplicable || r.Evidence["account_type"] != control.AccountUser {
			t.Errorf("%s = %s %v, want not_applicable with account_type=user", r.RuleID, r.Status, r.Evidence)
		}
	}

	if len(cs.API) != 0 {
		t.Errorf("writes = %v, want none", cs.API)
	}
}

func TestScope_OutOfScopePropertyIsNotManaged(t *testing.T) {
	def := definition()
	def.Params[ParamProperties].(map[string]any)[keyTier] = map[string]any{
		factSource: "spec.lifecycle",
		factScope:  []any{"other-org"},
	} //nolint:forcetypeassert // fixture

	got, _, _ := changes(t, &def, &controltest.FakeRepo{Files: map[string]string{PathYAML: catalogInfo("platform", "BILL")}, Schema: schema}, "")
	if _, ok := got[keyTier]; ok {
		t.Errorf("writes = %v, want Tier left alone outside its scope", got)
	}
}

func TestParseFailure_CountedOncePerEvaluation(t *testing.T) {
	counter := metrics.CatalogParseFailedTotal.WithLabelValues(org)
	before := testutil.ToFloat64(counter)

	changes(t, ptrDef(definition()), &controltest.FakeRepo{Files: map[string]string{PathYAML: "kind: [unclosed\n"}}, "")

	if got := testutil.ToFloat64(counter) - before; got != 1 {
		t.Errorf("CatalogParseFailedTotal rose by %v, want 1", got)
	}
}

func TestNormalize_EntityRef(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"platform", "group:platform", "group:default/platform", "default/platform"} {
		if got := normalize(NormalizeEntityRef, in); got != "platform" {
			t.Errorf("normalize(entity_ref, %q) = %q, want platform", in, got)
		}
	}
}

func TestBuild_Errors(t *testing.T) {
	t.Parallel()

	with := func(params map[string]any) control.Definition {
		d := definition()
		d.Params = params

		return d
	}

	ruleOn := definition()
	ruleOn.Rules = append(ruleOn.Rules, control.Rule{ID: "one", Kind: KindMatches, Params: map[string]any{ParamProperty: "Nope"}})

	tests := []struct {
		name string
		def  control.Definition
		want string
	}{
		{"annotation onto a built-in", with(map[string]any{ParamAnnotationProperties: map[string]any{"x/owner": "owner"}}), "built-in key"},
		{
			"duplicate rendered key",
			with(
				map[string]any{
					ParamProperties: map[string]any{
						"A": map[string]any{factSource: "spec.a", factKey: "Same"},
						"B": map[string]any{factSource: "spec.b", factKey: "same"},
					},
				},
			),
			"both render key",
		},
		{
			"bad key",
			with(map[string]any{ParamProperties: map[string]any{"A": map[string]any{factSource: "spec.a", factKey: "has space"}}}),
			"must match",
		},
		{"bad pattern", with(map[string]any{ParamProperties: map[string]any{KeyOwner: map[string]any{factPattern: "("}}}), "pattern"},
		{"bad normalize", with(map[string]any{ParamProperties: map[string]any{KeyOwner: map[string]any{factNormalize: "upper"}}}), "normalize"},
		{"unknown fact", with(map[string]any{ParamProperties: map[string]any{KeyOwner: map[string]any{"colour": "x"}}}), "unknown fact"},
		{"rule on an unmanaged property", ruleOn, "not a managed property"},
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

func TestBuild_SourcelessPropertyIsNotManaged(t *testing.T) {
	t.Parallel()

	def := definition()
	def.Params[ParamProperties].(map[string]any)["Schema"] = map[string]any{factPattern: "x"} //nolint:forcetypeassert // fixture

	c, err := (Type{}).Build(def, control.BuildEnv{})
	if err != nil {
		t.Fatal(err)
	}

	for _, r := range c.Owns() {
		if r.Key == "Schema" {
			t.Errorf("Owns() = %v, want no sourceless property", c.Owns())
		}
	}
}

func TestWorkflowApply_OwnsTheFile(t *testing.T) {
	t.Parallel()

	def := definition()
	def.Apply = control.ApplyWorkflow

	c, err := registry(t).Build(def, control.BuildEnv{})
	if err != nil {
		t.Fatal(err)
	}

	want := control.MustCanonical(control.KindFile, ".github/workflows/repo-guardian-custom_properties.yml")
	found := false

	for _, r := range c.Owns() {
		found = found || r == want
	}

	if !found {
		t.Errorf("Owns() = %v, want %v", c.Owns(), want)
	}
}

// The IMPL-0020 A2 regression, carried over from internal/rules: a
// hostile value reaches the script only through the env block.
func TestWorkflowFile_InjectionValueIsInert(t *testing.T) {
	t.Parallel()

	hostile := "x'$(id)'"
	out := render(t, map[string]*string{keyJira: ptr(hostile), KeyOwner: ptr("platform"), keyTier: nil})

	if strings.Contains(out, "-f 'properties[][value]="+hostile+"'") || strings.Contains(out, "value]="+hostile) {
		t.Errorf("hostile value interpolated into a run-script argument:\n%s", out)
	}

	if !strings.Contains(out, `RG_PROP_JiraProject: "x'$(id)'"`) {
		t.Errorf("want the hostile value as a yamlq-quoted env scalar:\n%s", out)
	}

	if !strings.Contains(out, `properties[][value]=$RG_PROP_JiraProject`) {
		t.Errorf("want the script to reference $RG_PROP_JiraProject:\n%s", out)
	}

	if !strings.Contains(out, `-f 'properties[][property_name]=Tier' -F 'properties[][value]=null'`) {
		t.Errorf("want Tier cleared with a JSON null:\n%s", out)
	}

	assertEnv(t, out, keyJira, hostile)
}

func TestWorkflowFile_HostileValuesYAMLSafe(t *testing.T) {
	t.Parallel()

	for _, v := range []string{`he said "hi"`, "it's mine", "$HOME/path", "a: b", "line1\nline2", "{k: [1,2]}", "- item"} {
		assertEnv(t, render(t, map[string]*string{keyJira: ptr(v)}), keyJira, v)
	}

	if _, err := (Type{}).WorkflowFile(definition(), []control.APIChange{{
		Resource: control.Resource{Kind: control.KindProperty, Key: keyJira}, Op: control.OpSet, Value: ptr("${{ secrets.X }}"),
	}}); err == nil {
		t.Error("WorkflowFile() rendered an Actions expression, want an error")
	}
}

func render(t *testing.T, props map[string]*string) string {
	t.Helper()

	chs := make([]control.APIChange, 0, len(props))
	for k, v := range props {
		chs = append(chs, control.APIChange{Resource: control.Resource{Kind: control.KindProperty, Key: k}, Op: control.OpSet, Value: v})
	}

	fc, err := (Type{}).WorkflowFile(definition(), chs)
	if err != nil {
		t.Fatal(err)
	}

	if fc.Path != ".github/workflows/repo-guardian-custom_properties.yml" {
		t.Errorf("Path = %s", fc.Path)
	}

	return string(fc.Content)
}

func assertEnv(t *testing.T, rendered, property, want string) {
	t.Helper()

	var wf struct {
		Jobs struct {
			SetProperties struct {
				Steps []struct {
					Env map[string]string `yaml:"env"`
				} `yaml:"steps"`
			} `yaml:"set-properties"`
		} `yaml:"jobs"`
	}

	if err := yaml.Unmarshal([]byte(rendered), &wf); err != nil {
		t.Fatalf("workflow is not valid YAML: %v\n%s", err, rendered)
	}

	if got := wf.Jobs.SetProperties.Steps[0].Env["RG_PROP_"+property]; got != want {
		t.Errorf("env RG_PROP_%s = %q, want %q", property, got, want)
	}
}

const goldenRevision = "ed80fe4e675ee4704ad96a2b025bea8298e999cee32ab7d88cebdee6a9ddce59"

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

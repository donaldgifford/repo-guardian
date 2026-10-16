package control

import (
	"context"
	"fmt"
	"path"
	"reflect"
	"slices"
	"sync"

	"github.com/donaldgifford/repo-guardian/internal/template"
)

// The apply values (D15). The empty string is ApplyRecommend.
const (
	ApplyRecommend = "recommend"
	ApplyDirect    = "direct"
	ApplyWorkflow  = "workflow"
)

// SampleVars are the variables Build renders a definition's template
// with to prove it passes the control's remediable rules.
var SampleVars = template.Vars{
	Common: template.Common{Owner: "example-org", Repo: "example", DefaultBranch: "main"},
	Org:    "example-org",
}

// Registry holds the control types a deployment knows. It is safe for
// concurrent use; types are registered at startup.
type Registry struct {
	mu    sync.RWMutex
	types map[string]Type
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{types: make(map[string]Type)}
}

// Register adds t; a second type with the same name is an error.
func (r *Registry) Register(t Type) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, dup := r.types[t.Name()]; dup {
		return fmt.Errorf("control type %q registered twice", t.Name())
	}

	r.types[t.Name()] = t

	return nil
}

// Type returns the type named name.
func (r *Registry) Type(name string) (Type, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	t, ok := r.types[name]

	return t, ok
}

// Build validates def against its type's rule kinds and builds the
// Control. It fails on an unknown type, a definition with no rules, a
// bad rule id, an unknown rule kind, a parameter the kind does not
// declare or of the wrong type, a missing required parameter,
// remediate = true on a non-remediable kind, an apply value the type
// does not support, and a template whose render with SampleVars fails
// one of the control's remediable rules. Every error names the
// definition's slug@N, and the rule id where there is one.
func (r *Registry) Build(def Definition, env BuildEnv) (Control, error) { //nolint:gocritic // hugeParam: the signature is DESIGN-0031's
	t, ok := r.Type(def.Type)
	if !ok {
		return nil, fmt.Errorf("%s: unknown control type %q", def.ID, def.Type)
	}

	if len(def.Rules) == 0 {
		return nil, fmt.Errorf("%s: a definition needs at least one rule", def.ID)
	}

	def.Rules = slices.Clone(def.Rules)

	if err := checkRules(t, def.ID, def.Rules); err != nil {
		return nil, err
	}

	if err := checkApply(t, def); err != nil {
		return nil, err
	}

	c, err := t.Build(def, env)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", def.ID, err)
	}

	if def.Template != "" {
		if err := checkTemplate(c); err != nil {
			return nil, fmt.Errorf("%s: template %q: %w", def.ID, def.Template, err)
		}
	}

	return c, nil
}

// checkRules validates every rule against t's kinds, defaulting an
// empty Kind to the rule id.
func checkRules(t Type, id ID, rules []Rule) error {
	kinds := make(map[RuleKind]RuleKindSpec)
	for _, k := range t.RuleKinds() {
		kinds[k.Kind] = k
	}

	seen := make(map[RuleID]bool)

	for i := range rules {
		rule := &rules[i]

		if !ValidSlug.MatchString(string(rule.ID)) {
			return fmt.Errorf("%s/%s: rule id must match %s", id, rule.ID, ValidSlug)
		}

		if seen[rule.ID] {
			return fmt.Errorf("%s/%s: rule id declared twice", id, rule.ID)
		}

		seen[rule.ID] = true

		if rule.Kind == "" {
			rule.Kind = RuleKind(rule.ID)
		}

		spec, ok := kinds[rule.Kind]
		if !ok {
			return fmt.Errorf("%s/%s: unknown rule kind %q for type %s", id, rule.ID, rule.Kind, t.Name())
		}

		if rule.Remediate && !spec.Remediable {
			return fmt.Errorf("%s/%s: rule kind %q is not remediable", id, rule.ID, rule.Kind)
		}

		if err := checkParams(spec, rule.Params); err != nil {
			return fmt.Errorf("%s/%s: %w", id, rule.ID, err)
		}
	}

	return nil
}

// checkParams validates params against spec: every one declared and of
// its type, every required one present.
func checkParams(spec RuleKindSpec, params map[string]any) error {
	declared := make(map[string]ParamSpec, len(spec.Params))

	for _, p := range spec.Params {
		declared[p.Name] = p

		if _, ok := params[p.Name]; p.Required && !ok {
			return fmt.Errorf("missing required parameter %q", p.Name)
		}
	}

	for name, v := range params {
		p, ok := declared[name]
		if !ok {
			return fmt.Errorf("parameter %q is not one of rule kind %q's", name, spec.Kind)
		}

		if !paramHasType(v, p.Type) {
			return fmt.Errorf("parameter %q must be %s", name, p.Type)
		}
	}

	return nil
}

// paramHasType reports whether v, as decoded from HCL, is of the
// ParamSpec type typ.
func paramHasType(v any, typ string) bool {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return false
	}

	switch typ {
	case "string":
		return rv.Kind() == reflect.String
	case "bool":
		return rv.Kind() == reflect.Bool
	case "number":
		return rv.CanInt() || rv.CanUint() || rv.CanFloat()
	case "list(string)":
		return isStringList(rv)
	case "map(string)":
		return isStringMap(rv)
	default:
		return false
	}
}

// isStringList reports whether rv is a slice of strings.
func isStringList(rv reflect.Value) bool {
	if rv.Kind() != reflect.Slice {
		return false
	}

	for i := range rv.Len() {
		if !isString(rv.Index(i)) {
			return false
		}
	}

	return true
}

// isStringMap reports whether rv is a map of string to string.
func isStringMap(rv reflect.Value) bool {
	if rv.Kind() != reflect.Map || rv.Type().Key().Kind() != reflect.String {
		return false
	}

	for it := rv.MapRange(); it.Next(); {
		if !isString(it.Value()) {
			return false
		}
	}

	return true
}

// isString reports whether v is a string, looking through an interface.
func isString(v reflect.Value) bool {
	if v.Kind() == reflect.Interface {
		v = v.Elem()
	}

	return v.Kind() == reflect.String
}

// checkApply refuses an apply value that is not one of the three, and
// workflow on a type without WorkflowApply.
func checkApply(t Type, def Definition) error { //nolint:gocritic // hugeParam: one call per Build
	switch def.Apply {
	case "", ApplyRecommend, ApplyDirect:
		return nil
	case ApplyWorkflow:
		if _, ok := t.(WorkflowApply); !ok {
			return fmt.Errorf("%s: type %s does not support apply = %q", def.ID, t.Name(), def.Apply)
		}

		return nil
	default:
		return fmt.Errorf("%s: apply = %q: want %q, %q or %q", def.ID, def.Apply, ApplyRecommend, ApplyDirect, ApplyWorkflow)
	}
}

// checkTemplate proves the control's template passes its remediable
// rules: remediate an empty repository with SampleVars, apply the
// files, and evaluate again.
func checkTemplate(c Control) error {
	ctx := context.Background()
	in := EvalInput{Repo: RepoContext{Org: SampleVars.Org, Name: SampleVars.Repo, DefaultBranch: SampleVars.DefaultBranch, Vars: SampleVars}}

	empty := &sampleRepo{files: map[string][]byte{}}
	in.Reader = empty

	ev, err := c.Evaluate(ctx, in)
	if err != nil {
		return fmt.Errorf("evaluating an empty repository: %w", err)
	}

	cs, err := c.Remediate(ctx, RemediationInput{EvalInput: in, Evaluation: ev})
	if err != nil {
		return fmt.Errorf("remediating an empty repository: %w", err)
	}

	applied := &sampleRepo{files: map[string][]byte{}}
	for _, f := range cs.Files {
		if !f.Delete {
			applied.files[f.Path] = f.Content
		}
	}

	in.Reader = applied

	after, err := c.Evaluate(ctx, in)
	if err != nil {
		return fmt.Errorf("evaluating the rendered template: %w", err)
	}

	remediable := make(map[RuleID]bool)
	for _, r := range c.Rules() {
		remediable[r.ID] = r.Remediate
	}

	for _, res := range after.Results {
		if remediable[res.RuleID] && res.Status != Pass {
			return fmt.Errorf("rule %s is %s on the rendered template", res.RuleID, res.Status)
		}
	}

	return nil
}

// sampleRepo is the in-memory Reader checkTemplate evaluates against:
// files only, every API read empty.
type sampleRepo struct {
	files map[string][]byte
}

func (s *sampleRepo) GetContents(_ context.Context, file string) ([]byte, string, bool, error) {
	b, ok := s.files[file]
	if !ok {
		return nil, "", false, nil
	}

	return slices.Clone(b), "sample", true, nil
}

// ListDirectory returns the names of the files directly under dir.
func (s *sampleRepo) ListDirectory(_ context.Context, dir string) ([]string, error) {
	if dir == "" {
		dir = rootTree
	}

	var names []string

	for file := range s.files {
		if path.Dir(file) == dir {
			names = append(names, path.Base(file))
		}
	}

	slices.Sort(names)

	return names, nil
}

func (*sampleRepo) GetRepository(context.Context) (RepositorySettings, error) {
	return RepositorySettings{}, nil
}

func (*sampleRepo) ListRulesets(context.Context) ([]Ruleset, error) { return nil, nil }

func (*sampleRepo) GetCustomProperties(context.Context) (map[string]*string, error) {
	return map[string]*string{}, nil
}

func (*sampleRepo) OrgPropertySchema(context.Context) ([]string, error) { return nil, nil }

func (*sampleRepo) ListLabels(context.Context) ([]Label, error) { return nil, nil }

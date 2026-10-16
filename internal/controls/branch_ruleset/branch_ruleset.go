// Package branch_ruleset is the branch-ruleset control type (DESIGN-0031
// § Branch ruleset): one definition declares one repository ruleset by
// name. exists says a repository-sourced ruleset of that name is present
// and active; matches that it equals the declaration. The fix is one
// upsert by id. A same-named ruleset inherited from the organization or
// the enterprise is never written: the repository cannot change
// inherited policy, so both rules are unknown{reason=inherited}.
package branch_ruleset

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"

	"github.com/donaldgifford/repo-guardian/internal/control"
)

// Name is the type name a catalogue definition names.
const Name = "branch_ruleset"

// The rule kinds.
const (
	KindExists  control.RuleKind = "exists"
	KindMatches control.RuleKind = "matches"
)

// The definition parameters: name, and ruleset = { enforcement, target,
// conditions, rules, bypass_actors } in the REST API's shape.
const (
	ParamName    = "name"
	ParamRuleset = "ruleset"
)

// The declared ruleset's fields.
const (
	fieldEnforcement  = "enforcement"
	fieldTarget       = "target"
	fieldConditions   = "conditions"
	fieldRules        = "rules"
	fieldBypassActors = "bypass_actors"
)

// The defaults for an omitted enforcement and target.
const (
	EnforcementActive = "active"
	TargetBranch      = "branch"
)

// ReasonInherited is a same-named ruleset owned by the organization or
// the enterprise.
const ReasonInherited = "inherited"

const (
	evidenceVersion = 1
	evidenceReason  = "reason" // FrameworkEvidence's key
	evidencePresent = "present"
)

// Type is the branch-ruleset control type.
type Type struct{}

var (
	_ control.Type    = Type{}
	_ control.APIType = Type{}
)

// Name returns "branch_ruleset".
func (Type) Name() string { return Name }

// Semantics is 1.
func (Type) Semantics() int { return 1 }

// RemediatesByAPI marks an API type, which takes apply (D15).
func (Type) RemediatesByAPI() {}

// RuleKinds says both kinds are remediable, neither with parameters: the
// ruleset is the definition's.
func (Type) RuleKinds() []control.RuleKindSpec {
	return []control.RuleKindSpec{
		{Kind: KindExists, Remediable: true},
		{Kind: KindMatches, Remediable: true},
	}
}

// Build validates the name and the declared ruleset.
func (Type) Build(def control.Definition, _ control.BuildEnv) (control.Control, error) { //nolint:gocritic // hugeParam: Type.Build's signature
	name, _ := def.Params[ParamName].(string) //nolint:errcheck // empty is checked below
	if name == "" {
		return nil, fmt.Errorf("%s: parameter %q is required", def.ID, ParamName)
	}

	declared, err := declare(def.Params[ParamRuleset])
	if err != nil {
		return nil, fmt.Errorf("%s: %s: %w", def.ID, ParamRuleset, err)
	}

	for _, r := range def.Rules {
		if slices.ContainsFunc(def.Rules, func(o control.Rule) bool { return o.ID != r.ID && o.Kind == r.Kind }) {
			return nil, fmt.Errorf("%s: rule kind %s is declared twice", def.ID, r.Kind)
		}
	}

	enforcement, _ := declared[fieldEnforcement].(string) //nolint:errcheck // declare filled it in
	for _, r := range def.Rules {
		if r.Kind == KindExists && r.Remediate && enforcement != EnforcementActive {
			return nil, fmt.Errorf(
				"%s/%s: a remediable exists rule needs enforcement %q, the ruleset declares %q",
				def.ID,
				r.ID,
				EnforcementActive,
				enforcement,
			)
		}
	}

	return &rulesetControl{def: def, name: name, declared: declared}, nil
}

// declare normalises the declared ruleset to its JSON value, with the
// enforcement and target defaults filled in.
func declare(v any) (map[string]any, error) {
	in, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("must be an object")
	}

	known := []string{fieldEnforcement, fieldTarget, fieldConditions, fieldRules, fieldBypassActors}
	for k := range in {
		if !slices.Contains(known, k) {
			return nil, fmt.Errorf("unknown field %q", k)
		}
	}

	if _, ok := in[fieldRules]; !ok {
		return nil, fmt.Errorf("field %q is required", fieldRules)
	}

	out, err := normalise(in)
	if err != nil {
		return nil, err
	}

	m, _ := out.(map[string]any) //nolint:errcheck // an object stays an object
	if _, ok := m[fieldEnforcement]; !ok {
		m[fieldEnforcement] = EnforcementActive
	}

	if _, ok := m[fieldTarget]; !ok {
		m[fieldTarget] = TargetBranch
	}

	if _, ok := m[fieldRules].([]any); !ok {
		return nil, fmt.Errorf("field %q must be a list", fieldRules)
	}

	return m, nil
}

// normalise is v as JSON would decode it, so a declaration from HCL and
// a ruleset from the API compare by value (numbers as float64).
func normalise(v any) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}

	return out, nil
}

type rulesetControl struct {
	def      control.Definition
	name     string
	declared map[string]any
}

func (c *rulesetControl) ID() control.ID { return c.def.ID }

// Owns is ruleset:<name>.
func (c *rulesetControl) Owns() []control.Resource {
	return []control.Resource{{Kind: control.KindRuleset, Key: c.name}}
}

// Reads is the same ruleset; it carries no push signal, so the control
// is evaluated on the schedule.
func (c *rulesetControl) Reads() []control.Resource { return c.Owns() }

func (c *rulesetControl) Rules() []control.Rule { return append([]control.Rule(nil), c.def.Rules...) }

// observation is the named ruleset as this evaluation sees it.
type observation struct {
	failed    error
	inherited control.RulesetSource
	repo      *control.Ruleset
}

func (c *rulesetControl) observe(ctx context.Context, r control.Reader) observation {
	list, err := r.ListRulesets(ctx)
	if err != nil {
		return observation{failed: err}
	}

	var obs observation

	for i := range list {
		rs := &list[i]
		if rs.Name != c.name {
			continue
		}

		if rs.Source == control.RulesetSourceRepository {
			obs.repo = rs
		} else {
			obs.inherited = rs.Source
		}
	}

	return obs
}

// Evaluate reads the rulesets once and judges both rules from them.
//
//nolint:gocritic // hugeParam: the Control interface is DESIGN-0031's.
func (c *rulesetControl) Evaluate(ctx context.Context, in control.EvalInput) (control.Evaluation, error) {
	obs := c.observe(ctx, in.Reader)
	results := make([]control.RuleResult, 0, len(c.def.Rules))

	for _, r := range c.def.Rules {
		res := c.judge(&obs, r.Kind)
		res.RuleID = r.ID
		results = append(results, res)
	}

	return control.Evaluation{Results: results}, nil
}

func (c *rulesetControl) judge(obs *observation, kind control.RuleKind) control.RuleResult {
	switch {
	case errors.Is(obs.failed, control.ErrPermission):
		return c.framework(control.Unknown, control.ReasonPermission, nil)
	case obs.failed != nil:
		return c.framework(control.Error, control.ReasonReadError, nil)
	case obs.inherited != "":
		return c.framework(control.Unknown, ReasonInherited, map[string]any{"source": string(obs.inherited)})
	case obs.repo == nil:
		return control.RuleResult{Status: control.Fail, Evidence: c.evidence(kind, map[string]any{evidencePresent: false})}
	}

	pass := obs.repo.Enforcement == EnforcementActive
	detail := map[string]any{evidencePresent: true, "id": obs.repo.ID, fieldEnforcement: obs.repo.Enforcement}

	if kind == KindMatches {
		diff, err := c.differences(obs.repo)
		if err != nil {
			return c.framework(control.Unknown, control.ReasonParseError, map[string]any{"error": err.Error()})
		}

		pass, detail = len(diff) == 0, map[string]any{evidencePresent: true, "id": obs.repo.ID, "differs": diff}
	}

	status := control.Fail
	if pass {
		status = control.Pass
	}

	return control.RuleResult{Status: status, Evidence: c.evidence(kind, detail)}
}

func (c *rulesetControl) evidence(kind control.RuleKind, detail map[string]any) map[string]any {
	ev := map[string]any{control.EvidenceKind: Name + "." + string(kind), control.EvidenceVersion: evidenceVersion, ParamName: c.name}
	for k, v := range detail {
		ev[k] = v
	}

	return ev
}

func (c *rulesetControl) framework(status control.RuleStatus, reason string, detail map[string]any) control.RuleResult {
	ev := control.FrameworkEvidence(reason)
	ev[ParamName] = c.name

	for k, v := range detail {
		ev[k] = v
	}

	return control.RuleResult{Status: status, Evidence: ev}
}

// differences names the declared fields the observed ruleset does not
// match, sorted. A declared object matches when every key it names
// matches (the API fills in defaults the declaration may omit); lists
// match element by element, the rules list after sorting both sides by
// rule type, because the order of rules carries no meaning.
func (c *rulesetControl) differences(rs *control.Ruleset) ([]string, error) {
	observed := map[string]any{fieldEnforcement: rs.Enforcement, fieldTarget: rs.Target}

	for field, raw := range map[string]json.RawMessage{fieldConditions: rs.Conditions, fieldRules: rs.Rules, fieldBypassActors: rs.BypassActors} {
		if raw == nil {
			continue
		}

		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("%s: %w", field, err)
		}

		observed[field] = v
	}

	var diff []string

	for field, want := range c.declared {
		got := observed[field]
		if field == fieldRules {
			want, got = byType(want), byType(got)
		}

		if !contains(got, want) {
			diff = append(diff, field)
		}
	}

	slices.Sort(diff)

	return diff, nil
}

// contains reports whether got matches want: objects by want's keys,
// lists by length and element, scalars by value.
func contains(got, want any) bool {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return false
		}

		for k, wv := range w {
			if !contains(g[k], wv) {
				return false
			}
		}

		return true
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return false
		}

		for i := range w {
			if !contains(g[i], w[i]) {
				return false
			}
		}

		return true
	default:
		return reflect.DeepEqual(got, want)
	}
}

// byType returns a list of rules sorted by their type.
func byType(v any) any {
	list, ok := v.([]any)
	if !ok {
		return v
	}

	out := slices.Clone(list)
	slices.SortStableFunc(out, func(a, b any) int { return cmp.Compare(ruleType(a), ruleType(b)) })

	return out
}

func ruleType(v any) string {
	m, _ := v.(map[string]any) //nolint:errcheck // a non-object sorts first
	t, _ := m["type"].(string) //nolint:errcheck // as above

	return t
}

// Remediate upserts the declared ruleset when a remediable rule fails:
// one write fixes both rules. An inherited or unreadable ruleset is
// Blocked with a note and never written.
//
//nolint:gocritic // hugeParam: the Control interface is DESIGN-0031's.
func (c *rulesetControl) Remediate(ctx context.Context, in control.RemediationInput) (control.ChangeSet, error) {
	var (
		cs      control.ChangeSet
		failing []control.Rule
		write   bool
	)

	for _, res := range in.Evaluation.Results {
		r := c.rule(res.RuleID)

		switch res.Status {
		case control.Unknown, control.Error:
			cs.Blocked = append(cs.Blocked, r.ID)
			cs.Notes = append(cs.Notes, c.blockedNote(r.ID, res.Evidence))
		case control.Fail:
			failing = append(failing, r)
			write = write || r.Remediate
		case control.Pass, control.NotApplicable:
		}
	}

	if !write {
		for _, r := range failing {
			cs.Manual = append(cs.Manual, r.ID)
			cs.Notes = append(cs.Notes, fmt.Sprintf("%s/%s: create or update ruleset %q by hand", c.def.ID, r.ID, c.name))
		}

		return cs, nil
	}

	rs, err := c.desired(ctx, in.Reader)
	if err != nil {
		return control.ChangeSet{}, err
	}

	cs.API = append(cs.API, control.APIChange{
		Resource: control.Resource{Kind: control.KindRuleset, Key: c.name},
		Op:       control.OpSet, Value: rs, Reason: "rule_failed:" + string(failing[0].ID),
	})

	for _, r := range failing {
		cs.Fixed = append(cs.Fixed, r.ID)
	}

	return cs, nil
}

func (c *rulesetControl) blockedNote(id control.RuleID, ev map[string]any) string {
	if ev[evidenceReason] == ReasonInherited {
		return fmt.Sprintf("%s/%s: ruleset %q is inherited from the %v and is never written; change it there, or rename this definition's ruleset",
			c.def.ID, id, c.name, ev["source"])
	}

	return fmt.Sprintf("%s/%s: the rulesets could not be read; check the App's permissions", c.def.ID, id)
}

// desired is the declared ruleset as a repository ruleset, carrying the
// existing one's id so the write updates it in place.
func (c *rulesetControl) desired(ctx context.Context, r control.Reader) (control.Ruleset, error) {
	rs := control.Ruleset{Name: c.name, Source: control.RulesetSourceRepository}

	if obs := c.observe(ctx, r); obs.repo != nil {
		rs.ID = obs.repo.ID
	}

	rs.Enforcement, _ = c.declared[fieldEnforcement].(string) //nolint:errcheck // declare filled it in
	rs.Target, _ = c.declared[fieldTarget].(string)           //nolint:errcheck // as above

	for field, dst := range map[string]*json.RawMessage{fieldConditions: &rs.Conditions, fieldRules: &rs.Rules, fieldBypassActors: &rs.BypassActors} {
		v, ok := c.declared[field]
		if !ok {
			continue
		}

		b, err := json.Marshal(v)
		if err != nil {
			return control.Ruleset{}, fmt.Errorf("%s: %s: %w", c.def.ID, field, err)
		}

		*dst = b
	}

	return rs, nil
}

func (c *rulesetControl) rule(id control.RuleID) control.Rule {
	for _, r := range c.def.Rules {
		if r.ID == id {
			return r
		}
	}

	return control.Rule{ID: id}
}

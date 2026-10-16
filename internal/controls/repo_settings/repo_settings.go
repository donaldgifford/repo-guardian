// Package repo_settings is the repository-settings control type
// (DESIGN-0031 § Repository settings): each equals rule pins one
// repository setting to a value, and the fix sets it through the API.
// It owns setting:<property> for every property its rules name.
package repo_settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/donaldgifford/repo-guardian/internal/control"
)

// Name is the type name a catalogue definition names.
const Name = "repo_settings"

// KindEquals is the type's one rule kind.
const KindEquals control.RuleKind = "equals"

// The equals parameters.
const (
	ParamProperty = "property"
	ParamValue    = "value"
)

// propertyDefaultBranch may be checked but never remediated: renaming a
// branch is not a settings change.
const propertyDefaultBranch = "default_branch"

const evidenceVersion = 1

// properties maps every supported property to its value kind: the
// scalar fields of control.RepositorySettings by JSON name, plus
// vulnerability_alerts, read from its own endpoint.
var properties = func() map[string]reflect.Kind {
	out := map[string]reflect.Kind{control.SettingVulnerabilityAlerts: reflect.Bool}

	t := reflect.TypeFor[control.RepositorySettings]()
	for i := range t.NumField() {
		f := t.Field(i)
		if f.Type.Kind() != reflect.Pointer {
			continue // security_and_analysis is not a single setting
		}

		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		out[name] = f.Type.Elem().Kind()
	}

	return out
}()

// Properties returns the supported property names, sorted.
func Properties() []string {
	out := make([]string, 0, len(properties))
	for p := range properties {
		out = append(out, p)
	}

	slices.Sort(out)

	return out
}

// Type is the repository-settings control type.
type Type struct{}

var _ control.Type = Type{}

// Name returns "repo_settings".
func (Type) Name() string { return Name }

// Semantics is 1.
func (Type) Semantics() int { return 1 }

// RuleKinds is equals{property, value}, remediable.
func (Type) RuleKinds() []control.RuleKindSpec {
	return []control.RuleKindSpec{{
		Kind:       KindEquals,
		Remediable: true,
		Params: []control.ParamSpec{
			{Name: ParamProperty, Type: control.ParamString, Required: true},
			{Name: ParamValue, Type: control.ParamAny, Required: true},
		},
	}}
}

// Build checks every rule names a supported property once, with a value
// of the property's kind.
func (Type) Build(def control.Definition, _ control.BuildEnv) (control.Control, error) { //nolint:gocritic // hugeParam: Type.Build's signature
	c := &settingsControl{def: def, byRule: make(map[control.RuleID]setting, len(def.Rules))}
	seen := make(map[string]control.RuleID)

	for _, r := range def.Rules {
		prop, _ := r.Params[ParamProperty].(string) //nolint:errcheck // the registry checked its type

		kind, ok := properties[prop]
		if !ok {
			return nil, fmt.Errorf("%s/%s: unknown property %q; supported: %s", def.ID, r.ID, prop, strings.Join(Properties(), ", "))
		}

		if other, dup := seen[prop]; dup {
			return nil, fmt.Errorf("%s/%s: property %q is already pinned by rule %s", def.ID, r.ID, prop, other)
		}

		value := r.Params[ParamValue]
		if reflect.ValueOf(value).Kind() != kind {
			return nil, fmt.Errorf("%s/%s: property %q takes a %s value, got %T", def.ID, r.ID, prop, kind, value)
		}

		if prop == propertyDefaultBranch && r.Remediate {
			return nil, fmt.Errorf("%s/%s: %s can be checked but not remediated", def.ID, r.ID, propertyDefaultBranch)
		}

		seen[prop] = r.ID
		c.byRule[r.ID] = setting{property: prop, value: value}
		c.owned = append(c.owned, control.Resource{Kind: control.KindSetting, Key: prop})
	}

	return c, nil
}

// setting is one rule's pinned property.
type setting struct {
	property string
	value    any
}

type settingsControl struct {
	def    control.Definition
	byRule map[control.RuleID]setting
	owned  []control.Resource
}

func (c *settingsControl) ID() control.ID { return c.def.ID }

// Owns is setting:<property> per rule.
func (c *settingsControl) Owns() []control.Resource {
	return append([]control.Resource(nil), c.owned...)
}

// Reads is the same settings; they carry no push signal, so the control
// is evaluated on the schedule.
func (c *settingsControl) Reads() []control.Resource { return c.Owns() }

func (c *settingsControl) Rules() []control.Rule { return append([]control.Rule(nil), c.def.Rules...) }

// Evaluate reads the settings once and compares each pinned property. A
// property the response lacks is unknown, never a mismatch; a refused
// read is unknown{permission} for the rules that needed it alone.
//
//nolint:gocritic // hugeParam: the Control interface is DESIGN-0031's.
func (c *settingsControl) Evaluate(ctx context.Context, in control.EvalInput) (control.Evaluation, error) {
	results := make([]control.RuleResult, 0, len(c.def.Rules))

	for _, r := range c.def.Rules {
		s := c.byRule[r.ID]
		got, err := read(ctx, in.Reader, s.property)

		res := control.RuleResult{RuleID: r.ID}

		switch {
		case errors.Is(err, control.ErrPermission):
			res.Status, res.Evidence = control.Unknown, framework(control.ReasonPermission, s.property)
		case err != nil:
			res.Status, res.Evidence = control.Error, framework(control.ReasonReadError, s.property)
		case got == nil:
			res.Status, res.Evidence = control.Unknown, framework("not_returned", s.property)
		default:
			res.Status = control.Fail
			if got == s.value {
				res.Status = control.Pass
			}

			res.Evidence = map[string]any{
				control.EvidenceKind: Name + "." + string(KindEquals), control.EvidenceVersion: evidenceVersion,
				ParamProperty: s.property, "want": s.value, "got": got,
			}
		}

		results = append(results, res)
	}

	return control.Evaluation{Results: results}, nil
}

func framework(reason, property string) map[string]any {
	ev := control.FrameworkEvidence(reason)
	ev[ParamProperty] = property

	return ev
}

// read returns one property's current value, nil when not returned.
func read(ctx context.Context, r control.Reader, property string) (any, error) {
	if property == control.SettingVulnerabilityAlerts {
		return r.GetVulnerabilityAlerts(ctx)
	}

	s, err := r.GetRepository(ctx)
	if err != nil {
		return nil, err
	}

	b, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}

	var fields map[string]any
	if err := json.Unmarshal(b, &fields); err != nil {
		return nil, err
	}

	return fields[property], nil
}

// Remediate sets each failing, remediable property to its pinned value.
// Unjudged rules are Blocked; non-remediable ones Manual.
//
//nolint:gocritic // hugeParam: the Control interface is DESIGN-0031's.
func (c *settingsControl) Remediate(_ context.Context, in control.RemediationInput) (control.ChangeSet, error) {
	var cs control.ChangeSet

	for _, res := range in.Evaluation.Results {
		r, s := c.rule(res.RuleID), c.byRule[res.RuleID]

		switch {
		case res.Status == control.Unknown || res.Status == control.Error:
			cs.Blocked = append(cs.Blocked, r.ID)
			cs.Notes = append(cs.Notes, fmt.Sprintf("%s/%s: %s could not be read; check the App's permissions", c.def.ID, r.ID, s.property))
		case res.Status != control.Fail:
		case !r.Remediate:
			cs.Manual = append(cs.Manual, r.ID)
			cs.Notes = append(cs.Notes, fmt.Sprintf("%s/%s: set %s to %v by hand", c.def.ID, r.ID, s.property, s.value))
		default:
			cs.API = append(cs.API, control.APIChange{
				Resource: control.Resource{Kind: control.KindSetting, Key: s.property},
				Op:       control.OpSet, Value: s.value, Reason: "rule_failed:" + string(r.ID),
			})
			cs.Fixed = append(cs.Fixed, r.ID)
		}
	}

	return cs, nil
}

func (c *settingsControl) rule(id control.RuleID) control.Rule {
	for _, r := range c.def.Rules {
		if r.ID == id {
			return r
		}
	}

	return control.Rule{ID: id}
}

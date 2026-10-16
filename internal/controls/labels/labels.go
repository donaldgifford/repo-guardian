// Package labels is the labels control type (DESIGN-0031 § Labels): each
// present rule declares one label with its colour and description, each
// absent rule one retired label. Names compare case-insensitively, as
// GitHub's do: a label differing only in case is updated, never created
// (INV-0022 spike 10), and a retired label is deleted through
// Writer.DeleteLabel. It owns label:<lower-cased name> per rule.
package labels

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/donaldgifford/repo-guardian/internal/control"
)

// Name is the type name a catalogue definition names.
const Name = "labels"

// The rule kinds.
const (
	KindPresent control.RuleKind = "present"
	KindAbsent  control.RuleKind = "absent"
)

// The rule parameters.
const (
	ParamName        = "name"
	ParamColor       = "color"
	ParamDescription = "description"
)

const (
	evidenceVersion = 1
	evidencePresent = "present"
)

// validColor is six hex digits without a leading "#", as the API takes.
var validColor = regexp.MustCompile(`^[0-9a-fA-F]{6}$`)

// Type is the labels control type.
type Type struct{}

var (
	_ control.Type    = Type{}
	_ control.APIType = Type{}
)

// Name returns "labels".
func (Type) Name() string { return Name }

// Semantics is 1.
func (Type) Semantics() int { return 1 }

// RemediatesByAPI marks an API type, which takes apply (D15).
func (Type) RemediatesByAPI() {}

// RuleKinds is present{name, color, description} and absent{name}, both
// remediable. An omitted description is not checked, and an update
// keeps the label's own.
func (Type) RuleKinds() []control.RuleKindSpec {
	name := control.ParamSpec{Name: ParamName, Type: control.ParamString, Required: true}

	return []control.RuleKindSpec{
		{Kind: KindPresent, Remediable: true, Params: []control.ParamSpec{
			name,
			{Name: ParamColor, Type: control.ParamString, Required: true},
			{Name: ParamDescription, Type: control.ParamString},
		}},
		{Kind: KindAbsent, Remediable: true, Params: []control.ParamSpec{name}},
	}
}

// Build checks every rule names a label no other rule names, compared
// case-insensitively, and that colours are six hex digits.
func (Type) Build(def control.Definition, _ control.BuildEnv) (control.Control, error) { //nolint:gocritic // hugeParam: Type.Build's signature
	c := &labelsControl{def: def, byRule: make(map[control.RuleID]label, len(def.Rules))}
	seen := make(map[string]control.RuleID)

	for _, r := range def.Rules {
		l := label{name: str(r.Params[ParamName]), color: str(r.Params[ParamColor])}
		l.description, l.hasDescription = r.Params[ParamDescription].(string)

		if strings.TrimSpace(l.name) == "" {
			return nil, fmt.Errorf("%s/%s: %s must not be empty", def.ID, r.ID, ParamName)
		}

		res := control.MustCanonical(control.KindLabel, l.name)
		if other, dup := seen[res.Key]; dup {
			return nil, fmt.Errorf("%s/%s: label %q is already managed by rule %s", def.ID, r.ID, l.name, other)
		}

		if r.Kind == KindPresent && !validColor.MatchString(l.color) {
			return nil, fmt.Errorf("%s/%s: %s %q must be six hex digits without a leading #", def.ID, r.ID, ParamColor, l.color)
		}

		seen[res.Key] = r.ID
		c.byRule[r.ID] = l
		c.owned = append(c.owned, res)
	}

	return c, nil
}

// label is one rule's declared label.
type label struct {
	name, color, description string
	hasDescription           bool
}

type labelsControl struct {
	def    control.Definition
	byRule map[control.RuleID]label
	owned  []control.Resource
}

func (c *labelsControl) ID() control.ID { return c.def.ID }

// Owns is label:<lower-cased name> per rule.
func (c *labelsControl) Owns() []control.Resource {
	return append([]control.Resource(nil), c.owned...)
}

// Reads is the same labels; they carry no push signal, so the control is
// evaluated on the schedule.
func (c *labelsControl) Reads() []control.Resource { return c.Owns() }

func (c *labelsControl) Rules() []control.Rule { return append([]control.Rule(nil), c.def.Rules...) }

// Evaluate lists the labels once and judges every rule from the list.
//
//nolint:gocritic // hugeParam: the Control interface is DESIGN-0031's.
func (c *labelsControl) Evaluate(ctx context.Context, in control.EvalInput) (control.Evaluation, error) {
	list, err := in.Reader.ListLabels(ctx)
	results := make([]control.RuleResult, 0, len(c.def.Rules))

	for _, r := range c.def.Rules {
		want := c.byRule[r.ID]

		var res control.RuleResult

		switch {
		case errors.Is(err, control.ErrPermission):
			res = c.framework(control.Unknown, control.ReasonPermission, want.name)
		case err != nil:
			res = c.framework(control.Error, control.ReasonReadError, want.name)
		default:
			res = judge(r.Kind, &want, find(list, want.name))
		}

		res.RuleID = r.ID
		results = append(results, res)
	}

	return control.Evaluation{Results: results}, nil
}

func find(list []control.Label, name string) *control.Label {
	for i := range list {
		if strings.EqualFold(list[i].Name, name) {
			return &list[i]
		}
	}

	return nil
}

func judge(kind control.RuleKind, want *label, got *control.Label) control.RuleResult {
	ev := map[string]any{
		control.EvidenceKind: Name + "." + string(kind), control.EvidenceVersion: evidenceVersion,
		ParamName: want.name, evidencePresent: got != nil,
	}

	if kind == KindAbsent {
		return control.RuleResult{Status: pass(got == nil), Evidence: ev}
	}

	if got == nil {
		return control.RuleResult{Status: control.Fail, Evidence: ev}
	}

	differs := []string{}
	if got.Name != want.name {
		differs = append(differs, ParamName)
	}

	if !strings.EqualFold(got.Color, want.color) {
		differs = append(differs, ParamColor)
	}

	if want.hasDescription && got.Description != want.description {
		differs = append(differs, ParamDescription)
	}

	ev["differs"] = differs

	return control.RuleResult{Status: pass(len(differs) == 0), Evidence: ev}
}

func pass(ok bool) control.RuleStatus {
	if ok {
		return control.Pass
	}

	return control.Fail
}

func (*labelsControl) framework(status control.RuleStatus, reason, name string) control.RuleResult {
	ev := control.FrameworkEvidence(reason)
	ev[ParamName] = name

	return control.RuleResult{Status: status, Evidence: ev}
}

// Remediate sets each failing present label and deletes each failing
// absent one. A set carries the declared name, so a case-only
// difference is an update of the existing label.
//
//nolint:gocritic // hugeParam: the Control interface is DESIGN-0031's.
func (c *labelsControl) Remediate(ctx context.Context, in control.RemediationInput) (control.ChangeSet, error) {
	var cs control.ChangeSet

	// The list is read again for the descriptions a set keeps; the
	// evaluation reader caches it.
	list, listErr := in.Reader.ListLabels(ctx)

	for _, res := range in.Evaluation.Results {
		r, want := c.rule(res.RuleID), c.byRule[res.RuleID]

		switch {
		case res.Status == control.Unknown || res.Status == control.Error:
			cs.Blocked = append(cs.Blocked, r.ID)
			cs.Notes = append(cs.Notes, fmt.Sprintf("%s/%s: the labels could not be read; check the App's permissions", c.def.ID, r.ID))

			continue
		case res.Status != control.Fail:
			continue
		case !r.Remediate:
			cs.Manual = append(cs.Manual, r.ID)
			cs.Notes = append(cs.Notes, fmt.Sprintf("%s/%s: %s label %q by hand", c.def.ID, r.ID, verb(r.Kind), want.name))

			continue
		}

		change := control.APIChange{Resource: control.MustCanonical(control.KindLabel, want.name), Reason: "rule_failed:" + string(r.ID)}

		switch {
		case r.Kind == KindAbsent:
			change.Op = control.OpDelete
		case listErr != nil:
			return control.ChangeSet{}, fmt.Errorf("%s: listing labels: %w", c.def.ID, listErr)
		default:
			change.Op, change.Value = control.OpSet, desired(&want, find(list, want.name))
		}

		cs.API = append(cs.API, change)
		cs.Fixed = append(cs.Fixed, r.ID)
	}

	return cs, nil
}

// desired is the declared label, keeping the existing description when
// the rule declares none.
func desired(want *label, got *control.Label) control.Label {
	l := control.Label{Name: want.name, Color: strings.ToLower(want.color), Description: want.description}
	if !want.hasDescription && got != nil {
		l.Description = got.Description
	}

	return l
}

func verb(kind control.RuleKind) string {
	if kind == KindAbsent {
		return "delete"
	}

	return "create or update"
}

func (c *labelsControl) rule(id control.RuleID) control.Rule {
	for _, r := range c.def.Rules {
		if r.ID == id {
			return r
		}
	}

	return control.Rule{ID: id}
}

// str is a string parameter; the registry has checked its type.
func str(v any) string {
	s, _ := v.(string) //nolint:errcheck // absent is the empty string

	return s
}

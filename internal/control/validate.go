package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
)

// MaxEvidenceBytes bounds one rule's evidence, JSON-encoded (D18).
const MaxEvidenceBytes = 16 << 10

// The evidence keys every result carries (D18).
const (
	EvidenceKind    = "evidence_kind"
	EvidenceVersion = "evidence_version"
)

// ReasonMissingResult is the reason Validate gives a declared rule the
// control returned no result for.
const ReasonMissingResult = "missing_result"

// ErrInvalidEvaluation is wrapped by every Validate failure: a bug in the
// control type, which fails the run rather than recording a result.
var ErrInvalidEvaluation = errors.New("invalid evaluation")

// Validate checks an Evaluation before status derivation (D18). It
// returns the results in the control's declared rule order with exactly
// one result per rule: a declared rule with no result becomes
// error{reason=missing_result}. A result for an undeclared rule, a second
// result for one rule, a status outside the closed set, or evidence that
// is not flat, is over MaxEvidenceBytes, or lacks evidence_kind or
// evidence_version is a bug and fails with ErrInvalidEvaluation. Both
// workflows call it; no type does.
func Validate(c Control, ev Evaluation) (Evaluation, error) {
	got := make(map[RuleID]RuleResult, len(ev.Results))

	declared := make(map[RuleID]bool)
	for _, r := range c.Rules() {
		declared[r.ID] = true
	}

	for _, res := range ev.Results {
		if !declared[res.RuleID] {
			return Evaluation{}, fmt.Errorf("%w: %s: result for undeclared rule %q", ErrInvalidEvaluation, c.ID(), res.RuleID)
		}

		if _, dup := got[res.RuleID]; dup {
			return Evaluation{}, fmt.Errorf("%w: %s: two results for rule %q", ErrInvalidEvaluation, c.ID(), res.RuleID)
		}

		if err := validateResult(res); err != nil {
			return Evaluation{}, fmt.Errorf("%w: %s/%s: %w", ErrInvalidEvaluation, c.ID(), res.RuleID, err)
		}

		got[res.RuleID] = res
	}

	out := Evaluation{Results: make([]RuleResult, 0, len(c.Rules())), Reads: ev.Reads}

	for _, r := range c.Rules() {
		res, ok := got[r.ID]
		if !ok {
			res = RuleResult{RuleID: r.ID, Status: Error, Evidence: FrameworkEvidence(ReasonMissingResult)}
		}

		out.Results = append(out.Results, res)
	}

	return out, nil
}

// FrameworkEvidence is the evidence the framework itself writes for a
// result it had to supply.
func FrameworkEvidence(reason string) map[string]any {
	return map[string]any{EvidenceKind: "framework", EvidenceVersion: 1, "reason": reason}
}

func validateResult(res RuleResult) error {
	switch res.Status {
	case Pass, Fail, Error, Unknown, NotApplicable:
	default:
		return fmt.Errorf("status %q is not a rule status", res.Status)
	}

	for _, k := range []string{EvidenceKind, EvidenceVersion} {
		if _, ok := res.Evidence[k]; !ok {
			return fmt.Errorf("evidence lacks %s", k)
		}
	}

	for k, v := range res.Evidence {
		if !flatValue(v) {
			return fmt.Errorf("evidence %q is not a scalar or an array of scalars", k)
		}
	}

	b, err := json.Marshal(res.Evidence)
	if err != nil {
		return fmt.Errorf("evidence does not encode: %w", err)
	}

	if len(b) > MaxEvidenceBytes {
		return fmt.Errorf("evidence is %d bytes, over %d", len(b), MaxEvidenceBytes)
	}

	return nil
}

// flatValue reports whether v is a scalar or an array of scalars.
func flatValue(v any) bool {
	if scalar(v) {
		return true
	}

	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return false
	}

	for i := range rv.Len() {
		if !scalar(rv.Index(i).Interface()) {
			return false
		}
	}

	return true
}

func scalar(v any) bool {
	if v == nil {
		return true
	}

	switch reflect.ValueOf(v).Kind() {
	case reflect.String, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	default:
		return false
	}
}

// StatusOf derives the control status from validated rule results, in
// the order of DESIGN-0031's table: any fail is non_compliant; else any
// error or unknown is unknown; else all not_applicable is
// not_applicable; else compliant. No results at all is unknown: Validate
// never produces that, and an empty evaluation must not read as a pass.
func StatusOf(ev Evaluation) Status {
	if len(ev.Results) == 0 {
		return StatusUnknown
	}

	var undetermined bool

	allNA := true

	for _, r := range ev.Results {
		switch r.Status {
		case Fail:
			return StatusNonCompliant
		case Error, Unknown:
			undetermined = true
		}

		if r.Status != NotApplicable {
			allNA = false
		}
	}

	switch {
	case undetermined:
		return StatusUnknown
	case allNA:
		return StatusNotApplicable
	default:
		return StatusCompliant
	}
}

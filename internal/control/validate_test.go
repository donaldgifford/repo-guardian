package control

import (
	"errors"
	"strings"
	"testing"
)

func TestStatusOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		statuses []RuleStatus
		want     Status
	}{
		{name: "any fail", statuses: []RuleStatus{Pass, Fail, Error, Unknown, NotApplicable}, want: StatusNonCompliant},
		{name: "fail outranks unknown", statuses: []RuleStatus{Unknown, Fail}, want: StatusNonCompliant},
		{name: "error without fail", statuses: []RuleStatus{Pass, Error}, want: StatusUnknown},
		{name: "unknown without fail", statuses: []RuleStatus{Pass, Unknown, NotApplicable}, want: StatusUnknown},
		{name: "all not applicable", statuses: []RuleStatus{NotApplicable, NotApplicable}, want: StatusNotApplicable},
		{name: "pass and not applicable", statuses: []RuleStatus{Pass, NotApplicable}, want: StatusCompliant},
		{name: "all pass", statuses: []RuleStatus{Pass, Pass}, want: StatusCompliant},
		{name: "no results", want: StatusUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var e Evaluation
			for i, s := range tt.statuses {
				e.Results = append(e.Results, result(RuleID(string(rune('a'+i))), s))
			}

			if got := StatusOf(e); got != tt.want {
				t.Errorf("StatusOf(%v) = %s, want %s", tt.statuses, got, tt.want)
			}
		})
	}
}

func TestValidate_FillsMissingAndOrders(t *testing.T) {
	t.Parallel()

	c := newStub("first", "second", "third")
	in := Evaluation{
		Results: []RuleResult{result("third", Pass), result("first", Fail)},
		Reads:   []ResourceRead{{Resource: MustCanonical(KindFile, "a"), State: Present}},
	}

	got, err := Validate(c, in)
	if err != nil {
		t.Fatalf("Validate() = %v", err)
	}

	if len(got.Results) != 3 {
		t.Fatalf("Validate() results = %+v, want three", got.Results)
	}

	for i, want := range []RuleID{"first", "second", "third"} {
		if got.Results[i].RuleID != want {
			t.Errorf("result %d = %s, want %s", i, got.Results[i].RuleID, want)
		}
	}

	missing := got.Results[1]
	if missing.Status != Error || missing.Evidence["reason"] != ReasonMissingResult {
		t.Errorf("missing result = %+v, want error{reason=missing_result}", missing)
	}

	if len(got.Reads) != 1 {
		t.Errorf("Validate() dropped reads: %+v", got.Reads)
	}
}

func TestValidate_Rejects(t *testing.T) {
	t.Parallel()

	big := ev()
	big["text"] = strings.Repeat("x", MaxEvidenceBytes)

	tests := []struct {
		name string
		res  []RuleResult
		want string
	}{
		{name: "undeclared rule", res: []RuleResult{result("nope", Pass)}, want: "undeclared"},
		{name: "duplicate", res: []RuleResult{result("a", Pass), result("a", Fail)}, want: "two results"},
		{name: "open status", res: []RuleResult{result("a", "maybe")}, want: "not a rule status"},
		{name: "no evidence kind", res: []RuleResult{{RuleID: "a", Status: Pass, Evidence: map[string]any{EvidenceVersion: 1}}}, want: EvidenceKind},
		{
			name: "no evidence version",
			res:  []RuleResult{{RuleID: "a", Status: Pass, Evidence: map[string]any{EvidenceKind: "x"}}},
			want: EvidenceVersion,
		},
		{name: "nil evidence", res: []RuleResult{{RuleID: "a", Status: Pass}}, want: EvidenceKind},
		{name: "nested map", res: []RuleResult{withEvidence("a", "nested", map[string]any{"k": "v"})}, want: "not a scalar"},
		{name: "array of maps", res: []RuleResult{withEvidence("a", "list", []any{map[string]any{"k": "v"}})}, want: "not a scalar"},
		{name: "nested array", res: []RuleResult{withEvidence("a", "list", []any{[]string{"x"}})}, want: "not a scalar"},
		{name: "oversized", res: []RuleResult{{RuleID: "a", Status: Pass, Evidence: big}}, want: "bytes, over"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := Validate(newStub("a", "b"), Evaluation{Results: tt.res})
			if !errors.Is(err, ErrInvalidEvaluation) || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Validate() = %v, want ErrInvalidEvaluation mentioning %q", err, tt.want)
			}
		})
	}
}

func TestValidate_AcceptsFlatEvidence(t *testing.T) {
	t.Parallel()

	e := ev()
	e["paths"] = []string{"CODEOWNERS", ".github/CODEOWNERS"}
	e["mixed"] = []any{"a", 1, true, nil}
	e["count"] = 3
	e["ratio"] = 0.5
	e["missing"] = nil

	if _, err := Validate(newStub("a"), Evaluation{Results: []RuleResult{{RuleID: "a", Status: Pass, Evidence: e}}}); err != nil {
		t.Errorf("Validate() = %v, want flat evidence accepted", err)
	}
}

func withEvidence(id RuleID, k string, v any) RuleResult {
	e := ev()
	e[k] = v

	return RuleResult{RuleID: id, Status: Pass, Evidence: e}
}

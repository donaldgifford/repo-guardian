package shadow

import (
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/findings"
)

func TestCompare_Classify(t *testing.T) {
	t.Parallel()

	v1 := func(actionable bool) *V1Row {
		return &V1Row{Org: "acme", Repo: "web", RuleKind: "file", RuleName: "codeowners", Actionable: actionable}
	}
	v2 := func(status findings.Status, reason findings.Reason, rem findings.Remediation) V2Row {
		return V2Row{Org: "acme", Repo: "web", RuleKind: "file", RuleName: "codeowners", Status: status, Reason: reason, Remediation: rem}
	}

	tests := []struct {
		name string
		v1   *V1Row
		v2   *V2Row
		want Class
	}{
		{"both pass", v1(false), ptr(v2(findings.StatusCompliant, "", findings.RemediationNone)), Match},
		{"both fail", v1(true), ptr(v2(findings.StatusNonCompliant, findings.ReasonFileMissing, findings.RemediationPROpen)), Match},
		{"not rechecked", v1(true), ptr(v2(findings.StatusNonCompliant, findings.ReasonMigratedFromV1, findings.RemediationNone)), NotRechecked},
		{"foreign PR", v1(false), ptr(v2(findings.StatusNonCompliant, findings.ReasonForeignPROpen, findings.RemediationForeignPR)), ForeignPR},
		{"branch missing", v1(false), ptr(v2(findings.StatusNotApplicable, findings.ReasonBranchMissing, findings.RemediationNone)), BranchMissing},
		{
			"not applicable, no v1 row",
			nil,
			ptr(v2(findings.StatusNotApplicable, findings.ReasonOutOfScopeRule, findings.RemediationNone)),
			NotApplicable,
		},
		{"gate error, no v1 row", nil, ptr(v2(findings.StatusUnknown, findings.ReasonGateError, findings.RemediationNone)), Unknown},
		{"v1 failed, v2 passes", v1(true), ptr(v2(findings.StatusCompliant, "", findings.RemediationNone)), Unexplained},
		{"v1 passed, v2 fails", v1(false), ptr(v2(findings.StatusNonCompliant, findings.ReasonFileMissing, findings.RemediationPROpen)), Unexplained},
		{"v2 fails, no v1 row", nil, ptr(v2(findings.StatusNonCompliant, findings.ReasonFileMissing, findings.RemediationPROpen)), Unexplained},
		{"v1 row, no v2 finding", v1(true), nil, Unexplained},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				v1Rows []V1Row
				v2Rows []V2Row
			)

			if tt.v1 != nil {
				v1Rows = append(v1Rows, *tt.v1)
			}

			if tt.v2 != nil {
				v2Rows = append(v2Rows, *tt.v2)
			}

			rep := Compare([]string{"acme/web"}, []string{"acme/web"}, v1Rows, v2Rows)
			if rep.Counts[tt.want] != 1 || len(rep.Counts) != 1 {
				t.Fatalf("counts = %v, want one %s", rep.Counts, tt.want)
			}

			if got := rep.OK(); got != (tt.want != Unexplained) {
				t.Errorf("OK() = %v, want %v", got, tt.want != Unexplained)
			}
		})
	}
}

func TestCompare_SharedName(t *testing.T) {
	t.Parallel()

	// v1 kept only the last kind written; v2 keeps both.
	v1 := []V1Row{{Org: "acme", Repo: "web", RuleKind: "setting", RuleName: "security", Actionable: false}}
	v2 := []V2Row{
		{Org: "acme", Repo: "web", RuleKind: "setting", RuleName: "security", Status: findings.StatusCompliant},
		{Org: "acme", Repo: "web", RuleKind: "file", RuleName: "security", Status: findings.StatusCompliant},
	}

	rep := Compare([]string{"acme/web"}, []string{"acme/web"}, v1, v2)
	if rep.Counts[Match] != 1 || rep.Counts[SharedName] != 1 || !rep.OK() {
		t.Fatalf("counts = %v, want one match and one shared_name", rep.Counts)
	}
}

func TestCompare_OnlyActiveOnBothSides(t *testing.T) {
	t.Parallel()

	v1 := []V1Row{
		{Org: "Acme", Repo: "Web", RuleKind: "file", RuleName: "codeowners", Actionable: true},
		{Org: "acme", Repo: "parked-in-v2", RuleKind: "file", RuleName: "codeowners", Actionable: true},
	}
	v2 := []V2Row{
		{Org: "acme", Repo: "web", RuleKind: "file", RuleName: "codeowners", Status: findings.StatusNonCompliant, Reason: findings.ReasonFileMissing},
		{Org: "acme", Repo: "new-in-v2", RuleKind: "file", RuleName: "codeowners", Status: findings.StatusNonCompliant},
	}

	rep := Compare([]string{"Acme/Web", "acme/parked-in-v2"}, []string{"acme/web", "acme/new-in-v2"}, v1, v2)

	if rep.Repositories != 1 || rep.OnlyV1 != 1 || rep.OnlyV2 != 1 {
		t.Fatalf("repositories = %d, only v1 = %d, only v2 = %d; want 1, 1, 1", rep.Repositories, rep.OnlyV1, rep.OnlyV2)
	}

	if rep.Counts[Match] != 1 || !rep.OK() {
		t.Fatalf("counts = %v, want one case-insensitive match", rep.Counts)
	}
}

func ptr[T any](v T) *T { return &v }

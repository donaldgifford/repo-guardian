package policy

import "testing"

// workedExample is DESIGN-0030's two overlapping repos blocks; swapped
// puts the blocks in the other order.
func workedExample(swapped bool) string {
	backstage := "  repos \"backstage\" {\n    match    = [\"backstage-*\"]\n    controls = [\"catalog@1\"]\n  }\n"
	legacy := "  repos \"backstage-legacy\" {\n    match = [\"backstage-legacy\"]\n" +
		"    exclude \"catalog@1\" {\n      reason = \"being decommissioned\"\n    }\n  }\n"

	blocks := backstage + legacy
	if swapped {
		blocks = legacy + backstage
	}

	return org("  exclude \"catalog@1\" {\n    reason = \"no Backstage yet\"\n  }\n" + blocks)
}

func TestResolve_WorkedExample(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		swapped   bool
		repo      string
		wantState AssignmentState
		wantBy    string
	}{
		{"backstage-api re-added", false, "backstage-api", AssignmentActive, ""},
		{"backstage-legacy excluded again", false, "backstage-legacy", AssignmentExcluded, "org:acme/repos[backstage-legacy]"},
		{"swapped leaves backstage-legacy active", true, "backstage-legacy", AssignmentActive, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			snap := mustLoad(t, map[string]string{"enterprise.hcl": ent(`"catalog@1"`), "orgs/acme.hcl": workedExample(tt.swapped)})
			res := snap.Resolve(Repository{Org: "acme", Name: tt.repo, Active: true}, AppAccess{Evaluable: true, Remediable: true})

			if len(res.Assignments) != 1 {
				t.Fatalf("Resolve(%s).Assignments = %+v, want one", tt.repo, res.Assignments)
			}

			a := res.Assignments[0]
			if a.State != tt.wantState || a.ExcludedBy != tt.wantBy || a.BaselineControl != "catalog" {
				t.Errorf("Resolve(%s) = state %s, excluded_by %q, baseline %q; want %s, %q, catalog",
					tt.repo, a.State, a.ExcludedBy, a.BaselineControl, tt.wantState, tt.wantBy)
			}
		})
	}
}

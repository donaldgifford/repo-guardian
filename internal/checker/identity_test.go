package checker

import (
	"strings"
	"testing"
)

// TestPRIdentity_IsFrozen locks the strings v1 and v2 share to find and
// adopt each other's pull requests (DESIGN-0026 § Cutover from v1). v2
// resumes v1's open PR, and a rollback resumes v2's, only because these
// are byte-identical. Do not update a literal here to make a change
// pass: changing any of them strands every open repo-guardian PR.
func TestPRIdentity_IsFrozen(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name, got, want string
	}{
		{"branch name", BranchName, "repo-guardian/add-missing-files"},
		{"title", PRTitle, "chore: add missing repo configuration files"},
		{"reconcile-log marker", reconcileLogMarker, "<!-- repo-guardian:reconcile-log:v1 -->"},
		{
			"reconcile-log hash tag",
			reconcileLogHashTag([]reconcileLogEvent{
				{Rule: "codeowners", Status: "ok"},
				{Rule: "renovate", Status: "failing"},
			}),
			"<!-- repo-guardian:reconcile-log:hash:8f9497978856b29e -->",
		},
	} {
		if tt.got != tt.want {
			t.Errorf("%s = %q, want %q", tt.name, tt.got, tt.want)
		}
	}

	body := renderReconcileLog([]reconcileLogEvent{{Rule: "codeowners", Status: "failing"}})
	if !strings.HasPrefix(body, "<!-- repo-guardian:reconcile-log:hash:") {
		t.Errorf("reconcile-log body must open with the hash tag; got %q", body[:min(len(body), 80)])
	}
}

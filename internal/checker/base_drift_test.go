package checker

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// Base-drift tests for INV-0011 B4 (IMPL-0021 task 6.2).
//
// A repo-guardian PR is long-lived: it stays open until every file
// rule it carries is satisfied. Meanwhile the default branch keeps
// moving. These tests pin the contract that the reconcile branch is
// brought up to date with the default branch before files are synced
// onto it, and that failing to do so never blocks the reconcile.

func TestBaseDrift_ExistingPR_BranchUpdatedBeforeSync(t *testing.T) {
	s := newStagedConvergence(t, nil)
	s.openOurPR(7)

	s.sweep()

	if !slices.Equal(s.client.updatedPRBranches, []int{7}) {
		t.Errorf("UpdatePRBranch calls = %v, want [7]", s.client.updatedPRBranches)
	}
}

// A branch created fresh in this same sweep is already at the default
// branch's HEAD, so spending an API call to update it would be waste.
func TestBaseDrift_NoExistingPR_BranchNotUpdated(t *testing.T) {
	s := newStagedConvergence(t, nil)

	s.sweep()

	if len(s.client.updatedPRBranches) != 0 {
		t.Errorf("UpdatePRBranch calls = %v, want none for a freshly created branch",
			s.client.updatedPRBranches)
	}
}

// Steady state — the actionable set has not moved since the last sweep,
// so the re-rendered PR text is identical and the PATCH is waste
// (INV-0011 B4, IMPL-0021 task 6.3).
func TestPRRefresh_UnchangedText_SkipsPatch(t *testing.T) {
	s := newStagedConvergence(t, nil)
	s.openOurPR(7)

	// Sweep 1: PR text is empty on the mock's PR, so the refresh
	// patches it with the freshly rendered body.
	s.sweep()

	if s.client.updatePRCalls != 1 {
		t.Fatalf("UpdatePullRequest calls after sweep 1 = %d, want 1", s.client.updatePRCalls)
	}

	// Sweep 2: nothing changed on the repo, so the render is identical
	// to what sweep 1 wrote and no PATCH should go out.
	s.sweep()

	if s.client.updatePRCalls != 1 {
		t.Errorf("UpdatePullRequest calls after an unchanged sweep 2 = %d, want 1",
			s.client.updatePRCalls)
	}
}

// A body a human re-saved through the web UI comes back CRLF-encoded.
// Nothing about it actually changed, so it must not draw a PATCH on
// every sweep for the rest of the PR's life.
func TestPRRefresh_CRLFBodyFromWebUI_SkipsPatch(t *testing.T) {
	s := newStagedConvergence(t, nil)
	s.openOurPR(7)

	s.sweep()

	crlf := strings.ReplaceAll(s.client.updatedPRBody, "\n", "\r\n")
	s.client.openPRs[0].Body = crlf
	s.client.updatePRCalls = 0

	s.sweep()

	if s.client.updatePRCalls != 0 {
		t.Errorf("UpdatePullRequest calls for a CRLF-normalized body = %d, want 0",
			s.client.updatePRCalls)
	}
}

// The update is best-effort: a reconcile branch can legitimately
// conflict with base (a human edited the same file on it), and
// refusing to reconcile over that would strand the PR entirely.
func TestBaseDrift_UpdateError_ReconcileContinues(t *testing.T) {
	s := newStagedConvergence(t, nil)
	s.openOurPR(7)
	s.client.updatePRBranchErr = errors.New("merge conflict")

	s.sweep()

	if len(s.client.createdFiles) != 2 {
		t.Errorf("createdFiles = %v, want the sync to run despite the branch-update failure",
			s.client.createdFiles)
	}
}

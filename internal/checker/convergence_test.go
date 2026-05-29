package checker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/policy"
)

// Multi-sweep convergence tests for IMPL-0013 Phase 3.
//
// Each test simulates two or three sequential CheckRepo invocations
// against the same mockClient and asserts how the engine reacts to
// the file rules being progressively satisfied on the default
// branch.

// stagedConvergenceState carries the bookkeeping a test needs to
// advance the simulated repository state between sweeps.
type stagedConvergenceState struct {
	org    string
	repo   string
	branch string
	client *mockClient
	engine *Engine
	t      *testing.T
}

func newStagedConvergence(t *testing.T, autoClose *bool) *stagedConvergenceState {
	t.Helper()

	cfg := policy.BuiltinDefaults()
	cfg.Guardian.AutoClosePR = autoClose

	engine := testPolicyEngine(cfg)

	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}
	client.branchSHAs["org/repo/main"] = "main-sha"

	return &stagedConvergenceState{
		org:    "org",
		repo:   "repo",
		branch: BranchName,
		client: client,
		engine: engine,
		t:      t,
	}
}

func (s *stagedConvergenceState) sweep() {
	s.t.Helper()
	if err := s.engine.CheckRepo(context.Background(), s.client, s.org, s.repo); err != nil {
		s.t.Fatalf("CheckRepo: %v", err)
	}
}

func (s *stagedConvergenceState) satisfyOnMain(path string) {
	s.client.contents[s.org+"/"+s.repo+"/"+path] = true
}

func (s *stagedConvergenceState) addOrphanToBranch(path, sha string) {
	s.client.branchContents[s.org+"/"+s.repo+"/"+s.branch+"/"+path] = sha
}

func TestConvergence_Sweep1_OpensPR(t *testing.T) {
	metrics.PRsCreatedTotal.Reset()

	s := newStagedConvergence(t, nil)

	s.sweep()

	if len(s.client.createdFiles) != 2 {
		t.Errorf("expected 2 files created on sweep 1, got %d: %v",
			len(s.client.createdFiles), s.client.createdFiles)
	}
	if got := testutil.ToFloat64(metrics.PRsCreatedTotal.WithLabelValues(s.org)); got != 1 {
		t.Errorf("PRsCreatedTotal{%q} = %v, want 1", s.org, got)
	}
}

// Sweep 1: 2 rules fail → PR opened.
// Sweep 2: CODEOWNERS satisfied on main → file removed from branch,
//
//	PR updated with body describing only Dependabot.
func TestConvergence_Sweep2_OrphanCleanupAndBodyRefresh(t *testing.T) {
	metrics.PROrphanLeftTotal.Reset()

	s := newStagedConvergence(t, nil)

	// Sweep 1: open PR (both rules actionable).
	s.sweep()

	// Simulate the branch having both files committed by the open PR.
	// Targets match the BuiltinDefaults: .github/CODEOWNERS and
	// .github/dependabot.yml.
	s.addOrphanToBranch(".github/CODEOWNERS", "codeowners-blob-sha")
	s.addOrphanToBranch(".github/dependabot.yml", "dependabot-blob-sha")

	// Mark the PR as already open (sweep 1 logic creates a PR but the
	// mock doesn't persist it back to openPRs automatically).
	s.client.openPRs = []*ghclient.PullRequest{
		{Number: 1, Title: PRTitle, Head: s.branch, State: "open"},
	}
	s.client.branchSHAs[s.org+"/"+s.repo+"/"+s.branch] = "branch-sha"

	// Sweep 2: CODEOWNERS satisfied on main.
	s.satisfyOnMain("CODEOWNERS")
	s.client.deletedFiles = nil
	s.client.updatedPRNumber = 0
	s.sweep()

	if len(s.client.deletedFiles) != 1 || s.client.deletedFiles[0] != ".github/CODEOWNERS" {
		t.Errorf("expected .github/CODEOWNERS to be deleted as orphan, deletedFiles=%v",
			s.client.deletedFiles)
	}

	if s.client.updatedPRNumber != 1 {
		t.Errorf("expected UpdatePullRequest called for PR #1, got %d", s.client.updatedPRNumber)
	}
	if !strings.Contains(s.client.updatedPRBody, "dependabot") {
		t.Errorf("expected refreshed body to mention dependabot, got %q", s.client.updatedPRBody)
	}
	// The rule list (under "### Added Files") should no longer include
	// the satisfied codeowners rule entry "— codeowners". The
	// CODEOWNERS placeholder note elsewhere in the template body is
	// static text and does not indicate the rule is still active.
	if strings.Contains(s.client.updatedPRBody, "— codeowners") {
		t.Errorf("refreshed body should not list satisfied rule, got %q", s.client.updatedPRBody)
	}

	if got := testutil.ToFloat64(metrics.PROrphanLeftTotal.WithLabelValues(s.org)); got != 0 {
		t.Errorf("PROrphanLeftTotal{%q} = %v, want 0 (no failures)", s.org, got)
	}
}

// Sweep 3: every rule satisfied on main → PR auto-closed (default).
func TestConvergence_Sweep3_AutoClosesPR_Default(t *testing.T) {
	metrics.PRsClosedTotal.Reset()
	metrics.PROpenWithEmptyActionableTotal.Reset()

	s := newStagedConvergence(t, nil)
	s.client.contents[s.org+"/"+s.repo+"/CODEOWNERS"] = true
	s.client.contents[s.org+"/"+s.repo+"/.github/dependabot.yml"] = true
	s.client.openPRs = []*ghclient.PullRequest{
		{Number: 5, Title: PRTitle, Head: s.branch, State: "open"},
	}

	s.sweep()

	if s.client.closedPRNumber != 5 {
		t.Errorf("expected PR #5 to be closed, got closedPRNumber=%d", s.client.closedPRNumber)
	}
	if got := testutil.ToFloat64(metrics.PRsClosedTotal.WithLabelValues(s.org, "satisfied")); got != 1 {
		t.Errorf("PRsClosedTotal{%q,satisfied} = %v, want 1", s.org, got)
	}
	if len(s.client.upsertedComments) != 1 {
		t.Errorf("expected one upserted close comment, got %d", len(s.client.upsertedComments))
	} else {
		c := s.client.upsertedComments[0]
		if c.PRNumber != 5 {
			t.Errorf("close comment PR = %d, want 5", c.PRNumber)
		}
		if c.Marker != reconcileLogMarker {
			t.Errorf("close comment marker = %q, want %q", c.Marker, reconcileLogMarker)
		}
	}
	if got := testutil.ToFloat64(metrics.PROpenWithEmptyActionableTotal.WithLabelValues(s.org)); got != 1 {
		t.Errorf("drift counter should still increment even when auto-close runs, got %v", got)
	}
}

// Sweep 3 alt: AutoClosePR=false → PR stays open.
func TestConvergence_Sweep3_AutoCloseDisabled_LeavesPROpen(t *testing.T) {
	metrics.PRsClosedTotal.Reset()

	falseVal := false
	s := newStagedConvergence(t, &falseVal)

	s.client.contents[s.org+"/"+s.repo+"/CODEOWNERS"] = true
	s.client.contents[s.org+"/"+s.repo+"/.github/dependabot.yml"] = true
	s.client.openPRs = []*ghclient.PullRequest{
		{Number: 6, Title: PRTitle, Head: s.branch, State: "open"},
	}

	s.sweep()

	if s.client.closedPRNumber != 0 {
		t.Errorf("expected PR to stay open with AutoClosePR=false, got closedPRNumber=%d",
			s.client.closedPRNumber)
	}
	if got := testutil.ToFloat64(metrics.PRsClosedTotal.WithLabelValues(s.org, "satisfied")); got != 0 {
		t.Errorf("PRsClosedTotal{%q,satisfied} = %v, want 0 when AutoClosePR=false", s.org, got)
	}
}

// GetContentsOnBranch error → no delete, no close.
func TestConvergence_GetContentsOnBranchError_FailsSafe(t *testing.T) {
	metrics.PROrphanLeftTotal.Reset()

	s := newStagedConvergence(t, nil)
	s.client.getContentsOnBranchErr = errors.New("simulated 500")
	s.client.contents[s.org+"/"+s.repo+"/CODEOWNERS"] = true
	// dependabot is still actionable.
	s.client.openPRs = []*ghclient.PullRequest{
		{Number: 8, Title: PRTitle, Head: s.branch, State: "open"},
	}
	s.client.branchSHAs[s.org+"/"+s.repo+"/"+s.branch] = "branch-sha"

	s.sweep()

	if len(s.client.deletedFiles) != 0 {
		t.Errorf("expected zero deletes under GetContentsOnBranch error, got %v",
			s.client.deletedFiles)
	}
	if s.client.closedPRNumber != 0 {
		t.Errorf("expected PR to stay open under GetContentsOnBranch error, got closedPRNumber=%d",
			s.client.closedPRNumber)
	}
}

// DeleteFile partial failure increments PROrphanLeftTotal and does
// not abort the sweep.
func TestConvergence_DeleteFileError_IncrementsCounter_Continues(t *testing.T) {
	metrics.PROrphanLeftTotal.Reset()

	s := newStagedConvergence(t, nil)

	s.client.contents[s.org+"/"+s.repo+"/CODEOWNERS"] = true
	// Branch has an orphan CODEOWNERS file at the Target path.
	s.addOrphanToBranch(".github/CODEOWNERS", "codeowners-sha")
	s.client.openPRs = []*ghclient.PullRequest{
		{Number: 9, Title: PRTitle, Head: s.branch, State: "open"},
	}
	s.client.branchSHAs[s.org+"/"+s.repo+"/"+s.branch] = "branch-sha"
	s.client.deleteFileErr = errors.New("simulated 503")

	s.sweep()

	if got := testutil.ToFloat64(metrics.PROrphanLeftTotal.WithLabelValues(s.org)); got != 1 {
		t.Errorf("PROrphanLeftTotal{%q} = %v, want 1 (orphan delete failed)", s.org, got)
	}
}

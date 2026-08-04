package checker

import (
	"context"
	"errors"
	"slices"
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

	return newStagedConvergenceWithPolicy(t, cfg)
}

func newStagedConvergenceWithPolicy(t *testing.T, cfg *policy.PolicyConfig) *stagedConvergenceState {
	t.Helper()

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

// renovateFirstPolicy is the DESIGN-0020 driving policy: add renovate.json
// where missing, and remove dependabot config (both extensions) once
// renovate_config is satisfied on the default branch.
func renovateFirstPolicy() *policy.PolicyConfig {
	return &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		FileRules: []policy.FileRuleConfig{
			{
				Type:     "file",
				Name:     "renovate_config",
				Check:    string(policy.CheckExists),
				Paths:    []string{"renovate.json"},
				Target:   "renovate.json",
				Template: "codeowners",
			},
			{
				Type:  "file",
				Name:  "no_dependabot",
				Check: string(policy.CheckAbsent),
				Paths: []string{".github/dependabot.yml", ".github/dependabot.yaml"},
				When:  &policy.WhenConfig{RuleSatisfied: "renovate_config"},
			},
		},
	}
}

// openOurPR marks a repo-guardian PR as already open on the reconcile
// branch so the next sweep takes the existing-PR update path.
func (s *stagedConvergenceState) openOurPR(number int) {
	s.client.openPRs = []*ghclient.PullRequest{
		{Number: number, Title: PRTitle, Head: s.branch, State: "open"},
	}
	s.client.branchSHAs[s.org+"/"+s.repo+"/"+s.branch] = "branch-sha"
}

// forbiddenOnMainAndBranch seeds a forbidden file as present on both the
// default branch and the reconcile branch (as if the branch was cut from
// main before the file was removed).
func (s *stagedConvergenceState) forbiddenOnMainAndBranch(path, sha string) {
	s.satisfyOnMain(path)
	s.client.fileContents[s.org+"/"+s.repo+"/"+path] = "content-of-" + path
	s.addOrphanToBranch(path, sha)
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

// deletedFromBranch marks a path as deleted on the reconcile branch by a
// previous sweep, while leaving it present on the default branch.
//
// This has to be stated explicitly since INV-0014. Before the fake modelled
// branch inheritance, "present on main" implied "absent from the branch"
// purely because the fake had no way to represent the branch inheriting
// anything — so a fixture could describe this state in a comment and get it
// by accident. Now the branch really does inherit from main, and a test that
// wants a file gone from the branch has to say so.
func (s *stagedConvergenceState) deletedFromBranch(path string) {
	key := s.org + "/" + s.repo + "/" + s.branch + "/" + path
	delete(s.client.branchContents, key)

	if s.client.branchDeleted == nil {
		s.client.branchDeleted = make(map[string]bool)
	}

	s.client.branchDeleted[key] = true
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

// Renovate-first sweep 1 on a dependabot-only repo: renovate_config is
// missing so it is added, while no_dependabot's gate is closed (renovate
// not yet satisfied) so dependabot is left untouched.
func TestConvergence_RenovateFirst_Sweep1_GateClosedSkipsDependabot(t *testing.T) {
	metrics.RuleGateClosedTotal.Reset()

	s := newStagedConvergenceWithPolicy(t, renovateFirstPolicy())
	s.satisfyOnMain(".github/dependabot.yml") // dependabot present, renovate missing

	s.sweep()

	if !slices.Contains(s.client.createdFiles, "renovate.json") {
		t.Errorf("expected renovate.json to be added, createdFiles=%v", s.client.createdFiles)
	}

	if len(s.client.deletedFiles) != 0 {
		t.Errorf("dependabot must not be removed while the gate is closed, deletedFiles=%v", s.client.deletedFiles)
	}

	if got := testutil.ToFloat64(
		metrics.RuleGateClosedTotal.WithLabelValues("no_dependabot", s.org, "not_satisfied"),
	); got != 1 {
		t.Errorf("RuleGateClosedTotal{no_dependabot,%s,not_satisfied} = %v, want 1", s.org, got)
	}
}

// Renovate satisfied on main + dependabot present → the open PR removes
// the forbidden file from the reconcile branch.
func TestConvergence_RenovateFirst_RemovesDependabot(t *testing.T) {
	metrics.FilesForbiddenPresentTotal.Reset()

	s := newStagedConvergenceWithPolicy(t, renovateFirstPolicy())
	s.satisfyOnMain("renovate.json") // referee satisfied
	s.forbiddenOnMainAndBranch(".github/dependabot.yml", "dep-sha")
	s.openOurPR(2)

	s.sweep()

	if len(s.client.deletedFiles) != 1 || s.client.deletedFiles[0] != ".github/dependabot.yml" {
		t.Errorf("expected .github/dependabot.yml deleted, deletedFiles=%v", s.client.deletedFiles)
	}

	if got := testutil.ToFloat64(
		metrics.FilesForbiddenPresentTotal.WithLabelValues("no_dependabot", s.org),
	); got != 1 {
		t.Errorf("FilesForbiddenPresentTotal{no_dependabot,%s} = %v, want 1", s.org, got)
	}
}

// Both dependabot extensions present → both removed in the one PR.
func TestConvergence_RenovateFirst_BothVariantsDeleted(t *testing.T) {
	s := newStagedConvergenceWithPolicy(t, renovateFirstPolicy())
	s.satisfyOnMain("renovate.json")
	s.forbiddenOnMainAndBranch(".github/dependabot.yml", "dep-yml-sha")
	s.forbiddenOnMainAndBranch(".github/dependabot.yaml", "dep-yaml-sha")
	s.openOurPR(3)

	s.sweep()

	if !slices.Contains(s.client.deletedFiles, ".github/dependabot.yml") ||
		!slices.Contains(s.client.deletedFiles, ".github/dependabot.yaml") {
		t.Errorf("expected both dependabot variants deleted, deletedFiles=%v", s.client.deletedFiles)
	}
}

// Re-sweeping identical state after the removal is a no-op: the branch
// already converged, so zero mutating API calls fire.
func TestConvergence_RenovateFirst_IdempotentReSweep_ZeroMutations(t *testing.T) {
	s := newStagedConvergenceWithPolicy(t, renovateFirstPolicy())
	s.satisfyOnMain("renovate.json")
	s.forbiddenOnMainAndBranch(".github/dependabot.yml", "dep-sha")
	s.openOurPR(4)

	s.sweep() // removes dependabot from the branch

	s.client.deletedFiles = nil
	s.client.createdFiles = nil

	s.sweep() // identical state — nothing left to do

	if len(s.client.deletedFiles) != 0 || len(s.client.createdFiles) != 0 {
		t.Errorf("identical re-sweep must not mutate: deleted=%v created=%v",
			s.client.deletedFiles, s.client.createdFiles)
	}
}

// Dependabot hand-deleted from main while a removal PR is open → the
// absent rule is satisfied, nothing else is actionable, PR auto-closes.
func TestConvergence_RenovateFirst_HandDeletedDependabot_AutoCloses(t *testing.T) {
	metrics.PRsClosedTotal.Reset()

	s := newStagedConvergenceWithPolicy(t, renovateFirstPolicy())
	s.satisfyOnMain("renovate.json") // referee satisfied; dependabot absent from main
	s.openOurPR(5)

	s.sweep()

	if s.client.closedPRNumber != 5 {
		t.Errorf("expected PR #5 auto-closed once dependabot is gone from main, got %d", s.client.closedPRNumber)
	}

	if got := testutil.ToFloat64(metrics.PRsClosedTotal.WithLabelValues(s.org, "satisfied")); got != 1 {
		t.Errorf("PRsClosedTotal{%s,satisfied} = %v, want 1", s.org, got)
	}
}

// Gate closes mid-flight in a bundle PR: renovate.json is removed from main
// (gate closes) while the renovate_config add rule keeps the PR alive. The
// dependabot file previously deleted from the branch is restored so the PR
// stops proposing the deletion.
func TestConvergence_RenovateFirst_GateClosesMidFlight_RestoresDependabot(t *testing.T) {
	s := newStagedConvergenceWithPolicy(t, renovateFirstPolicy())

	// renovate.json missing on main → renovate_config actionable (keeps PR
	// alive) and no_dependabot's gate closed. dependabot present on main but
	// already deleted from the branch by a prior sweep.
	s.satisfyOnMain(".github/dependabot.yml")
	s.deletedFromBranch(".github/dependabot.yml")
	s.client.fileContents[s.org+"/"+s.repo+"/.github/dependabot.yml"] = "restored-body"
	s.openOurPR(6)

	s.sweep()

	if !slices.Contains(s.client.createdFiles, ".github/dependabot.yml") {
		t.Errorf("expected dependabot.yml restored to the branch, createdFiles=%v", s.client.createdFiles)
	}
}

// A bundle PR mixing an add rule (codeowners) and an actionable absent
// rule (no_dependabot, gate open) renders both body sections and produces
// both commit kinds on the branch.
func TestConvergence_Bundle_AddAndRemove_RendersBothSections(t *testing.T) {
	cfg := renovateFirstPolicy()
	cfg.FileRules = append(cfg.FileRules, policy.FileRuleConfig{
		Type:     "file",
		Name:     "codeowners",
		Check:    string(policy.CheckExists),
		Paths:    []string{".github/CODEOWNERS"},
		Target:   ".github/CODEOWNERS",
		Template: "codeowners",
	})

	s := newStagedConvergenceWithPolicy(t, cfg)
	s.satisfyOnMain("renovate.json")                          // referee satisfied → gate open
	s.forbiddenOnMainAndBranch(".github/dependabot.yml", "d") // absent rule actionable
	// .github/CODEOWNERS missing on main → add rule actionable.

	s.sweep()

	if s.client.createdPR == nil {
		t.Fatal("expected a bundle PR to be created")
	}

	body := s.client.createdPRBody
	if !strings.Contains(body, "### Added Files") {
		t.Errorf("bundle body missing Added Files section:\n%s", body)
	}

	if !strings.Contains(body, "### Removed Files") {
		t.Errorf("bundle body missing Removed Files section:\n%s", body)
	}

	if !slices.Contains(s.client.createdFiles, ".github/CODEOWNERS") {
		t.Errorf("expected CODEOWNERS added, createdFiles=%v", s.client.createdFiles)
	}

	if !slices.Contains(s.client.deletedFiles, ".github/dependabot.yml") {
		t.Errorf("expected dependabot.yml deleted, deletedFiles=%v", s.client.deletedFiles)
	}
}

// TestConvergence_SatisfiedRuleNeverDeletesDefaultBranchFile is the
// INV-0014 regression test: the reported production bug, in the shape it
// was reported.
//
// A repo already has .github/CODEOWNERS and is missing dependabot. Sweep
// one opens a PR adding dependabot; the reconcile branch is cut from main
// and therefore carries CODEOWNERS, which repo-guardian did not write.
// Sweep two finds the codeowners rule satisfied and — before the fix —
// read "CODEOWNERS is on the branch" as "I put it there in an earlier
// sweep", and committed a deletion. Merging that PR would have removed
// CODEOWNERS from the default branch.
//
// This test can only fail against a fake that models branch inheritance.
// Against the pre-INV-0014 fake the branch appeared empty, nothing was
// ever a candidate for deletion, and the assertion below passed whether
// or not the bug was present.
func TestConvergence_SatisfiedRuleNeverDeletesDefaultBranchFile(t *testing.T) {
	s := newStagedConvergence(t, nil)

	// The repository owns CODEOWNERS at the rule's Target path. Only that
	// layout is affected: a root-level CODEOWNERS is not the rule's Target
	// and never becomes an orphan candidate (INV-0014 finding C).
	s.satisfyOnMain(".github/CODEOWNERS")
	s.openOurPR(1)

	s.sweep()

	if slices.Contains(s.client.deletedFiles, ".github/CODEOWNERS") {
		t.Fatalf("deleted .github/CODEOWNERS, which the default branch owns; "+
			"merging this PR would remove it from main. deletedFiles=%v", s.client.deletedFiles)
	}
}

// TestConvergence_GenuineOrphanIsStillDeleted guards the other side of the
// INV-0014 fix: the convergence behaviour IMPL-0013 Phase 3 was built for
// must survive.
//
// repo-guardian wrote .github/CODEOWNERS to the branch in an earlier sweep;
// a human then satisfied the rule by committing CODEOWNERS at the repo root
// straight to main. The branch copy is now redundant, and — crucially —
// .github/CODEOWNERS is NOT on main, so repo-guardian is the only party
// that could have placed it there. That is a real orphan and must still be
// removed, or the PR keeps proposing a duplicate CODEOWNERS forever.
//
// Without this test the INV-0014 fix could be "improved" into skipping
// orphan cleanup entirely and nothing would complain.
func TestConvergence_GenuineOrphanIsStillDeleted(t *testing.T) {
	s := newStagedConvergence(t, nil)

	// Rule satisfied by the root-level path on main...
	s.satisfyOnMain("CODEOWNERS")
	// ...while the branch still carries repo-guardian's earlier write at the
	// Target path, which main does not have.
	s.addOrphanToBranch(".github/CODEOWNERS", "sha-codeowners")
	s.openOurPR(1)

	s.sweep()

	if !slices.Contains(s.client.deletedFiles, ".github/CODEOWNERS") {
		t.Errorf("genuine orphan not cleaned up: .github/CODEOWNERS is absent from main "+
			"and was written to the branch by an earlier sweep. deletedFiles=%v", s.client.deletedFiles)
	}
}

// TestConvergence_OrphanCleanupDisabled_NoDeletion pins the INV-0014
// kill switch. It uses the genuine-orphan fixture — a file that SHOULD be
// deleted — because a test built on a file that must not be deleted would
// pass with the flag ignored entirely.
func TestConvergence_OrphanCleanupDisabled_NoDeletion(t *testing.T) {
	disabled := false

	pol := policy.BuiltinDefaults()
	pol.Guardian.DryRun = false
	pol.Guardian.OrphanCleanup = &disabled

	s := newStagedConvergenceWithPolicy(t, pol)
	s.satisfyOnMain("CODEOWNERS")
	s.addOrphanToBranch(".github/CODEOWNERS", "sha-codeowners")
	s.openOurPR(1)

	s.sweep()

	if len(s.client.deletedFiles) != 0 {
		t.Errorf("orphan_cleanup=false must delete nothing, deletedFiles=%v", s.client.deletedFiles)
	}
}

// TestConvergence_RepairsBranchThatDeletedADefaultBranchFile is the
// INV-0014 in-place repair: a PR whose branch already carries the bad
// deletion is fixed by the next sweep, rather than needing an operator to
// close and rebuild it.
//
// This is the state every affected PR is in right now: .github/CODEOWNERS
// present on main, deleted from the reconcile branch by a pre-fix sweep,
// and the rule satisfied so nothing would otherwise touch it again.
func TestConvergence_RepairsBranchThatDeletedADefaultBranchFile(t *testing.T) {
	s := newStagedConvergence(t, nil)

	// The repo owns CODEOWNERS; a pre-fix sweep deleted it from the branch.
	s.satisfyOnMain(".github/CODEOWNERS")
	s.deletedFromBranch(".github/CODEOWNERS")
	s.client.fileContents[s.org+"/"+s.repo+"/.github/CODEOWNERS"] = "* @team"
	s.openOurPR(1)

	s.sweep()

	if !slices.Contains(s.client.createdFiles, ".github/CODEOWNERS") {
		t.Errorf("branch still proposes deleting .github/CODEOWNERS, which main owns; "+
			"the open PR is not self-healing. createdFiles=%v", s.client.createdFiles)
	}
}

// conflictingPolicy pairs an add rule whose Target is a path an absent
// rule forbids. The add rule is satisfied on main (so non-actionable, and
// therefore a restore candidate) while the absent rule is actionable and
// wants that exact path gone.
func conflictingPolicy() *policy.PolicyConfig {
	return &policy.PolicyConfig{
		Guardian: policy.BuiltinDefaults().Guardian,
		FileRules: []policy.FileRuleConfig{
			{
				Type:     "file",
				Name:     "needs_dependabot",
				Check:    string(policy.CheckExists),
				Paths:    []string{".github/dependabot.yml"},
				Target:   ".github/dependabot.yml",
				Template: "codeowners",
			},
			{
				Type:   "file",
				Name:   "needs_codeowners",
				Check:  string(policy.CheckExists),
				Paths:  []string{".github/CODEOWNERS"},
				Target: ".github/CODEOWNERS",
				// Keeps the PR alive so the existing-PR path runs.
				Template: "codeowners",
			},
			{
				Type:  "file",
				Name:  "no_dependabot",
				Check: string(policy.CheckAbsent),
				Paths: []string{".github/dependabot.yml"},
			},
		},
	}
}

// TestConvergence_RepairDoesNotFightAnActionableAbsentRule pins the
// exclusion that keeps repair from flip-flopping.
//
// needs_dependabot is satisfied on main, so it is non-actionable and its
// Target is a restore candidate. no_dependabot is actionable and wants that
// same path deleted this sweep. Without the planned-deletion exclusion the
// two undo each other on every sweep forever — a loop that burns rate limit
// and never converges. The deletion must win: an actionable rule is a live
// instruction, restoration only repairs a state nothing is asking for.
func TestConvergence_RepairDoesNotFightAnActionableAbsentRule(t *testing.T) {
	s := newStagedConvergenceWithPolicy(t, conflictingPolicy())

	// dependabot.yml on main: satisfies needs_dependabot AND makes
	// no_dependabot actionable. CODEOWNERS missing keeps the PR alive.
	s.forbiddenOnMainAndBranch(".github/dependabot.yml", "dep-sha")
	s.openOurPR(2)

	s.sweep()

	if slices.Contains(s.client.createdFiles, ".github/dependabot.yml") {
		t.Errorf("restored a path an actionable absent rule is deleting this sweep; "+
			"the two will undo each other forever. createdFiles=%v", s.client.createdFiles)
	}

	if !slices.Contains(s.client.deletedFiles, ".github/dependabot.yml") {
		t.Errorf("the actionable absent rule must still delete its forbidden file, deletedFiles=%v",
			s.client.deletedFiles)
	}
}

// TestConvergence_RepairScopedToRuleTarget pins the deliberate scope
// boundary: for non-absent rules, repair restores only the rule's Target.
//
// Target is the only path cleanupOrphans ever deletes, so Target is the
// exact inverse and covers all the damage INV-0014 caused. A rule's wider
// Paths set can be missing from the branch for reasons repo-guardian did
// not cause — a human editing the branch, most obviously — and re-adding
// those would turn a repair mechanism into a resurrection mechanism that
// silently reverts deliberate edits.
//
// Here the codeowners rule is satisfied by the root-level CODEOWNERS, which
// is in Paths but is not Target, and which is absent from the branch.
// Restoring it is out of scope.
func TestConvergence_RepairScopedToRuleTarget(t *testing.T) {
	s := newStagedConvergence(t, nil)

	s.satisfyOnMain("CODEOWNERS") // in Paths, not Target -> rule satisfied
	s.deletedFromBranch("CODEOWNERS")
	s.client.fileContents[s.org+"/"+s.repo+"/CODEOWNERS"] = "* @team"
	s.openOurPR(3)

	s.sweep()

	if slices.Contains(s.client.createdFiles, "CODEOWNERS") {
		t.Errorf("restored root CODEOWNERS, which is in the rule's Paths but is not its Target; "+
			"repair must mirror cleanupOrphans, which only ever deletes Target. createdFiles=%v",
			s.client.createdFiles)
	}
}

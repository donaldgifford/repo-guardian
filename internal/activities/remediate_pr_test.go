package activities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"

	"github.com/donaldgifford/repo-guardian/internal/control"
	"github.com/donaldgifford/repo-guardian/internal/controls/builtin"
	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/github/ghfake"
	"github.com/donaldgifford/repo-guardian/internal/policy"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

const (
	noticeV1  = "notice v1\n"
	prRepoID  = 100
	prRepo    = 9
	prControl = "notice"
)

var (
	prBot   = ghfake.Actor{ID: 42, Login: "rg-remediate[bot]", Type: ghfake.TypeBot}
	alice   = ghfake.Actor{ID: 7, Login: "alice", Type: ghfake.TypeUser}
	mallory = ghfake.Actor{ID: 66, Login: "rg-remediate[bot]", Type: ghfake.TypeUser}
)

func strp(s string) *string { return &s }

// memRemediationStore is the remediation store's contract in memory:
// reservations against the cap, claims that roll back on error, the
// journal, and records fenced on the epoch. Due is driven by the test,
// the way an evaluation moves the generation.
type memRemediationStore struct {
	mu       sync.Mutex
	cap      int
	targets  map[string]*store.RemediationTarget
	rows     []*store.Remediation
	steps    map[int64][]store.RemediationStep
	events   []string
	claimed  map[string]bool
	maxSlots int
	records  int

	// failRecord fails that many RecordRun calls: a crash after the
	// run's writes and before its record.
	failRecord int
}

func newMemStore(limit int) *memRemediationStore {
	return &memRemediationStore{
		cap:     limit,
		targets: make(map[string]*store.RemediationTarget),
		steps:   make(map[int64][]store.RemediationStep),
		claimed: make(map[string]bool),
	}
}

func (m *memRemediationStore) RemediationTarget(_ context.Context, _ int64, c string) (*store.RemediationTarget, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, ok := m.targets[c]
	if !ok {
		return nil, nil //nolint:nilnil // a withdrawn control
	}

	out := *t

	return &out, nil
}

func (m *memRemediationStore) SetHold(_ context.Context, _ int64, c string, epoch int64, hold string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	t := m.targets[c]
	if t.Epoch != epoch {
		return false, nil
	}

	t.Hold, t.Due = hold, false

	return true, nil
}

func (m *memRemediationStore) LatestRemediation(_ context.Context, _ int64, c string) (*store.Remediation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i := len(m.rows) - 1; i >= 0; i-- {
		if m.rows[i].ControlID == c {
			out := *m.rows[i]

			return &out, nil
		}
	}

	return nil, nil //nolint:nilnil // no remediation yet
}

func (m *memRemediationStore) slots() int {
	n := 0

	for _, r := range m.rows {
		if r.State == store.StateOpen || r.State == store.StateReserved {
			n++
		}
	}

	return n
}

func (m *memRemediationStore) ReserveSlot(_ context.Context, in *store.SlotRequest) (*store.Remediation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, r := range m.rows {
		if r.OperationKey == in.OperationKey {
			out := *r

			return &out, nil
		}
	}

	if m.slots() >= m.cap {
		return nil, store.ErrPRCap
	}

	until := time.Now().Add(in.TTL)
	r := &store.Remediation{
		ID: int64(len(m.rows) + 1), RepositoryID: in.RepositoryID, ControlID: in.ControlID, AssignmentEpoch: in.Epoch,
		OperationKey: in.OperationKey, Kind: store.RemediationPullRequest, State: store.StateReserved, IntentRevision: in.Intent,
		BaseSHA: in.BaseSHA, Branch: in.Branch, ExpectedHead: in.ExpectedHead, ReservedUntil: &until, RemediatedGeneration: in.Generation,
	}
	m.rows = append(m.rows, r)
	m.maxSlots = max(m.maxSlots, m.slots())

	out := *r

	return &out, nil
}

type memClaim struct {
	m   *memRemediationStore
	row store.Remediation
}

func (c *memClaim) Remediation() store.Remediation { return c.row }

func (c *memClaim) Update(_ context.Context, r *store.Remediation) error {
	c.row = *r

	return nil
}

func (c *memClaim) SetIntent(_ context.Context, intent string) error {
	c.row.IntentRevision = intent

	return nil
}

func (m *memRemediationStore) ClaimOperation(ctx context.Context, key string, fn func(context.Context, store.ClaimedOperation) error) error {
	m.mu.Lock()

	var row *store.Remediation

	for _, r := range m.rows {
		if r.OperationKey == key {
			row = r
		}
	}

	switch {
	case row == nil:
		m.mu.Unlock()

		return store.ErrRemediationNotFound
	case m.claimed[key]:
		m.mu.Unlock()

		return store.ErrOperationHeld
	}

	m.claimed[key] = true
	claim := &memClaim{m: m, row: *row}
	m.mu.Unlock()

	err := fn(ctx, claim)

	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.claimed, key)

	if err == nil {
		*row = claim.row
		m.maxSlots = max(m.maxSlots, m.slots())
	}

	return err
}

func (m *memRemediationStore) AppendStep(_ context.Context, step *store.RemediationStep) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s := *step
	s.Seq = len(m.steps[step.RemediationID]) + 1
	m.steps[step.RemediationID] = append(m.steps[step.RemediationID], s)

	return s.Seq, nil
}

func (m *memRemediationStore) Steps(_ context.Context, id int64) ([]store.RemediationStep, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return slices.Clone(m.steps[id]), nil
}

func (m *memRemediationStore) AppendEvent(_ context.Context, _ int64, kind string, _ json.RawMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.events = append(m.events, kind)

	return nil
}

func (m *memRemediationStore) RecordRun(_ context.Context, run *store.RunRecord) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.failRecord > 0 {
		m.failRecord--

		return false, errors.New("crash before the record")
	}

	t := m.targets[run.ControlID]
	if t.Epoch != run.Epoch {
		return false, nil
	}

	t.Due, t.Hold = false, ""
	m.records++

	return true, nil
}

func (*memRemediationStore) TouchLastRemediation(context.Context, int64) error { return nil }

// observe is the evaluator's PR observation of the latest row: merged,
// closed by a user, or anything else it records.
func (m *memRemediationStore) observe(c, state string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i := len(m.rows) - 1; i >= 0; i-- {
		if m.rows[i].ControlID == c {
			m.rows[i].State = state

			return
		}
	}
}

// due marks c due on a new generation, the way an evaluation does.
func (m *memRemediationStore) due(c string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	t := m.targets[c]
	t.Generation++
	t.IntentRevision = fmt.Sprintf("i%d", t.Generation)
	t.Due = true
}

// latest is the notice control's latest remediation.
func (m *memRemediationStore) latest() store.Remediation {
	r, _ := m.LatestRemediation(context.Background(), prRepo, prControl)
	if r == nil {
		return store.Remediation{}
	}

	return *r
}

// writeRoot writes a policy root.
func writeRoot(t *testing.T, files map[string]string) string {
	t.Helper()

	root := t.TempDir()

	for name, body := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	return root
}

// filePolicy is one file control per slug→path, each requiring its file
// to read exactly noticeV1.
func filePolicy(t *testing.T, paths map[string]string) *policy.Snapshot {
	t.Helper()

	files := map[string]string{"templates/notice.tmpl": noticeV1}
	ids := make([]string, 0, len(paths))

	for slug, path := range paths {
		ids = append(ids, fmt.Sprintf("%q", slug+"@1"))
		files["catalogue/"+slug+".hcl"] = fmt.Sprintf(
			"control %q {\n  version  = 1\n  type     = \"file\"\n  title    = \"Notice %s\"\n  template = \"notice\"\n  path     = %q\n\n"+
				"  rule \"exact\" {\n    remediate = true\n  }\n}\n", slug, slug, path)
	}

	slices.Sort(ids)
	files["enterprise.hcl"] = fmt.Sprintf("enterprise {\n  orgs     = [\"acme\"]\n  controls = [%s]\n}\n", strings.Join(ids, ", "))

	snap, err := policy.Load(writeRoot(t, files), builtin.Registry(), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}

	return snap
}

type ghfakeRemediationRepo struct{ c *ghfake.Client }

func (r ghfakeRemediationRepo) Writer() control.Writer           { return r.c }
func (r ghfakeRemediationRepo) Reader(sha string) control.Reader { return r.c.At(sha) }
func (r ghfakeRemediationRepo) Observer() control.PRObserver     { return r.c }

type ghfakeRemediationClients struct{ repo *ghfake.Repo }

func (f ghfakeRemediationClients) Repo(context.Context, int64, string, string) (RemediationRepo, error) {
	return ghfakeRemediationRepo{f.repo.As(prBot)}, nil
}

// prHarness is one repository, the remediator over the in-memory store,
// and the GitHub fake.
type prHarness struct {
	t    *testing.T
	gh   *ghfake.GitHub
	repo *ghfake.Repo
	st   *memRemediationStore
	r    *Remediator
	runs int
}

func newPRHarness(t *testing.T, limit int, paths, files map[string]string) *prHarness {
	t.Helper()

	gh := ghfake.New()
	repo := gh.AddRepo("acme", "svc", prRepoID, files)
	st := newMemStore(limit)
	snap := filePolicy(t, paths)

	for slug := range paths {
		id := control.ID{Slug: slug, Version: 1}
		st.targets[slug] = &store.RemediationTarget{
			Org: "acme", Name: "svc", Active: true, Epoch: 1, State: assignmentActive, EffectiveMode: modeRemediate, Revision: snap.Revision(id),
			InstallationID: 7, Remediable: true, HasResult: true, ResultEpoch: 1, Generation: 1, IntentRevision: "i1",
			Status: "non_compliant", ProviderRepoID: prRepoID, ControlVersion: 1, AccountType: "organization", Due: true,
		}
	}

	r := NewRemediator(
		st,
		snap,
		ghfakeRemediationClients{repo: repo},
		ghclient.BotIdentity{ID: prBot.ID, Login: prBot.Login},
		slog.New(slog.DiscardHandler),
	)

	return &prHarness{t: t, gh: gh, repo: repo, st: st, r: r}
}

// repolicy swaps the loaded policy, and the assignments' revisions with
// it, the way a rollout does.
func (h *prHarness) repolicy(paths map[string]string) {
	h.t.Helper()

	snap := filePolicy(h.t, paths)
	h.r.policy = snap

	for slug := range paths {
		h.st.targets[slug].Revision = snap.Revision(control.ID{Slug: slug, Version: 1})
	}
}

// run runs one Remediate attempt for c, under a fresh operation key.
func (h *prHarness) run(c string) (*workflows.RemediateResult, error) {
	h.t.Helper()
	h.runs++

	return h.runKey(c, fmt.Sprintf("remediation/%d/%s/r/%d", prRepo, c, h.runs))
}

func (h *prHarness) runKey(c, key string) (*workflows.RemediateResult, error) {
	h.t.Helper()

	var suite testsuite.WorkflowTestSuite

	env := suite.NewTestActivityEnvironment()
	env.RegisterActivityWithOptions(h.r.Remediate, activity.RegisterOptions{Name: workflows.RemediateActivity})

	val, err := env.ExecuteActivity(workflows.RemediateActivity, &workflows.RemediateInput{
		RepositoryID: prRepo, Control: c, InstallationID: 7, OperationKey: key,
	})
	if err != nil {
		return nil, err
	}

	var res workflows.RemediateResult
	if err := val.Get(&res); err != nil {
		h.t.Fatal(err)
	}

	return &res, nil
}

// mustRun runs the notice control and requires kind.
func (h *prHarness) mustRun(kind workflows.RemediationKind) *workflows.RemediateResult {
	h.t.Helper()

	const c = prControl

	res, err := h.run(c)
	if err != nil {
		h.t.Fatalf("Remediate(%s) = %v", c, err)
	}

	if res.Kind != kind {
		h.t.Fatalf("Remediate(%s) = %+v, want %s", c, res, kind)
	}

	return res
}

func (h *prHarness) openPRs() []int {
	var out []int

	for n := 1; ; n++ {
		pr := h.repo.PullRequest(n)
		if pr == nil {
			return out
		}

		if pr.State == prOpen {
			out = append(out, n)
		}
	}
}

// botCommits counts the remediation commits the bot made on branch; the
// merges update-branch makes on its behalf are not remediation commits.
func (h *prHarness) botCommits(branch string) int {
	h.t.Helper()

	commits, err := h.repo.As(prBot).ListCommits(context.Background(), branch)
	if err != nil {
		h.t.Fatal(err)
	}

	n := 0

	for _, c := range commits {
		if c.AuthorID == prBot.ID && strings.HasPrefix(c.Message, "chore(repo-guardian): remediate") {
			n++
		}
	}

	return n
}

// t.Cleanup check: no test ever deletes a branch or force-pushes as the
// bot (IMPL-0030 Phase 4 success criteria).
func (h *prHarness) noDestructiveCalls() {
	h.t.Helper()

	for _, m := range []string{"DeleteBranch", "ForcePush"} {
		for _, c := range h.gh.Calls(m) {
			if c.Actor == prBot.Login {
				h.t.Errorf("the remediator called %s: %+v", m, c)
			}
		}
	}
}

func newNoticeHarness(t *testing.T) *prHarness {
	t.Helper()

	h := newPRHarness(t, 5, map[string]string{prControl: "NOTICE"}, map[string]string{"README.md": "hi\n"})
	t.Cleanup(h.noDestructiveCalls)

	return h
}

// onboard opens the notice PR and returns its number.
func (h *prHarness) onboard() int {
	h.t.Helper()
	h.mustRun(workflows.RemediationDone)

	prs := h.openPRs()
	if len(prs) != 1 {
		h.t.Fatalf("open PRs after onboarding = %v, want one", prs)
	}

	return prs[0]
}

func TestPullRequest_OnboardingOpensThePR(t *testing.T) {
	t.Parallel()

	h := newNoticeHarness(t)
	n := h.onboard()

	pr := h.repo.PullRequest(n)
	if pr.HeadRef != branchName(prControl) || pr.BaseRef != "main" || pr.AuthorID != prBot.ID {
		t.Errorf("PR = %+v, want the bot's from %s onto main", pr, branchName(prControl))
	}

	if got, _ := h.repo.File(h.repo.Head(branchName(prControl)), "NOTICE"); got != noticeV1 {
		t.Errorf("NOTICE on the branch = %q, want %q", got, noticeV1)
	}

	if h.botCommits(branchName(prControl)) != 1 {
		t.Errorf("bot commits = %d, want 1", h.botCommits(branchName(prControl)))
	}

	row := h.st.latest()
	if row.State != store.StateOpen || row.PRNumber != n || row.ReservedUntil != nil {
		t.Errorf("row = %+v, want open with #%d", row, n)
	}

	kinds := make([]string, 0, len(h.st.steps[row.ID]))
	for _, s := range h.st.steps[row.ID] {
		kinds = append(kinds, s.Kind)
	}

	if want := []string{stepBranchCreated, stepCommitted, stepPRCreated}; !slices.Equal(kinds, want) {
		t.Errorf("journal = %v, want %v", kinds, want)
	}

	if h.st.targets[prControl].Due || h.st.records != 1 {
		t.Errorf("after the run: due %v, records %d; want acknowledged once", h.st.targets[prControl].Due, h.st.records)
	}
}

func TestPullRequest_NoChangeDoesNothing(t *testing.T) {
	t.Parallel()

	h := newNoticeHarness(t)
	h.onboard()

	before := len(h.gh.Requests())

	// Not due: nothing is read or written.
	h.mustRun(workflows.RemediationDone)

	if got := len(h.gh.Requests()); got != before {
		t.Errorf("a run that was not due made %d requests", got-before)
	}

	// Due, but the PR already proposes everything: no commit, no update.
	h.st.due(prControl)
	h.mustRun(workflows.RemediationDone)

	if h.botCommits(branchName(prControl)) != 1 || len(h.gh.Calls("UpdatePullRequest")) != 0 {
		t.Errorf("a no-change run committed or updated the PR")
	}
}

func TestPullRequest_CompliantOnMainOpensNothing(t *testing.T) {
	t.Parallel()

	h := newPRHarness(t, 5, map[string]string{prControl: "NOTICE"}, map[string]string{"NOTICE": noticeV1})
	h.mustRun(workflows.RemediationDone)

	if len(h.openPRs()) != 0 || len(h.gh.Calls("CreateRef")) != 0 || h.st.records != 1 {
		t.Errorf("a compliant repository got a PR, or its generation was not acknowledged")
	}
}

func TestPullRequest_HumanEditsAndAMainMerge(t *testing.T) {
	t.Parallel()

	h := newNoticeHarness(t)
	n := h.onboard()
	branch := branchName(prControl)

	h.repo.Push(alice, branch, map[string]*string{"docs.md": strp("mine\n")}, "a human edit")
	h.repo.Push(alice, "main", map[string]*string{"go.mod": strp("module x\n")}, "main moves")
	h.st.due(prControl)

	// Behind main: update-branch, then defer to re-pin the merge.
	h.mustRun(workflows.RemediationDeferred)

	head := h.repo.Head(branch)
	if got, _ := h.repo.File(head, "docs.md"); got != "mine\n" {
		t.Errorf("the human's edit was lost: %q", got)
	}

	if got, _ := h.repo.File(head, "go.mod"); got != "module x\n" {
		t.Errorf("main was not merged in: %q", got)
	}

	if _, err := h.run(prControl); err != nil {
		t.Fatal(err)
	}

	if pr := h.repo.PullRequest(n); pr.State != prOpen || h.botCommits(branch) != 1 {
		t.Errorf("PR #%d = %+v with %d bot commits; want it open and untouched", n, pr, h.botCommits(branch))
	}
}

func TestPullRequest_MainChangesTheSameFile(t *testing.T) {
	t.Parallel()

	h := newNoticeHarness(t)
	n := h.onboard()

	h.repo.Push(alice, "main", map[string]*string{"NOTICE": strp("theirs\n")}, "main writes NOTICE")
	h.st.due(prControl)

	res := h.mustRun(workflows.RemediationHeld)
	if res.Hold != store.HoldConflict {
		t.Errorf("hold = %q, want conflict", res.Hold)
	}

	if c := h.repo.Comments(n); len(c) != 1 || !strings.HasPrefix(c[0], StickyMarker) {
		t.Errorf("comments = %q, want one sticky comment", c)
	}
}

func TestPullRequest_RetiredPathRevertedHumanEditKept(t *testing.T) {
	t.Parallel()

	for _, human := range []bool{false, true} {
		t.Run(fmt.Sprintf("human_edit=%v", human), func(t *testing.T) {
			t.Parallel()

			h := newNoticeHarness(t)
			n := h.onboard()
			branch := branchName(prControl)

			if human {
				h.repo.Push(alice, branch, map[string]*string{"NOTICE": strp("edited\n")}, "a human edit")
			}

			h.repolicy(map[string]string{prControl: "NOTICE.md"})
			h.st.due(prControl)
			h.mustRun(workflows.RemediationDone)

			head := h.repo.Head(branch)
			if got, _ := h.repo.File(head, "NOTICE.md"); got != noticeV1 {
				t.Errorf("NOTICE.md = %q, want the new file", got)
			}

			got, found := h.repo.File(head, "NOTICE")

			switch {
			case human && got != "edited\n":
				t.Errorf("the human's NOTICE = %q, %v; want it kept", got, found)
			case !human && found:
				t.Errorf("the retired NOTICE survived: %q", got)
			}

			if h.repo.PullRequest(n).State != prOpen || len(h.gh.Calls("UpdatePullRequest")) != 1 {
				t.Errorf("the PR was not refreshed in place")
			}
		})
	}
}

func TestPullRequest_HumanCloses(t *testing.T) {
	t.Parallel()

	h := newNoticeHarness(t)
	n := h.onboard()
	branch := branchName(prControl)

	h.repo.ClosePR(alice, n)
	h.st.observe(prControl, store.StateClosedByUser)
	h.st.due(prControl)

	// The bot's branch is left behind: hold stale_branch, open nothing.
	res := h.mustRun(workflows.RemediationHeld)
	if res.Hold != store.HoldStaleBranch || len(h.openPRs()) != 0 {
		t.Errorf("hold = %q, open %v; want stale_branch and no PR", res.Hold, h.openPRs())
	}

	// A human deletes it: the next generation proposes on a fresh branch.
	h.repo.DeleteBranch(alice, branch)
	h.st.due(prControl)
	h.mustRun(workflows.RemediationDone)

	if prs := h.openPRs(); len(prs) != 1 || prs[0] == n {
		t.Errorf("open PRs = %v, want one new PR", prs)
	}
}

func TestPullRequest_CompliantCloseOnlyAfterAFreshRead(t *testing.T) {
	t.Parallel()

	h := newNoticeHarness(t)
	n := h.onboard()

	// Stored status says compliant, but main is not: the PR stays open.
	h.st.targets[prControl].Status = "compliant"
	h.st.due(prControl)
	h.mustRun(workflows.RemediationDone)

	if h.repo.PullRequest(n).State != prOpen {
		t.Fatal("closed on stored status, without a fresh read")
	}

	h.repo.Push(alice, "main", map[string]*string{"NOTICE": strp(noticeV1)}, "fix on main")
	h.st.due(prControl)

	// Main now carries the PR's change: update-branch first, then close.
	if _, err := h.run(prControl); err != nil {
		t.Fatal(err)
	}

	h.st.due(prControl)
	h.mustRun(workflows.RemediationDone)

	if pr := h.repo.PullRequest(n); pr.State != "closed" || pr.Merged {
		t.Errorf("PR = %+v, want closed unmerged", pr)
	}

	if row := h.st.latest(); row.State != store.StateClosedCompliant {
		t.Errorf("row state = %q, want closed_compliant", row.State)
	}

	if _, ok, _ := h.repo.As(prBot).GetRef(context.Background(), branchName(prControl)); !ok {
		t.Error("the branch was deleted")
	}
}

func TestPullRequest_MergedIsTerminal(t *testing.T) {
	t.Parallel()

	h := newNoticeHarness(t)
	n := h.onboard()

	if err := h.repo.MergePR(alice, n); err != nil {
		t.Fatal(err)
	}

	h.st.observe(prControl, store.StateMerged)
	h.mustRun(workflows.RemediationDone)

	if len(h.openPRs()) != 0 || len(h.gh.Calls("CreatePullRequest")) != 1 {
		t.Errorf("a merged remediation was re-proposed")
	}
}

func TestPullRequest_ExpectedHeadMismatchDefers(t *testing.T) {
	t.Parallel()

	for name, inject := range map[string]func(h *prHarness){
		"concurrent push": func(h *prHarness) { h.repo.FailNext("Commit", control.ErrExpectedHeadMismatch) },
		"update-branch head moved": func(h *prHarness) {
			h.onboard()
			h.repo.Push(alice, "main", map[string]*string{"go.mod": strp("x\n")}, "main moves")
			h.st.due(prControl)
			h.repo.FailNext("UpdateBranch", control.ErrExpectedHeadMismatch)
		},
		"update-branch 202": func(h *prHarness) {
			h.onboard()
			h.repo.Push(alice, "main", map[string]*string{"go.mod": strp("x\n")}, "main moves")
			h.st.due(prControl)
			h.repo.FailNext("UpdateBranch", control.ErrUpdateBranchPending)
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h := newNoticeHarness(t)
			inject(h)
			h.mustRun(workflows.RemediationDeferred)

			// The next attempt re-pins and finishes with one PR, one commit.
			if _, err := h.run(prControl); err != nil {
				t.Fatal(err)
			}

			if prs := h.openPRs(); len(prs) != 1 || h.botCommits(branchName(prControl)) != 1 {
				t.Errorf("open %v, %d bot commits; want one of each", prs, h.botCommits(branchName(prControl)))
			}
		})
	}
}

func TestPullRequest_SpoofedAuthorAndForkRefused(t *testing.T) {
	t.Parallel()

	for name, open := range map[string]func(h *prHarness){
		"spoofed login": func(h *prHarness) {
			_ = h.repo.As(mallory).CreateRef(context.Background(), branchName(prControl), h.repo.Head("main"))
			h.repo.OpenPR(mallory, branchName(prControl), "main", ghfake.PROptions{Title: "x"})
		},
		"same-named fork": func(h *prHarness) {
			_ = h.repo.As(alice).CreateRef(context.Background(), branchName(prControl), h.repo.Head("main"))
			h.repo.OpenPR(prBot, branchName(prControl), "main", ghfake.PROptions{Title: "x", HeadRepoID: 999, ForkHeadSHA: "f00"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h := newNoticeHarness(t)
			open(h)

			res := h.mustRun(workflows.RemediationHeld)
			if res.Hold != store.HoldForeignBranch || len(h.gh.Calls("Commit")) != 0 {
				t.Errorf("hold %q, %d commits; want foreign_branch and nothing written", res.Hold, len(h.gh.Calls("Commit")))
			}
		})
	}
}

func TestPullRequest_CreateAfterCommitRecovery(t *testing.T) {
	t.Parallel()

	t.Run("create failed", func(t *testing.T) {
		t.Parallel()

		h := newNoticeHarness(t)
		h.repo.FailNext("CreatePullRequest", errors.New("connection reset"))

		if _, err := h.run(prControl); err == nil {
			t.Fatal("a failed create returned no error")
		}

		if row := h.st.latest(); row.State != store.StateReserved {
			t.Fatalf("row after the failed create = %q, want reserved", row.State)
		}

		h.mustRun(workflows.RemediationDone)

		if prs := h.openPRs(); len(prs) != 1 || h.botCommits(branchName(prControl)) != 1 {
			t.Errorf("open %v, %d bot commits; want one of each", prs, h.botCommits(branchName(prControl)))
		}
	})

	t.Run("422 adoption", func(t *testing.T) {
		t.Parallel()

		h := newNoticeHarness(t)
		h.repo.FailNext("CreatePullRequest", errors.New("connection reset"))

		if _, err := h.run(prControl); err == nil {
			t.Fatal("a failed create returned no error")
		}

		// GitHub opened it after all; the replayed create gets 422.
		if _, err := h.repo.As(prBot).CreatePullRequest(context.Background(), branchName(prControl), "main", "t", "b"); err != nil {
			t.Fatal(err)
		}

		h.repo.FailNext("CreatePullRequest", errors.New("422 a pull request already exists"))
		h.mustRun(workflows.RemediationDone)

		if prs := h.openPRs(); len(prs) != 1 || h.st.latest().PRNumber != prs[0] {
			t.Errorf("open %v, row #%d; want the one PR adopted", prs, h.st.latest().PRNumber)
		}
	})
}

// TestPullRequest_EveryControlNearTheCap is the last DESIGN-0032 bullet:
// every failing control at once against a cap of three, with crashes
// injected between reservation, commit, create and record. The open
// count never exceeds the cap and every crashed run completes.
func TestPullRequest_EveryControlNearTheCap(t *testing.T) {
	t.Parallel()

	paths := map[string]string{"a": "A", "b": "B", "c": "C", "d": "D"}
	h := newPRHarness(t, 3, paths, map[string]string{"README.md": "hi\n"})
	t.Cleanup(h.noDestructiveCalls)

	crashes := map[string]string{"a": "CreateRef", "b": "Commit", "c": "CreatePullRequest"}

	for _, c := range []string{"a", "b", "c", "d"} {
		if m, ok := crashes[c]; ok {
			h.repo.FailNext(m, errors.New("crash"))
		}

		res, err := h.run(c)

		switch {
		case err != nil:
			// The crash; the retry under the same key resumes.
			if _, err := h.runKey(c, fmt.Sprintf("remediation/%d/%s/r/%d", prRepo, c, h.runs)); err != nil {
				t.Fatalf("retry %s = %v", c, err)
			}
		case res.Kind != workflows.RemediationHeld || res.Hold != store.HoldPRCap:
			t.Errorf("%s = %+v, want held pr_cap once the cap is reached", c, res)
		}
	}

	// A crash between the writes and the record: the re-run under the
	// same key writes nothing new and records.
	h.st.failRecord = 1
	h.st.due("a")

	if _, err := h.run("a"); err == nil {
		t.Fatal("a failed record returned no error")
	}

	if _, err := h.runKey("a", fmt.Sprintf("remediation/%d/a/r/%d", prRepo, h.runs)); err != nil {
		t.Fatalf("re-run after the record crash = %v", err)
	}

	if h.st.targets["a"].Due {
		t.Error("the re-run did not record")
	}

	if h.st.maxSlots > 3 {
		t.Errorf("slots peaked at %d, above the cap of 3", h.st.maxSlots)
	}

	if prs := h.openPRs(); len(prs) != 3 {
		t.Errorf("open PRs = %v, want the cap", prs)
	}

	for _, c := range []string{"a", "b", "c"} {
		if h.botCommits(branchName(c)) != 1 {
			t.Errorf("%s: %d bot commits, want 1", c, h.botCommits(branchName(c)))
		}
	}
}

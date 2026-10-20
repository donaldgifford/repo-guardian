package ghfake

import (
	"errors"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/control"
)

var (
	bot   = Actor{ID: 42, Login: "rg[bot]", Type: TypeBot}
	alice = Actor{ID: 7, Login: "alice", Type: TypeUser}
)

func ptr(s string) *string { return &s }

func newRepo(t *testing.T) (*GitHub, *Repo) {
	t.Helper()

	g := New()
	r := g.AddRepo("acme", "svc", 100, map[string]string{"README.md": "hi\n", "go.mod": "module x\n"})

	return g, r
}

func TestCommit_ExpectedHeadOid(t *testing.T) {
	t.Parallel()

	_, r := newRepo(t)
	c := r.As(bot)
	base := r.Head("main")

	if err := c.CreateRef(t.Context(), "repo-guardian/codeowners", base); err != nil {
		t.Fatal(err)
	}

	head, err := c.Commit(t.Context(), "repo-guardian/codeowners", base,
		[]control.FileChange{{Path: "CODEOWNERS", Content: []byte("* @acme\n")}, {Path: "README.md", Delete: true}}, "add CODEOWNERS")
	if err != nil {
		t.Fatalf("Commit = %v", err)
	}

	if got, ok := r.File(head, "CODEOWNERS"); !ok || got != "* @acme\n" {
		t.Errorf("CODEOWNERS = %q, %v", got, ok)
	}

	if _, ok := r.File(head, "README.md"); ok {
		t.Error("README.md survived its delete")
	}

	// A second commit at the stale base is refused.
	if _, err := c.Commit(t.Context(), "repo-guardian/codeowners", base, nil, "again"); !errors.Is(err, control.ErrExpectedHeadMismatch) {
		t.Errorf("Commit at a stale base = %v, want ErrExpectedHeadMismatch", err)
	}

	commits, err := c.ListCommits(t.Context(), "repo-guardian/codeowners")
	if err != nil || len(commits) != 2 || commits[0].AuthorID != bot.ID || commits[0].CommitterLogin != webFlow || !commits[0].Verified {
		t.Errorf("ListCommits = %+v, %v; want the bot's verified web-flow commit first", commits, err)
	}
}

func TestPush_IsUnverifiedAndMovesTheHead(t *testing.T) {
	t.Parallel()

	_, r := newRepo(t)
	c := r.As(bot)
	base := r.Head("main")

	if err := c.CreateRef(t.Context(), "repo-guardian/x", base); err != nil {
		t.Fatal(err)
	}

	pushed := r.Push(alice, "repo-guardian/x", map[string]*string{"a.txt": ptr("a")}, "user edit")

	if _, err := c.Commit(t.Context(), "repo-guardian/x", base, nil, "bot"); !errors.Is(err, control.ErrExpectedHeadMismatch) {
		t.Errorf("Commit after a push = %v, want ErrExpectedHeadMismatch", err)
	}

	commits, _ := c.ListCommits(t.Context(), "repo-guardian/x")
	if commits[0].SHA != pushed || commits[0].Verified || commits[0].CommitterLogin != alice.Login {
		t.Errorf("pushed commit = %+v, want alice's unverified commit", commits[0])
	}
}

func TestCreateRef_FailsOnAnExistingBranch(t *testing.T) {
	t.Parallel()

	_, r := newRepo(t)

	if err := r.As(bot).CreateRef(t.Context(), "main", r.Head("main")); !errors.Is(err, ErrRefExists) {
		t.Errorf("CreateRef(main) = %v, want ErrRefExists", err)
	}
}

func TestUpdateBranch_ThreeWayMerge(t *testing.T) {
	t.Parallel()

	_, r := newRepo(t)
	c := r.As(bot)
	ctx := t.Context()

	if err := c.CreateRef(ctx, "repo-guardian/x", r.Head("main")); err != nil {
		t.Fatal(err)
	}

	head, _ := c.Commit(ctx, "repo-guardian/x", r.Head("repo-guardian/x"), []control.FileChange{{Path: "CODEOWNERS", Content: []byte("x")}}, "m")

	pr, err := c.CreatePullRequest(ctx, "repo-guardian/x", "main", "t", "b")
	if err != nil {
		t.Fatal(err)
	}

	// The base moves on a different file: a clean merge.
	r.Push(alice, "main", map[string]*string{"go.mod": ptr("module y\n")}, "bump")

	if err := c.UpdateBranch(ctx, pr.Number, "stale"); !errors.Is(err, control.ErrExpectedHeadMismatch) {
		t.Errorf("UpdateBranch at a stale head = %v, want ErrExpectedHeadMismatch", err)
	}

	if err := c.UpdateBranch(ctx, pr.Number, head); err != nil {
		t.Fatalf("UpdateBranch = %v", err)
	}

	merged := r.Head("repo-guardian/x")
	if got, _ := r.File(merged, "go.mod"); got != "module y\n" {
		t.Errorf("go.mod after update-branch = %q, want the base's", got)
	}

	if got, _ := r.File(merged, "CODEOWNERS"); got != "x" {
		t.Errorf("CODEOWNERS after update-branch = %q, want the head's", got)
	}

	// Both sides change CODEOWNERS: a conflict.
	r.Push(alice, "main", map[string]*string{"CODEOWNERS": ptr("y")}, "theirs")

	if err := c.UpdateBranch(ctx, pr.Number, merged); !errors.Is(err, control.ErrMergeConflict) {
		t.Errorf("UpdateBranch over a conflict = %v, want ErrMergeConflict", err)
	}
}

func TestPullRequests_AuthorshipAndForks(t *testing.T) {
	t.Parallel()

	_, r := newRepo(t)
	ctx := t.Context()
	c := r.As(bot)

	if err := c.CreateRef(ctx, "repo-guardian/x", r.Head("main")); err != nil {
		t.Fatal(err)
	}

	own, err := c.CreatePullRequest(ctx, "repo-guardian/x", "main", "t", "b")
	if err != nil {
		t.Fatal(err)
	}

	fork := r.OpenPR(alice, "repo-guardian/y", "main", PROptions{Title: "fork", HeadRepoID: 999, ForkHeadSHA: "f00"})

	prs, err := c.ListPullRequests(ctx, "repo-guardian/")
	if err != nil || len(prs) != 2 {
		t.Fatalf("ListPullRequests = %+v, %v", prs, err)
	}

	if prs[0].Number != own.Number || prs[0].AuthorID != bot.ID || prs[0].HeadRepoID != r.ID {
		t.Errorf("own PR = %+v, want the bot's, from this repository", prs[0])
	}

	if prs[1].Number != fork || prs[1].AuthorID != alice.ID || prs[1].HeadRepoID != 999 || prs[1].HeadSHA != "f00" {
		t.Errorf("fork PR = %+v, want alice's from repository 999", prs[1])
	}

	if err := r.MergePR(alice, own.Number); err != nil {
		t.Fatal(err)
	}

	if got := r.PullRequest(own.Number); got.State != "closed" || !got.Merged || got.ClosedAt.IsZero() {
		t.Errorf("merged PR = %+v", got)
	}
}

func TestRequestLog_RecordsEveryCall(t *testing.T) {
	t.Parallel()

	g, r := newRepo(t)
	c := r.As(bot)

	if err := c.CreateRef(t.Context(), "repo-guardian/x", r.Head("main")); err != nil {
		t.Fatal(err)
	}

	if len(g.Calls("DeleteBranch")) != 0 {
		t.Fatal("a branch delete before anyone asked for one")
	}

	r.DeleteBranch(alice, "repo-guardian/x")

	calls := g.Calls("DeleteBranch")
	if len(calls) != 1 || calls[0].Actor != alice.Login {
		t.Errorf("DeleteBranch calls = %+v, want alice's alone", calls)
	}

	if got := g.Calls("CreateRef"); len(got) != 1 || got[0].Actor != bot.Login || got[0].Repo != "acme/svc" {
		t.Errorf("CreateRef calls = %+v", got)
	}
}

func TestFailNext_InjectsOnce(t *testing.T) {
	t.Parallel()

	_, r := newRepo(t)
	boom := errors.New("boom")
	r.FailNext("GetRef", boom)

	c := r.As(bot)
	if _, _, err := c.GetRef(t.Context(), "main"); !errors.Is(err, boom) {
		t.Errorf("first GetRef = %v, want the injected error", err)
	}

	if sha, ok, err := c.GetRef(t.Context(), "main"); err != nil || !ok || sha != r.Head("main") {
		t.Errorf("second GetRef = %q, %v, %v", sha, ok, err)
	}
}

func TestReader_AtAPinnedCommit(t *testing.T) {
	t.Parallel()

	_, r := newRepo(t)
	pinned := r.Head("main")
	r.Push(alice, "main", map[string]*string{"go.mod": nil}, "rm")

	content, blob, found, err := r.As(bot).At(pinned).GetContents(t.Context(), "go.mod")
	if err != nil || !found || string(content) != "module x\n" || blob != BlobSHA("module x\n") {
		t.Errorf("GetContents at the pin = %q, %q, %v, %v", content, blob, found, err)
	}

	if _, _, found, _ := r.As(bot).GetContents(t.Context(), "go.mod"); found {
		t.Error("GetContents at the head found a deleted file")
	}
}

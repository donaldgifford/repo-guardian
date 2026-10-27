package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/donaldgifford/repo-guardian/tools/rgctl/internal/ghapi/ghapitest"
	"github.com/donaldgifford/repo-guardian/tools/rgctl/internal/prs"
)

// seedClose builds acme/web with one clean PR (#1), one edited PR (#2) and
// acme/api with a clean PR (#3).
func seedClose(srv *ghapitest.Server) {
	bot := make([]ghapitest.Commit, 0, 2)
	bot = append(bot, ghapitest.Commit{Author: testBot, Committer: testBot})
	srv.AddPR(&ghapitest.PR{Owner: "acme", Repo: "web", Number: 1, Author: testBot, HeadRef: prs.BranchAddMissingFiles, Commits: bot})
	srv.AddPR(&ghapitest.PR{
		Owner: "acme", Repo: "web", Number: 2, Author: testBot, HeadRef: prs.BranchAddCatalogInfo,
		Commits: append(bot, ghapitest.Commit{Author: "octocat", Committer: "octocat"}),
	})
	srv.AddPR(&ghapitest.PR{Owner: "acme", Repo: "api", Number: 3, Author: testBot, HeadRef: prs.BranchSetCustomProps, Commits: bot})
}

// listRecord runs prs list for org and returns the record path.
func listRecord(t *testing.T, srv *ghapitest.Server, args ...string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "record.json")
	r := runFake(t, srv, tokenEnv(), append([]string{cmdPRs, cmdList, "--out", out}, args...)...)
	if r.code != exitFound {
		t.Fatalf("prs list exit = %d, want %d\n%s", r.code, exitFound, r.stdout)
	}
	return out
}

func closeRun(t *testing.T, srv *ghapitest.Server, args ...string) (result, *prs.Record) {
	t.Helper()
	r := runFake(t, srv, tokenEnv(), append([]string{cmdPRs, cmdClose, "--format", "json"}, args...)...)
	return r, decodeRecord(t, r.stdout)
}

func results(rec *prs.Record) map[int]string {
	m := map[int]string{}
	for i := range rec.PRs {
		m[rec.PRs[i].Number] = rec.PRs[i].Result
	}
	return m
}

func TestPRsClose_DryRunWritesNothing(t *testing.T) {
	t.Parallel()
	srv := fake(t)
	seedClose(srv)
	from := listRecord(t, srv, "--org", "acme")

	r, rec := closeRun(t, srv, "--from", from, "--delete-branch")
	if r.code != exitFound { // #2 is edited and skipped
		t.Errorf("exit = %d, want %d\n%s", r.code, exitFound, r.stderr)
	}
	if got, want := results(rec), map[int]string{1: prs.ResultPlanned, 2: prs.ResultSkippedEdited, 3: prs.ResultPlanned}; !equalMaps(got, want) {
		t.Errorf("results = %v, want %v", got, want)
	}
	if w := srv.Writes(); len(w) != 0 {
		t.Errorf("dry run wrote: %+v", w)
	}
	if !strings.Contains(r.stderr, "would comment and close") || !strings.Contains(r.stderr, "--force") {
		t.Errorf("plan not logged:\n%s", r.stderr)
	}
}

func TestPRsClose_YesIsIdempotentAndOrdered(t *testing.T) {
	t.Parallel()
	srv := fake(t)
	seedClose(srv)
	from := listRecord(t, srv, "--org", "acme")

	r, rec := closeRun(t, srv, "--from", from, "--yes", "--delete-branch")
	if r.code != exitFound {
		t.Errorf("exit = %d, want %d (one edited PR skipped)\n%s", r.code, exitFound, r.stderr)
	}
	if got, want := results(
		rec,
	), map[int]string{
		1: prs.ResultBranchDeleted,
		2: prs.ResultSkippedEdited,
		3: prs.ResultBranchDeleted,
	}; !equalMaps(
		got,
		want,
	) {
		t.Errorf("results = %v, want %v", got, want)
	}
	pr, _ := srv.PR("acme", "web", 1)
	if pr.State != "closed" || len(pr.Comments) != 1 ||
		!strings.HasPrefix(pr.Comments[0].Body, prs.ClosedMarker+"\nClosed by `rgctl` on 2026-10-05") {
		t.Errorf("web#1 = state %q comments %+v, want closed with the pointer comment", pr.State, pr.Comments)
	}
	if srv.BranchExists("acme", "web", prs.BranchAddMissingFiles) {
		t.Error("web#1 branch still exists")
	}
	if pr, _ := srv.PR("acme", "web", 2); pr.State != "open" || len(pr.Comments) != 0 {
		t.Errorf("edited web#2 was touched: %+v", pr)
	}

	// Order: comment, then close, then branch, for each PR.
	var order []string
	for _, req := range srv.Writes() {
		if strings.HasPrefix(req.Path, "/repos/acme/web/") {
			order = append(order, req.Method)
		}
	}
	if strings.Join(order, ",") != "POST,PATCH,DELETE" {
		t.Errorf("web writes = %v, want POST,PATCH,DELETE", order)
	}

	// A second run posts nothing and closes nothing.
	writes := len(srv.Writes())
	_, rec = closeRun(t, srv, "--from", from, "--yes", "--delete-branch")
	if got := len(srv.Writes()) - writes; got != 0 {
		t.Errorf("second run made %d writes, want 0", got)
	}
	if got := results(rec); got[1] != prs.ResultAlreadyClosed || got[3] != prs.ResultAlreadyClosed {
		t.Errorf("second run results = %v, want already_closed", got)
	}
}

func TestPRsClose_FaultsLeaveEarlierStepsDone(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		method, prefix string
		wantComments   int
		wantState      string
		wantBranch     bool
	}{
		{
			name:         "comment fails",
			method:       http.MethodPost,
			prefix:       "/repos/acme/web/issues/1/comments",
			wantComments: 0,
			wantState:    "open",
			wantBranch:   true,
		},
		{name: "close fails", method: http.MethodPatch, prefix: "/repos/acme/web/pulls/1", wantComments: 1, wantState: "open", wantBranch: true},
		{name: "delete fails", method: http.MethodDelete, prefix: "/repos/acme/web/git/refs", wantComments: 1, wantState: "closed", wantBranch: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := fake(t)
			seedClose(srv)
			from := listRecord(t, srv, "--repo", "acme/web")
			srv.Fail(tt.method, tt.prefix, http.StatusInternalServerError, 1)

			r, rec := closeRun(t, srv, "--from", from, "--yes", "--delete-branch")
			if r.code != exitOperational || results(rec)[1] != prs.ResultError || rec.PRs[0].Error == "" {
				t.Fatalf("exit = %d results = %v, want %d with web#1 error", r.code, results(rec), exitOperational)
			}
			pr, _ := srv.PR("acme", "web", 1)
			if len(pr.Comments) != tt.wantComments || pr.State != tt.wantState ||
				srv.BranchExists("acme", "web", prs.BranchAddMissingFiles) != tt.wantBranch {
				t.Errorf(
					"after the fault: %d comments, state %q, branch %v; want %d, %q, %v",
					len(
						pr.Comments,
					),
					pr.State,
					srv.BranchExists("acme", "web", prs.BranchAddMissingFiles),
					tt.wantComments,
					tt.wantState,
					tt.wantBranch,
				)
			}

			// The re-run finishes the job without repeating a step.
			r, rec = closeRun(t, srv, "--from", from, "--yes", "--delete-branch")
			pr, _ = srv.PR("acme", "web", 1)
			if results(rec)[1] != prs.ResultBranchDeleted || len(pr.Comments) != 1 || pr.State != "closed" ||
				srv.BranchExists("acme", "web", prs.BranchAddMissingFiles) {
				t.Errorf("re-run: result %q, %d comments, state %q, branch %v; want branch_deleted, 1, closed, gone\n%s",
					results(rec)[1], len(pr.Comments), pr.State, srv.BranchExists("acme", "web", prs.BranchAddMissingFiles), r.stderr)
			}
		})
	}
}

func TestPRsClose_ForceNeverDeletesEditedBranch(t *testing.T) {
	t.Parallel()
	srv := fake(t)
	seedClose(srv)
	from := listRecord(t, srv, "--repo", "acme/web")

	r, rec := closeRun(t, srv, "--from", from, "--yes", "--force", "--delete-branch", "--comment", "Moving to v2, see JIRA-1.")
	if r.code != exitOK {
		t.Errorf("exit = %d, want %d\n%s", r.code, exitOK, r.stderr)
	}
	if got := results(rec); got[2] != prs.ResultClosed || got[1] != prs.ResultBranchDeleted {
		t.Errorf("results = %v, want edited #2 closed and clean #1 branch_deleted", got)
	}
	if !srv.BranchExists("acme", "web", prs.BranchAddCatalogInfo) {
		t.Error("--force deleted an edited PR's branch")
	}
	pr, _ := srv.PR("acme", "web", 2)
	if len(pr.Comments) != 1 || pr.Comments[0].Body != prs.ClosedMarker+"\nMoving to v2, see JIRA-1." {
		t.Errorf("comment = %+v, want the marker then the --comment text", pr.Comments)
	}
}

func TestPRsClose_StaleRecord(t *testing.T) {
	t.Parallel()
	srv := fake(t)
	seedClose(srv)
	srv.AddPR(&ghapitest.PR{Owner: "acme", Repo: "web", Number: 4, Author: testBot, HeadRef: prs.BranchSetCustomProps})
	from := listRecord(t, srv, "--repo", "acme/web")

	srv.SetPRState("acme", "web", 1, "closed")                                                 // a human closed it
	srv.AddCommit("acme", "web", 4, ghapitest.Commit{Author: "octocat", Committer: "octocat"}) // a human pushed
	r, rec := closeRun(t, srv, "--from", from, "--yes", "--delete-branch")
	if got := results(rec); got[1] != prs.ResultAlreadyClosed || got[4] != prs.ResultSkippedEdited {
		t.Errorf("results = %v, want #1 already_closed and #4 skipped_edited", got)
	}
	if r.code != exitFound {
		t.Errorf("exit = %d, want %d", r.code, exitFound)
	}
	if !srv.BranchExists("acme", "web", prs.BranchAddMissingFiles) {
		t.Error("deleted the branch of a PR a human closed")
	}
	if !strings.Contains(r.stderr, "head moved since the record was written") {
		t.Errorf("moved head not logged:\n%s", r.stderr)
	}
}

func TestPRsClose_NotOursAndMissingBranch(t *testing.T) {
	t.Parallel()
	srv := fake(t)
	seedClose(srv)
	srv.AddPR(&ghapitest.PR{Owner: "acme", Repo: "web", Number: 9, Author: "octocat", HeadRef: prs.DefaultPrefix + "lookalike"})
	rec := &prs.Record{SchemaVersion: prs.SchemaVersion, BotLogin: testBot, PRs: []prs.Entry{
		{Repository: "acme/web", Number: 1, HeadBranch: prs.BranchAddMissingFiles},
		{Repository: "acme/web", Number: 9, HeadBranch: prs.DefaultPrefix + "lookalike"},
	}}
	from := filepath.Join(t.TempDir(), "hand.json")
	if err := rec.Save(from); err != nil {
		t.Fatal(err)
	}
	srv.RemoveBranch("acme", "web", prs.BranchAddMissingFiles)

	r, out := closeRun(t, srv, "--from", from, "--yes", "--delete-branch")
	if got := results(out); got[1] != prs.ResultBranchDeleted || got[9] != prs.ResultSkippedNotOurs {
		t.Errorf("results = %v, want #1 branch_deleted (already gone is fine) and #9 skipped_not_ours", got)
	}
	if r.code != exitOK {
		t.Errorf("exit = %d, want %d\n%s", r.code, exitOK, r.stderr)
	}
	if pr, _ := srv.PR("acme", "web", 9); pr.State != "open" || len(pr.Comments) != 0 {
		t.Errorf("touched a PR that is not ours: %+v", pr)
	}
}

func TestPRsClose_Selection(t *testing.T) {
	t.Parallel()
	srv := fake(t)
	seedClose(srv)
	r, rec := closeRun(t, srv, "--repo", "acme/api")
	if r.code != exitOK || results(rec)[3] != prs.ResultPlanned || len(srv.Writes()) != 0 {
		t.Errorf("dry run: exit = %d results = %v writes = %d, want %d, planned, none", r.code, results(rec), len(srv.Writes()), exitOK)
	}
	r, rec = closeRun(t, srv, "--repo", "acme/api", "--yes")
	if r.code != exitOK || results(rec)[3] != prs.ResultClosed {
		t.Errorf("exit = %d results = %v, want #3 closed from a fresh listing", r.code, results(rec))
	}
}

func TestPRsClose_Usage(t *testing.T) {
	t.Parallel()
	bad := filepath.Join(t.TempDir(), "v9.json")
	if err := os.WriteFile(bad, []byte(`{"schema_version": 9, "prs": []}`), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		args []string
	}{
		{name: "no input", args: nil},
		{name: "both inputs", args: []string{"--from", bad, "--org", "acme"}},
		{name: "unknown schema version", args: []string{"--from", bad}},
		{name: "missing record", args: []string{"--from", filepath.Join(t.TempDir(), "none.json")}},
		{name: "bad format", args: []string{"--from", bad, "--format", "yaml"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := fake(t)
			r := runFake(t, srv, tokenEnv(), append([]string{cmdPRs, cmdClose}, tt.args...)...)
			if r.code != exitUsage {
				t.Errorf("exit = %d, want %d\n%s%s", r.code, exitUsage, r.stdout, r.stderr)
			}
			if len(srv.Requests()) != 0 {
				t.Errorf("a usage error reached GitHub: %+v", srv.Requests())
			}
		})
	}
}

func equalMaps(a, b map[int]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

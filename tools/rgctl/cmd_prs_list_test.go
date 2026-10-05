package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/donaldgifford/repo-guardian/tools/rgctl/internal/ghapi/ghapitest"
	"github.com/donaldgifford/repo-guardian/tools/rgctl/internal/prs"
)

var update = flag.Bool("update", false, "rewrite golden files under testdata")

var pullRead = regexp.MustCompile(`^/repos/[^/]+/[^/]+/pulls/\d+$`)

// seedOrg builds the acme scenario: two hand-picked PRs, a hundred clean
// ones (so search needs two pages), a closed PR the index still returns,
// and a fork.
func seedOrg(srv *ghapitest.Server) {
	srv.AddPR(&ghapitest.PR{
		Owner:    "acme",
		Repo:     "web",
		Number:   1,
		Author:   testBot,
		HeadRef:  prs.BranchAddMissingFiles,
		Title:    "chore: add missing repo configuration files",
		Commits:  []ghapitest.Commit{{Author: testBot, Committer: testBot}},
		Comments: []ghapitest.Comment{{Author: testBot, Body: prs.ReconcileLogMarker + "\n| rule | status |"}},
	})
	edited := make([]ghapitest.Commit, 0, 101)
	for range 100 {
		edited = append(edited, ghapitest.Commit{Author: testBot, Committer: testBot})
	}
	edited = append(edited, ghapitest.Commit{Author: "octocat", Committer: "web-flow"}) // page two
	srv.AddPR(&ghapitest.PR{Owner: "acme", Repo: "web", Number: 2, Author: testBot, HeadRef: prs.BranchAddCatalogInfo, Commits: edited})
	for n := range 100 {
		srv.AddPR(&ghapitest.PR{Owner: "acme", Repo: "bulk", Number: 1000 + n, Author: testBot, HeadRef: prs.BranchAddMissingFiles})
	}
	srv.AddPR(
		&ghapitest.PR{
			Owner:        "acme",
			Repo:         "api",
			Number:       3,
			Author:       testBot,
			HeadRef:      prs.BranchAddMissingFiles,
			State:        "closed",
			StaleInIndex: true,
		},
	)
	srv.AddPR(&ghapitest.PR{Owner: "acme", Repo: "api", Number: 5, Author: testBot, HeadRef: prs.BranchAddMissingFiles, HeadRepoID: 999})
	srv.AddPR(&ghapitest.PR{Owner: "acme", Repo: "api", Number: 6, Author: "octocat", HeadRef: prs.DefaultPrefix + "lookalike"})
}

func decodeRecord(t *testing.T, s string) *prs.Record {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(s))
	dec.DisallowUnknownFields()
	var rec prs.Record
	if err := dec.Decode(&rec); err != nil {
		t.Fatalf("stdout is not one JSON record: %v\n%s", err, s)
	}
	if dec.More() {
		t.Fatalf("stdout has more than the record:\n%s", s)
	}
	return &rec
}

func TestPRsList_Org(t *testing.T) {
	t.Parallel()
	srv := fake(t)
	seedOrg(srv)

	r := runFake(t, srv, tokenEnv(), "prs", "list", "--org", "acme", "--format", "json")
	if r.code != exitFound {
		t.Fatalf("exit = %d, want %d\nstderr:\n%s", r.code, exitFound, r.stderr)
	}
	rec := decodeRecord(t, r.stdout) // the stream rule: stdout is only the record
	want := prs.Summary{Hits: 104, Verified: 104, Dropped: 2, Clean: 101, Edited: 1}
	if rec.Summary != want {
		t.Errorf("summary = %+v, want %+v", rec.Summary, want)
	}
	if len(rec.PRs) != 102 || rec.BotLogin != testBot || rec.SchemaVersion != prs.SchemaVersion {
		t.Errorf("record has %d PRs, bot %q, schema %d", len(rec.PRs), rec.BotLogin, rec.SchemaVersion)
	}
	byNum := map[int]prs.Entry{}
	for _, e := range rec.PRs {
		byNum[e.Number] = e
	}
	if e := byNum[1]; e.Edited || !e.ReconcileLog {
		t.Errorf("web#1 = %+v, want clean with the reconcile log", e)
	}
	if e := byNum[2]; !e.Edited || !slices.Equal(e.EditedBy, []string{"octocat", "web-flow"}) || e.ReconcileLog {
		t.Errorf("web#2 = %+v, want edited by octocat and web-flow", e)
	}
	for _, n := range []int{3, 5, 6} {
		if _, ok := byNum[n]; ok {
			t.Errorf("PR #%d is in the record; stale, fork and human PRs must be dropped", n)
		}
	}

	// Search is never the last word: one pull read per hit.
	reads := 0
	for _, req := range srv.Requests() {
		if req.Method == http.MethodGet && pullRead.MatchString(req.Path) {
			reads++
		}
	}
	if reads != 104 {
		t.Errorf("read %d pull requests back, want one per hit (104)", reads)
	}
	if srv.Count(http.MethodGet, "/search/issues") != 2 {
		t.Errorf("search pages = %d, want 2", srv.Count(http.MethodGet, "/search/issues"))
	}
	if srv.Count(http.MethodGet, "/repos/acme/web/pulls/2/commits") != 2 {
		t.Error("the human commit on page two was not read")
	}
	if w := srv.Writes(); len(w) != 0 {
		t.Errorf("list wrote to GitHub: %+v", w)
	}
	if !strings.Contains(r.stderr, "org acme: 2 pages, 104 hits, 104 verified, 2 dropped") {
		t.Errorf("progress line missing from the log (stderr):\n%s", r.stderr)
	}
}

func TestPRsList_Exhaustive(t *testing.T) {
	t.Parallel()
	srv := fake(t)
	seedOrg(srv)
	srv.Repos = []ghapitest.Repo{{Owner: "acme", Name: "web"}, {Owner: "acme", Name: "api"}, {Owner: "acme", Name: "bulk"}}

	r := runFake(t, srv, tokenEnv(), "prs", "list", "--org", "acme", "--exhaustive", "--format", "json")
	if r.code != exitFound {
		t.Fatalf("exit = %d, want %d\n%s", r.code, exitFound, r.stderr)
	}
	rec := decodeRecord(t, r.stdout)
	// Open PRs only: web 1-2, bulk 100, api 5 (fork) and 6 (human) dropped.
	want := prs.Summary{Hits: 104, Verified: 104, Dropped: 2, Clean: 101, Edited: 1}
	if rec.Summary != want || !rec.Selection.Exhaustive {
		t.Errorf("summary = %+v exhaustive=%v, want %+v", rec.Summary, rec.Selection.Exhaustive, want)
	}
	if srv.Count(http.MethodGet, "/search/issues") != 0 {
		t.Error("--exhaustive searched")
	}
}

func TestPRsList_RepoGolden(t *testing.T) {
	t.Parallel()
	srv := fake(t)
	seedOrg(srv)

	r := runFake(t, srv, tokenEnv(), "prs", "list", "--repo", "acme/web", "--format", "json")
	if r.code != exitFound {
		t.Fatalf("exit = %d, want %d\n%s", r.code, exitFound, r.stderr)
	}
	golden := filepath.Join("testdata", "list-repo.golden.json")
	if *update {
		if err := os.WriteFile(golden, []byte(r.stdout), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run go test -update): %v", err)
	}
	if !bytes.Equal([]byte(r.stdout), want) {
		t.Errorf("record differs from %s; run go test -update and review\n%s", golden, r.stdout)
	}
}

func TestPRsList_Table(t *testing.T) {
	t.Parallel()
	srv := fake(t)
	seedOrg(srv)
	out := filepath.Join(t.TempDir(), "acme.json")

	r := runFake(t, srv, tokenEnv(), "prs", "list", "--repo", "acme/web", "--out", out)
	if r.code != exitFound {
		t.Fatalf("exit = %d, want %d\n%s", r.code, exitFound, r.stderr)
	}
	for _, want := range []string{"REPOSITORY", "acme/web", "#2", prs.BranchAddCatalogInfo, "34d", "yes (octocat, web-flow)", "record written"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, r.stdout)
		}
	}
	if r.stderr != "" {
		t.Errorf("stderr = %q, want the log on stdout beside the table", r.stderr)
	}
	if rec, err := prs.Load(out); err != nil || len(rec.PRs) != 2 {
		t.Errorf("--out record = %+v, %v, want two PRs", rec, err)
	}
}

func TestPRsList_Config(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	scoped := filepath.Join(dir, "scoped.hcl")
	legacy := filepath.Join(dir, "legacy.hcl")
	if err := os.WriteFile(scoped, []byte("scope {\n  orgs = [\"acme\"]\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("guardian {\n  dry_run = true\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("scoped orgs", func(t *testing.T) {
		t.Parallel()
		srv := fake(t)
		seedOrg(srv)
		r := runFake(t, srv, tokenEnv(), "prs", "list", "--config", scoped, "--format", "json")
		if r.code != exitFound || !slices.Equal(decodeRecord(t, r.stdout).Selection.Orgs, []string{"acme"}) {
			t.Errorf("exit = %d, want %d scanning acme\n%s", r.code, exitFound, r.stderr)
		}
	})
	t.Run("legacy mode under token auth", func(t *testing.T) {
		t.Parallel()
		srv := fake(t)
		r := runFake(t, srv, tokenEnv(), "prs", "list", "--config", legacy)
		if r.code != exitUsage || !strings.Contains(r.stdout+r.stderr, "needs App credentials") {
			t.Errorf("exit = %d, want %d with the App-credentials message\n%s%s", r.code, exitUsage, r.stdout, r.stderr)
		}
	})
	t.Run("legacy mode under App auth scans every installation", func(t *testing.T) {
		t.Parallel()
		srv := fake(t)
		seedOrg(srv)
		env := map[string]string{envAppID: "7", envKeyFile: appKey(t)}
		r := runFake(t, srv, env, "prs", "list", "--config", legacy, "--format", "json")
		rec := decodeRecord(t, r.stdout)
		orgs := slices.Sorted(slices.Values(rec.Selection.Orgs))
		if r.code != exitFound || !slices.Equal(orgs, []string{"acme", "globex"}) {
			t.Errorf("exit = %d orgs = %v, want %d over acme and globex\n%s", r.code, orgs, exitFound, r.stderr)
		}
	})
}

func TestPRsList_IncompleteWarnsOnce(t *testing.T) {
	t.Parallel()
	srv := fake(t)
	seedOrg(srv)
	srv.SearchIncomplete = true
	r := runFake(t, srv, tokenEnv(), "prs", "list", "--org", "acme")
	if got := strings.Count(r.stdout, "--exhaustive"); got != 1 {
		t.Errorf("--exhaustive named %d times, want once:\n%s", got, r.stdout)
	}
	if r.code != exitFound {
		t.Errorf("exit = %d, want %d", r.code, exitFound)
	}
}

func TestPRsList_ExitCodes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		env   map[string]string
		args  []string
		fault bool
		want  int
	}{
		{name: "none found", env: tokenEnv(), args: []string{"--org", "globex"}, want: exitOK},
		{name: "found", env: tokenEnv(), args: []string{"--org", "acme"}, want: exitFound},
		{name: "no selection", env: tokenEnv(), want: exitUsage},
		{name: "two selections", env: tokenEnv(), args: []string{"--org", "acme", "--repo", "acme/web"}, want: exitUsage},
		{name: "no credentials", env: map[string]string{}, args: []string{"--org", "acme"}, want: exitUsage},
		{name: "token without bot login", env: map[string]string{envToken: testToken}, args: []string{"--org", "acme"}, want: exitUsage},
		{name: "bad app id", env: map[string]string{envAppID: "seven", envKeyFile: "k.pem"}, args: []string{"--org", "acme"}, want: exitUsage},
		{name: "search fails", env: tokenEnv(), args: []string{"--org", "acme"}, fault: true, want: exitOperational},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := fake(t)
			seedOrg(srv)
			if tt.fault {
				srv.Fail(http.MethodGet, "/search/issues", http.StatusInternalServerError, -1)
			}
			r := runFake(t, srv, tt.env, append([]string{"prs", "list"}, tt.args...)...)
			if r.code != tt.want {
				t.Errorf("exit = %d, want %d\n%s%s", r.code, tt.want, r.stdout, r.stderr)
			}
		})
	}
}

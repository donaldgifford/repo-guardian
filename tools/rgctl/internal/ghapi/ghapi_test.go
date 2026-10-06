package ghapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/tools/rgctl/internal/ghapi/ghapitest"
)

const (
	testSlug  = "repo-guardian"
	testBot   = "repo-guardian[bot]"
	testOrg   = "acme"
	testToken = "ghp_operator"
	ua        = "rgctl/test"
	prefix    = "repo-guardian/"
)

// keyFile writes a throwaway RSA key for App auth.
func keyFile(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	path := filepath.Join(t.TempDir(), "app.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return path
}

func newServer(t *testing.T) *ghapitest.Server {
	t.Helper()
	srv := ghapitest.New()
	t.Cleanup(srv.Close)
	srv.AppID = 7
	srv.Slug = testSlug
	srv.Installations = []ghapitest.Installation{{ID: 41, Account: testOrg}, {ID: 42, Account: "globex"}}
	return srv
}

// fakeClock records sleeps and advances instead of blocking.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	sleeps []time.Duration
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Sleep(_ context.Context, d time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sleeps = append(c.sleeps, d)
	c.now = c.now.Add(d)
	return nil
}

func appClient(t *testing.T, srv *ghapitest.Server, opts Options) *Client {
	t.Helper()
	opts.UserAgent = ua
	c, err := New(context.Background(), &Credentials{AppID: srv.AppID, PrivateKeyFile: keyFile(t), Host: srv.URL}, opts)
	if err != nil {
		t.Fatalf("New(app) error = %v", err)
	}
	return c
}

func tokenClient(t *testing.T, srv *ghapitest.Server, opts Options) *Client {
	t.Helper()
	opts.UserAgent = ua
	c, err := New(context.Background(), &Credentials{Token: testToken, BotLogin: testBot, Host: srv.URL}, opts)
	if err != nil {
		t.Fatalf("New(token) error = %v", err)
	}
	return c
}

func TestResolve(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		creds    Credentials
		wantMode Mode
		wantWarn bool
		wantErr  error
	}{
		{name: "app only", creds: Credentials{AppID: 1, PrivateKeyFile: "k.pem"}, wantMode: ModeApp},
		{name: "token only", creds: Credentials{Token: "t", BotLogin: testBot}, wantMode: ModeToken},
		{name: "both", creds: Credentials{AppID: 1, PrivateKeyFile: "k.pem", Token: "t"}, wantMode: ModeApp, wantWarn: true},
		{name: "neither", creds: Credentials{}, wantErr: ErrNoCredentials},
		{name: "app without key", creds: Credentials{AppID: 1}, wantErr: ErrAppIDWithoutKey},
		{name: "key without app", creds: Credentials{PrivateKeyFile: "k.pem"}, wantErr: ErrKeyWithoutAppID},
		{name: "token without bot login", creds: Credentials{Token: "t"}, wantErr: ErrTokenNeedsLogin},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mode, warn, err := tt.creds.Resolve()
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Resolve() error = %v, want %v", err, tt.wantErr)
			}
			if mode != tt.wantMode || (warn != "") != tt.wantWarn {
				t.Errorf("Resolve() = %v, %q, want %v, warning %v", mode, warn, tt.wantMode, tt.wantWarn)
			}
		})
	}
}

func TestAPIBase(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"":                           "",
		"github.com":                 "",
		"https://github.com/":        "",
		"ghe.example.com":            "https://ghe.example.com/",
		"https://ghe.example.com":    "https://ghe.example.com/",
		"http://127.0.0.1:8080/base": "http://127.0.0.1:8080/base/",
	}
	for in, want := range tests {
		got, err := apiBase(in)
		if err != nil || got != want {
			t.Errorf("apiBase(%q) = %q, %v, want %q", in, got, err, want)
		}
	}
}

func TestNew_AppMode(t *testing.T) {
	t.Parallel()
	srv := newServer(t)
	srv.AddPR(&ghapitest.PR{Owner: testOrg, Repo: "web", Number: 3, Author: testBot, HeadRef: prefix + "add-missing-files"})
	c := appClient(t, srv, Options{})

	if c.Mode() != ModeApp || c.BotLogin() != testBot {
		t.Fatalf("client = %v %q, want app %q", c.Mode(), c.BotLogin(), testBot)
	}
	if _, err := c.GetPR(context.Background(), "acme/web", 3); err != nil {
		t.Fatalf("GetPR() error = %v", err)
	}
	reqs := srv.Requests()
	if reqs[0].Path != "/app" || !strings.HasPrefix(reqs[0].Auth, "Bearer ") || strings.Count(reqs[0].Auth, ".") != 2 {
		t.Errorf("first request = %+v, want GET /app with a JWT", reqs[0])
	}
	if srv.Count(http.MethodPost, "/app/installations/41/access_tokens") != 1 {
		t.Errorf("installation token for acme minted %d times, want 1", srv.Count(http.MethodPost, "/app/installations/41/access_tokens"))
	}
	last := reqs[len(reqs)-1]
	if last.Path != "/repos/acme/web/pulls/3" || last.Auth != "token "+ghapitest.InstallationToken(41) {
		t.Errorf("GetPR request = %+v, want the acme installation token", last)
	}
	for _, r := range reqs {
		if !strings.HasPrefix(r.UserAgent, ua) {
			t.Errorf("request %s %s User-Agent = %q, want %q", r.Method, r.Path, r.UserAgent, ua)
		}
	}
	ins, err := c.Installations()
	if err != nil || len(ins) != 2 {
		t.Errorf("Installations() = %v, %v, want two", ins, err)
	}
	if _, err := c.GetPR(context.Background(), "initech/x", 1); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("GetPR(uninstalled org) error = %v, want ErrNotInstalled", err)
	}
}

func TestNew_TokenMode(t *testing.T) {
	t.Parallel()
	srv := newServer(t)
	srv.AddPR(&ghapitest.PR{Owner: testOrg, Repo: "web", Number: 3, Author: testBot, HeadRef: prefix + "x"})
	c := tokenClient(t, srv, Options{})
	if c.Mode() != ModeToken || c.BotLogin() != testBot {
		t.Fatalf("client = %v %q, want token %q", c.Mode(), c.BotLogin(), testBot)
	}
	if _, err := c.GetPR(context.Background(), "acme/web", 3); err != nil {
		t.Fatalf("GetPR() error = %v", err)
	}
	reqs := srv.Requests()
	if len(reqs) != 1 || !strings.HasSuffix(reqs[0].Auth, testToken) {
		t.Errorf("requests = %+v, want one GetPR carrying the token", reqs)
	}
	if _, err := c.Installations(); err == nil {
		t.Error("Installations() under token auth error = nil, want an error")
	}
}

func TestNew_BadKey(t *testing.T) {
	t.Parallel()
	srv := newServer(t)
	_, err := New(context.Background(), &Credentials{AppID: 7, PrivateKeyFile: filepath.Join(t.TempDir(), "missing.pem"), Host: srv.URL}, Options{})
	if err == nil {
		t.Fatal("New(missing key) error = nil, want an error")
	}
}

func TestSearchOpenPRs(t *testing.T) {
	t.Parallel()
	srv := newServer(t)
	for n := 1; n <= 120; n++ {
		srv.AddPR(&ghapitest.PR{Owner: testOrg, Repo: "web", Number: n, Author: testBot, HeadRef: prefix + "add-missing-files"})
	}
	srv.AddPR(&ghapitest.PR{Owner: testOrg, Repo: "web", Number: 500, Author: "octocat", HeadRef: prefix + "add-missing-files"})
	srv.AddPR(&ghapitest.PR{Owner: testOrg, Repo: "web", Number: 501, Author: testBot, HeadRef: "feature/x"})
	srv.AddPR(&ghapitest.PR{Owner: testOrg, Repo: "web", Number: 502, Author: testBot, HeadRef: prefix + "x", State: "closed", StaleInIndex: true})
	clock := &fakeClock{now: time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)}
	c := tokenClient(t, srv, Options{Now: clock.Now, Sleep: clock.Sleep})

	hits, status, err := c.SearchOpenPRs(context.Background(), testOrg, prefix)
	if err != nil {
		t.Fatalf("SearchOpenPRs() error = %v", err)
	}
	if len(hits) != 121 || status.Pages != 2 || status.Total != 121 || status.Incomplete || status.Capped {
		t.Errorf("SearchOpenPRs() = %d hits, %+v; want 121 hits (120 + one stale), 2 pages, complete", len(hits), status)
	}
	if hits[0] != (SearchHit{Repo: "acme/web", Number: 1}) {
		t.Errorf("hits[0] = %+v, want acme/web#1", hits[0])
	}
	q := srv.Requests()[0].Query
	for _, want := range []string{"is%3Apr", "is%3Aopen", "org%3Aacme", "author%3Aapp%2Frepo-guardian", "head%3Arepo-guardian%2F"} {
		if !strings.Contains(q, want) {
			t.Errorf("search query %q lacks %q", q, want)
		}
	}
	// The pacer slept once, before page two, for the full spacing.
	if !slices.Equal(clock.sleeps, []time.Duration{searchSpacing}) {
		t.Errorf("sleeps = %v, want [%v]", clock.sleeps, searchSpacing)
	}
}

func TestSearchOpenPRs_IncompleteAndCapped(t *testing.T) {
	t.Parallel()
	srv := newServer(t)
	srv.AddPR(&ghapitest.PR{Owner: testOrg, Repo: "web", Number: 1, Author: testBot, HeadRef: prefix + "x"})
	srv.SearchIncomplete = true
	srv.SearchTotal = 1500
	c := tokenClient(t, srv, Options{})
	_, status, err := c.SearchOpenPRs(context.Background(), testOrg, prefix)
	if err != nil {
		t.Fatalf("SearchOpenPRs() error = %v", err)
	}
	if !status.Incomplete || !status.Capped {
		t.Errorf("status = %+v, want incomplete and capped", status)
	}
}

func TestReadsAndWrites(t *testing.T) {
	t.Parallel()
	srv := newServer(t)
	commits := make([]ghapitest.Commit, 150)
	for i := range commits {
		commits[i] = ghapitest.Commit{Author: testBot, Committer: testBot}
	}
	commits[0] = ghapitest.Commit{Author: "", Committer: "octocat"}
	srv.AddPR(
		&ghapitest.PR{Owner: testOrg, Repo: "web", Number: 9, Author: testBot, HeadRef: prefix + "add-missing-files", Title: "t", Commits: commits},
	)
	srv.AddPR(&ghapitest.PR{Owner: testOrg, Repo: "web", Number: 10, Author: "octocat", HeadRef: "fix", State: "closed"})
	srv.Repos = []ghapitest.Repo{{Owner: testOrg, Name: "web"}, {Owner: testOrg, Name: "api"}, {Owner: "globex", Name: "other"}}
	ctx := context.Background()
	c := appClient(t, srv, Options{})

	pr, err := c.GetPR(ctx, "acme/web", 9)
	if err != nil || pr.Author != testBot || pr.HeadRef != prefix+"add-missing-files" || pr.State != "open" || pr.HeadRepoID != pr.BaseRepoID ||
		pr.CreatedAt.IsZero() {
		t.Errorf("GetPR() = %+v, %v", pr, err)
	}

	open, err := c.ListOpenPRs(ctx, "acme/web")
	if err != nil || len(open) != 1 || open[0].Number != 9 {
		t.Errorf("ListOpenPRs() = %+v, %v, want only #9", open, err)
	}

	var seen []Commit
	if err := c.ListPRCommits(ctx, "acme/web", 9, func(cm Commit) bool { seen = append(seen, cm); return false }); err != nil {
		t.Fatalf("ListPRCommits() error = %v", err)
	}
	if len(seen) != 1 || seen[0] != (Commit{CommitterLogin: "octocat"}) || srv.Count(http.MethodGet, "/repos/acme/web/pulls/9/commits") != 1 {
		t.Errorf(
			"ListPRCommits stopped after %+v and %d pages, want one commit and one page",
			seen,
			srv.Count(http.MethodGet, "/repos/acme/web/pulls/9/commits"),
		)
	}
	var all int
	if err := c.ListPRCommits(ctx, "acme/web", 9, func(Commit) bool { all++; return true }); err != nil || all != 150 {
		t.Errorf("ListPRCommits(all) visited %d, %v, want 150", all, err)
	}

	repos, err := c.ListRepos(ctx, testOrg)
	if err != nil || !slices.Equal(repos, []string{"acme/web", "acme/api"}) {
		t.Errorf("ListRepos(app) = %v, %v", repos, err)
	}
	if srv.Count(http.MethodGet, "/installation/repositories") != 1 {
		t.Error("ListRepos under App auth did not use the installation's repository list")
	}

	if err := c.CreateComment(ctx, "acme/web", 9, "hello"); err != nil {
		t.Fatalf("CreateComment() error = %v", err)
	}
	comments, err := c.ListComments(ctx, "acme/web", 9)
	if err != nil || len(comments) != 1 || comments[0].Body != "hello" || comments[0].Author != testBot {
		t.Errorf("ListComments() = %+v, %v, want the comment just posted", comments, err)
	}

	if err := c.ClosePR(ctx, "acme/web", 9); err != nil {
		t.Fatalf("ClosePR() error = %v", err)
	}
	if pr, _ := c.GetPR(ctx, "acme/web", 9); pr.State != "closed" {
		t.Errorf("after ClosePR state = %q, want closed", pr.State)
	}

	if ok, err := c.BranchExists(ctx, "acme/web", prefix+"add-missing-files"); err != nil || !ok {
		t.Errorf("BranchExists(before delete) = %v, %v, want true", ok, err)
	}
	for range 2 {
		if err := c.DeleteBranch(ctx, "acme/web", prefix+"add-missing-files"); err != nil {
			t.Errorf("DeleteBranch() error = %v, want nil both times", err)
		}
	}
	if srv.BranchExists(testOrg, "web", prefix+"add-missing-files") {
		t.Error("branch still exists after DeleteBranch")
	}
	if ok, err := c.BranchExists(ctx, "acme/web", prefix+"add-missing-files"); err != nil || ok {
		t.Errorf("BranchExists(after delete) = %v, %v, want false", ok, err)
	}
}

func TestListRepos_TokenMode(t *testing.T) {
	t.Parallel()
	srv := newServer(t)
	srv.Repos = []ghapitest.Repo{{Owner: testOrg, Name: "web"}}
	c := tokenClient(t, srv, Options{})
	repos, err := c.ListRepos(context.Background(), testOrg)
	if err != nil || !slices.Equal(repos, []string{"acme/web"}) || srv.Count(http.MethodGet, "/orgs/acme/repos") != 1 {
		t.Errorf("ListRepos(token) = %v, %v, want the org listing", repos, err)
	}
}

func TestSecondaryRateLimit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		times   int
		wantErr bool
	}{
		{times: 1, wantErr: false},
		{times: 2, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%d", tt.times), func(t *testing.T) {
			t.Parallel()
			srv := newServer(t)
			srv.AddPR(&ghapitest.PR{Owner: testOrg, Repo: "web", Number: 1, Author: testBot, HeadRef: prefix + "x"})
			srv.SecondaryLimit(http.MethodGet, "/repos/acme/web/pulls/1", tt.times)
			var logs bytes.Buffer
			// Real sleep: go-github blocks client-side until Retry-After
			// passes, so a fake clock would only see that block.
			c := tokenClient(t, srv, Options{Logger: slog.New(slog.NewTextHandler(&logs, nil))})
			_, err := c.GetPR(context.Background(), "acme/web", 1)
			if (err != nil) != tt.wantErr {
				t.Fatalf("GetPR() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got := strings.Count(logs.String(), "retrying once"); got != 1 {
				t.Errorf("logged %d retries, want 1:\n%s", got, logs.String())
			}
		})
	}
}

func TestRateLimitWait(t *testing.T) {
	t.Parallel()
	if _, ok := rateLimitWait(errors.New("boom"), time.Now()); ok {
		t.Error("rateLimitWait(plain error) = limited, want not")
	}
	if _, ok := rateLimitWait(nil, time.Now()); ok {
		t.Error("rateLimitWait(nil) = limited, want not")
	}
}

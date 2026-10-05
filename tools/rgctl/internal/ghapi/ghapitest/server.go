// Package ghapitest is a stateful fake of the GitHub REST endpoints rgctl
// uses, served by net/http/httptest under the GitHub Enterprise Server path
// prefix /api/v3.
//
// It is stateful on purpose (the mock-fidelity rule in CLAUDE.md): a comment
// it creates is returned by the next list, a pull request it closes reads as
// closed, a branch it deletes answers 422 afterwards. Every request is
// recorded so tests can count calls, and faults can be injected per route.
package ghapitest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"
)

// APIPrefix is the path every route is served under.
const APIPrefix = "/api/v3"

const (
	stateOpen  = "open"
	keyMessage = "message"
	keyLogin   = "login"
	keyUser    = "user"
)

// InstallationToken is the token the fake mints for installation id.
func InstallationToken(id int64) string { return fmt.Sprintf("ghs_inst_%d", id) }

// Server is the fake. Seed its fields before the first request; read them
// back through the accessor methods.
type Server struct {
	*httptest.Server

	mu sync.Mutex

	// AppID and Slug answer GET /app.
	AppID int64
	Slug  string
	// Installations of the App, by account.
	Installations []Installation
	// Repos per owner, as served to both listing endpoints.
	Repos []Repo
	// SearchTotal overrides total_count when non-zero.
	SearchTotal int
	// SearchIncomplete sets incomplete_results on every search page.
	SearchIncomplete bool

	prs       []*PR
	refs      map[string]bool // owner/name:branch
	commentID int64
	requests  []Request
	faults    []*fault
}

// Installation is one App installation.
type Installation struct {
	ID      int64
	Account string
}

// Repo is one repository.
type Repo struct {
	Owner string
	Name  string
}

// PR is one pull request.
type PR struct {
	Owner, Repo string
	Number      int
	// State is "open" or "closed".
	State  string
	Title  string
	Author string
	// HeadRef is the head branch; HeadRepoID differs from BaseRepoID for a
	// fork.
	HeadRef    string
	HeadSHA    string
	HeadRepoID int64
	BaseRepoID int64
	CreatedAt  time.Time
	UpdatedAt  time.Time
	// StaleInIndex keeps the PR in search results after it closes, the way
	// the search index lags.
	StaleInIndex bool
	Commits      []Commit
	Comments     []Comment
}

// Commit is one commit; an empty login is an unresolved account.
type Commit struct {
	Author, Committer string
}

// Comment is one issue comment.
type Comment struct {
	ID     int64
	Author string
	Body   string
}

// Request is one recorded request. Path excludes [APIPrefix].
type Request struct {
	Method string
	Path   string
	Query  string
	Auth   string
	// UserAgent is the request's User-Agent header.
	UserAgent string
}

type fault struct {
	method, pathPrefix string
	status             int
	retryAfter         string
	remaining          int // < 0 means forever
}

// New starts a fake. Close it with t.Cleanup(s.Close).
func New() *Server {
	s := &Server{refs: make(map[string]bool)}
	mux := http.NewServeMux()
	p := APIPrefix
	mux.HandleFunc("GET "+p+"/app", s.getApp)
	mux.HandleFunc("GET "+p+"/app/installations", s.listInstallations)
	mux.HandleFunc("POST "+p+"/app/installations/{id}/access_tokens", s.createToken)
	mux.HandleFunc("GET "+p+"/installation/repositories", s.installationRepos)
	mux.HandleFunc("GET "+p+"/orgs/{org}/repos", s.orgRepos)
	mux.HandleFunc("GET "+p+"/search/issues", s.search)
	mux.HandleFunc("GET "+p+"/repos/{owner}/{repo}/pulls", s.listPulls)
	mux.HandleFunc("GET "+p+"/repos/{owner}/{repo}/pulls/{number}", s.getPull)
	mux.HandleFunc("PATCH "+p+"/repos/{owner}/{repo}/pulls/{number}", s.editPull)
	mux.HandleFunc("GET "+p+"/repos/{owner}/{repo}/pulls/{number}/commits", s.listCommits)
	mux.HandleFunc("GET "+p+"/repos/{owner}/{repo}/issues/{number}/comments", s.listComments)
	mux.HandleFunc("POST "+p+"/repos/{owner}/{repo}/issues/{number}/comments", s.createComment)
	mux.HandleFunc("DELETE "+p+"/repos/{owner}/{repo}/git/refs/heads/{branch...}", s.deleteRef)
	s.Server = httptest.NewServer(s.record(mux))
	return s
}

// AddPR seeds a pull request and its head branch, filling defaults: open,
// head repository equal to the base, timestamps now.
func (s *Server) AddPR(pr *PR) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if pr.State == "" {
		pr.State = stateOpen
	}
	if pr.BaseRepoID == 0 {
		pr.BaseRepoID = 100
	}
	if pr.HeadRepoID == 0 {
		pr.HeadRepoID = pr.BaseRepoID
	}
	if pr.HeadSHA == "" {
		pr.HeadSHA = fmt.Sprintf("sha-%s-%d", pr.Repo, pr.Number)
	}
	if pr.CreatedAt.IsZero() {
		pr.CreatedAt = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
		pr.UpdatedAt = pr.CreatedAt
	}
	for i := range pr.Comments {
		if pr.Comments[i].ID == 0 {
			s.commentID++
			pr.Comments[i].ID = s.commentID
		}
	}
	s.prs = append(s.prs, pr)
	s.refs[pr.Owner+"/"+pr.Repo+":"+pr.HeadRef] = true
}

// SetPRState changes a seeded pull request's state, as a human would.
func (s *Server) SetPRState(owner, repo string, number int, state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if pr := s.find(owner, repo, number); pr != nil {
		pr.State = state
	}
}

// AddCommit appends a commit to a seeded pull request, as a push would.
func (s *Server) AddCommit(owner, repo string, number int, c Commit) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if pr := s.find(owner, repo, number); pr != nil {
		pr.Commits = append(pr.Commits, c)
		pr.HeadSHA += "+"
	}
}

// PR returns a copy of a pull request's current state.
func (s *Server) PR(owner, repo string, number int) (PR, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pr := s.find(owner, repo, number)
	if pr == nil {
		return PR{}, false
	}
	cp := *pr
	cp.Comments = append([]Comment(nil), pr.Comments...)
	cp.Commits = append([]Commit(nil), pr.Commits...)
	return cp, true
}

// BranchExists reports whether a branch is present.
func (s *Server) BranchExists(owner, repo, branch string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refs[owner+"/"+repo+":"+branch]
}

// Requests returns every request so far.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

// Count returns how many requests matched method and a path prefix.
func (s *Server) Count(method, pathPrefix string) int {
	n := 0
	for _, r := range s.Requests() {
		if r.Method == method && strings.HasPrefix(r.Path, pathPrefix) {
			n++
		}
	}
	return n
}

// Writes returns the requests that were not reads.
func (s *Server) Writes() []Request {
	var out []Request
	for _, r := range s.Requests() {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !strings.HasSuffix(r.Path, "/access_tokens") {
			out = append(out, r)
		}
	}
	return out
}

// Fail answers the next times requests matching method and a path prefix
// with status; times < 0 means every request.
func (s *Server) Fail(method, pathPrefix string, status, times int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.faults = append(s.faults, &fault{method: method, pathPrefix: pathPrefix, status: status, remaining: times})
}

// SecondaryLimit answers the next times matching requests with a 403
// secondary rate limit carrying Retry-After: 1.
func (s *Server) SecondaryLimit(method, pathPrefix string, times int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.faults = append(s.faults, &fault{method: method, pathPrefix: pathPrefix, status: http.StatusForbidden, retryAfter: "1", remaining: times})
}

func (s *Server) record(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, APIPrefix)
		s.mu.Lock()
		s.requests = append(
			s.requests,
			Request{Method: r.Method, Path: path, Query: r.URL.RawQuery, Auth: r.Header.Get("Authorization"), UserAgent: r.UserAgent()},
		)
		f := s.takeFault(r.Method, path)
		s.mu.Unlock()
		if f != nil {
			writeFault(w, f)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) takeFault(method, path string) *fault {
	for _, f := range s.faults {
		if f.remaining == 0 || f.method != method || !strings.HasPrefix(path, f.pathPrefix) {
			continue
		}
		if f.remaining > 0 {
			f.remaining--
		}
		return f
	}
	return nil
}

func writeFault(w http.ResponseWriter, f *fault) {
	if f.retryAfter != "" {
		w.Header().Set("Retry-After", f.retryAfter)
		writeJSON(w, f.status, map[string]any{
			keyMessage:          "You have exceeded a secondary rate limit.",
			"documentation_url": "https://docs.github.com/rest/overview/rate-limits-for-the-rest-api#about-secondary-rate-limits",
		})
		return
	}
	writeJSON(w, f.status, map[string]any{keyMessage: http.StatusText(f.status)})
}

func (s *Server) find(owner, repo string, number int) *PR {
	for _, pr := range s.prs {
		if strings.EqualFold(pr.Owner, owner) && strings.EqualFold(pr.Repo, repo) && pr.Number == number {
			return pr
		}
	}
	return nil
}

// installationFor reads the installation id out of an installation token.
func installationFor(r *http.Request) (int64, bool) {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "token ghs_inst_")
	if !ok {
		return 0, false
	}
	id, err := strconv.ParseInt(tok, 10, 64)
	return id, err == nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v) //nolint:errcheck,errchkjson // a test server cannot report a write error anywhere useful
}

// paginate writes items[page] and a Link header for the next page.
func paginate[T any](w http.ResponseWriter, r *http.Request, items []T, wrap func([]T) any) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))        //nolint:errcheck // absent means page 1
	perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page")) //nolint:errcheck // absent means the default
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 30
	}
	start := min((page-1)*perPage, len(items))
	end := min(start+perPage, len(items))
	if end < len(items) {
		q := r.URL.Query()
		q.Set("page", strconv.Itoa(page+1))
		q.Set("per_page", strconv.Itoa(perPage))
		w.Header().Set("Link", fmt.Sprintf(`<http://%s%s?%s>; rel="next"`, r.Host, r.URL.Path, q.Encode()))
	}
	writeJSON(w, http.StatusOK, wrap(items[start:end]))
}

func asIs[T any](items []T) any { return items }

func readBody(r *http.Request, v any) error {
	b, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

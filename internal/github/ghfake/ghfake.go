// Package ghfake is a stateful in-memory GitHub for remediation tests
// (IMPL-0030 task 3.8). A Repo holds commits, branches, pull requests,
// comments and the managed API settings; a Client acts on it as one
// actor and implements control.Writer, control.Reader and
// control.PRObserver with GitHub's semantics where remediation depends
// on them:
//
//   - Commit is createCommitOnBranch: it is refused with
//     control.ErrExpectedHeadMismatch unless the branch head is the
//     expected oid, and its commit is Verified with committer web-flow.
//   - CreateRef fails on an existing branch.
//   - UpdateBranch is a real three-way merge of the base into the head,
//     refusing a moved head and reporting control.ErrMergeConflict.
//   - Pull requests carry their author's user id and their head
//     repository id; a fork PR's head lives outside the repository.
//
// Users act through Repo's methods (Push, ForcePush, DeleteBranch,
// OpenPR, MergePR, ClosePR). Every call, by a Client or a user, lands in
// the request log, so a test can assert that the App never deleted a
// branch.
package ghfake

import (
	"context"
	"crypto/sha1" //nolint:gosec // git object ids are sha1
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/control"
)

// Errors the fake returns beyond control's sentinels.
var (
	// ErrRefExists is CreateRef on a branch that exists (GitHub's 422
	// "Reference already exists").
	ErrRefExists = errors.New("reference already exists")
	// ErrNotFound is a missing branch, commit or pull request.
	ErrNotFound = errors.New("not found")
)

// The user types GitHub reports.
const (
	TypeUser = "User"
	TypeBot  = "Bot"
)

// The pull-request states.
const (
	stateOpen   = "open"
	stateClosed = "closed"
)

// webFlow is the committer of every commit GitHub makes on a user's or
// an App's behalf through the API.
const webFlow = "web-flow"

// Actor is who makes a request.
type Actor struct {
	ID    int64
	Login string
	Type  string
}

// Request is one entry of the request log.
type Request struct {
	Repo   string
	Actor  string
	Method string
	Detail string
}

// GitHub is the fake: a set of repositories and one request log.
type GitHub struct {
	mu    sync.Mutex
	repos map[string]*Repo
	log   []Request
	seq   int
	now   time.Time
}

// New returns an empty fake whose clock starts at a fixed instant and
// advances one second per write.
func New() *GitHub {
	return &GitHub{repos: make(map[string]*Repo), now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

// Requests returns a copy of the request log.
func (g *GitHub) Requests() []Request {
	g.mu.Lock()
	defer g.mu.Unlock()

	return slices.Clone(g.log)
}

// Calls returns the logged requests of method, by anyone.
func (g *GitHub) Calls(method string) []Request {
	var out []Request

	for _, r := range g.Requests() {
		if r.Method == method {
			out = append(out, r)
		}
	}

	return out
}

// record appends to the log; the caller holds g.mu.
func (g *GitHub) record(repo *Repo, actor Actor, method, detail string) {
	g.log = append(g.log, Request{Repo: repo.FullName(), Actor: actor.Login, Method: method, Detail: detail})
}

// tick advances the clock; the caller holds g.mu.
func (g *GitHub) tick() time.Time {
	g.now = g.now.Add(time.Second)

	return g.now
}

// newSHA returns a fresh 40-hex object id; the caller holds g.mu.
func (g *GitHub) newSHA() string {
	g.seq++
	sum := sha256.Sum256(fmt.Appendf(nil, "commit-%d", g.seq))

	return hex.EncodeToString(sum[:20])
}

type commit struct {
	sha     string
	parents []string
	tree    map[string]string
	message string
	author  Actor
	// committer is the git committer login: web-flow for API commits, the
	// author for a push.
	committer string
	verified  bool
}

type pullRequest struct {
	control.PullRequest

	// sameRepo means the head branch lives in this repository and HeadSHA
	// follows its ref; a fork PR stores its head SHA.
	sameRepo bool
	comments []string
}

// Repo is one repository's state.
type Repo struct {
	gh *GitHub

	ID            int64
	Owner, Name   string
	DefaultBranch string

	commits map[string]*commit
	refs    map[string]string
	prs     map[int]*pullRequest
	nextPR  int

	settings   control.RepositorySettings
	rulesets   []control.Ruleset
	properties map[string]*string
	schema     []string
	labels     []control.Label
	alerts     bool

	// failures answer the next call of a method with an error, once each.
	failures map[string][]error
}

// AddRepo creates owner/name with id and one commit on main holding
// files, authored by owner.
func (g *GitHub) AddRepo(owner, name string, id int64, files map[string]string) *Repo {
	g.mu.Lock()
	defer g.mu.Unlock()

	r := &Repo{
		gh: g, ID: id, Owner: owner, Name: name, DefaultBranch: "main",
		commits: make(map[string]*commit), refs: make(map[string]string), prs: make(map[int]*pullRequest),
		properties: make(map[string]*string), failures: make(map[string][]error), nextPR: 1,
	}
	c := &commit{sha: g.newSHA(), tree: maps.Clone(files), message: "initial", author: Actor{Login: owner, Type: TypeUser}, committer: owner}
	if c.tree == nil {
		c.tree = make(map[string]string)
	}

	r.commits[c.sha] = c
	r.refs[r.DefaultBranch] = c.sha
	g.repos[r.FullName()] = r

	return r
}

// FullName is owner/name.
func (r *Repo) FullName() string { return r.Owner + "/" + r.Name }

// FailNext makes the next call of method (a Writer, Reader or PRObserver
// method name) return err, once. Calls queue in order.
func (r *Repo) FailNext(method string, err error) {
	r.gh.mu.Lock()
	defer r.gh.mu.Unlock()

	r.failures[method] = append(r.failures[method], err)
}

// failure pops method's next injected error; the caller holds gh.mu.
func (r *Repo) failure(method string) error {
	q := r.failures[method]
	if len(q) == 0 {
		return nil
	}

	r.failures[method] = q[1:]

	return q[0]
}

// Head returns branch's head SHA, or "" when it does not exist.
func (r *Repo) Head(branch string) string {
	r.gh.mu.Lock()
	defer r.gh.mu.Unlock()

	return r.refs[branch]
}

// File returns path's content at sha, and whether it exists.
func (r *Repo) File(sha, file string) (string, bool) {
	r.gh.mu.Lock()
	defer r.gh.mu.Unlock()

	c, ok := r.commits[sha]
	if !ok {
		return "", false
	}

	content, ok := c.tree[file]

	return content, ok
}

// PullRequest returns a copy of PR number, or nil.
func (r *Repo) PullRequest(number int) *control.PullRequest {
	r.gh.mu.Lock()
	defer r.gh.mu.Unlock()

	pr, ok := r.prs[number]
	if !ok {
		return nil
	}

	out := r.prView(pr)

	return &out
}

// Comments returns PR number's comments in order.
func (r *Repo) Comments(number int) []string {
	r.gh.mu.Lock()
	defer r.gh.mu.Unlock()

	if pr, ok := r.prs[number]; ok {
		return slices.Clone(pr.comments)
	}

	return nil
}

// Push commits changes (a nil value deletes the path) on top of branch
// as actor, git-style: unverified, committed by the actor. It creates
// the branch from the default branch when missing.
func (r *Repo) Push(actor Actor, branch string, changes map[string]*string, message string) string {
	r.gh.mu.Lock()
	defer r.gh.mu.Unlock()

	r.gh.record(r, actor, "Push", branch)

	parent := r.refs[branch]
	if parent == "" {
		parent = r.refs[r.DefaultBranch]
	}

	tree := maps.Clone(r.commits[parent].tree)

	for p, v := range changes {
		if v == nil {
			delete(tree, p)
		} else {
			tree[p] = *v
		}
	}

	c := &commit{sha: r.gh.newSHA(), parents: []string{parent}, tree: tree, message: message, author: actor, committer: actor.Login}
	r.commits[c.sha] = c
	r.refs[branch] = c.sha
	r.gh.tick()

	return c.sha
}

// ForcePush points branch at sha, which must be a known commit, as actor.
func (r *Repo) ForcePush(actor Actor, branch, sha string) {
	r.gh.mu.Lock()
	defer r.gh.mu.Unlock()

	r.gh.record(r, actor, "ForcePush", branch+"@"+sha)
	r.refs[branch] = sha
}

// DeleteBranch deletes branch as actor (a user; the App never does).
func (r *Repo) DeleteBranch(actor Actor, branch string) {
	r.gh.mu.Lock()
	defer r.gh.mu.Unlock()

	r.gh.record(r, actor, "DeleteBranch", branch)
	delete(r.refs, branch)
}

// PROptions shapes a user's pull request.
type PROptions struct {
	Title, Body string
	// HeadRepoID is the head repository; zero is this repository. A fork
	// PR's head is ForkHeadSHA, which need not exist here.
	HeadRepoID  int64
	ForkHeadSHA string
}

// OpenPR opens a pull request from head into base as actor.
func (r *Repo) OpenPR(actor Actor, head, base string, opts PROptions) int {
	r.gh.mu.Lock()
	defer r.gh.mu.Unlock()

	r.gh.record(r, actor, "OpenPR", head+"→"+base)

	pr := r.newPR(actor, head, base, opts.Title, opts.Body)
	if opts.HeadRepoID != 0 && opts.HeadRepoID != r.ID {
		pr.sameRepo, pr.HeadRepoID, pr.HeadSHA = false, opts.HeadRepoID, opts.ForkHeadSHA
	}

	return pr.Number
}

// newPR registers a same-repository PR; the caller holds gh.mu.
func (r *Repo) newPR(actor Actor, head, base, title, body string) *pullRequest {
	n := r.nextPR
	r.nextPR++

	pr := &pullRequest{
		PullRequest: control.PullRequest{
			Number: n, Title: title, Body: body, State: stateOpen, BaseRef: base, HeadRef: head, HeadRepoID: r.ID,
			URL:      fmt.Sprintf("https://github.com/%s/pull/%d", r.FullName(), n),
			AuthorID: actor.ID, AuthorLogin: actor.Login, AuthorType: actor.Type, CreatedAt: r.gh.tick(),
		},
		sameRepo: true,
	}
	r.prs[n] = pr

	return pr
}

// MergePR merges PR number into its base with a merge commit as actor:
// a three-way merge, which fails with control.ErrMergeConflict.
func (r *Repo) MergePR(actor Actor, number int) error {
	r.gh.mu.Lock()
	defer r.gh.mu.Unlock()

	r.gh.record(r, actor, "MergePR", fmt.Sprint(number))

	pr, ok := r.prs[number]
	if !ok || pr.State != stateOpen {
		return fmt.Errorf("merge #%d: %w", number, ErrNotFound)
	}

	sha, err := r.merge(actor, r.refs[pr.BaseRef], r.headSHA(pr), fmt.Sprintf("Merge pull request #%d", number))
	if err != nil {
		return err
	}

	r.refs[pr.BaseRef] = sha
	pr.State, pr.Merged, pr.ClosedAt = stateClosed, true, r.gh.tick()

	return nil
}

// ClosePR closes PR number unmerged as actor.
func (r *Repo) ClosePR(actor Actor, number int) {
	r.gh.mu.Lock()
	defer r.gh.mu.Unlock()

	r.gh.record(r, actor, "ClosePR", fmt.Sprint(number))

	if pr, ok := r.prs[number]; ok {
		pr.State, pr.ClosedAt = stateClosed, r.gh.tick()
	}
}

// headSHA is a PR's current head; the caller holds gh.mu.
func (r *Repo) headSHA(pr *pullRequest) string {
	if pr.sameRepo {
		return r.refs[pr.HeadRef]
	}

	return pr.HeadSHA
}

// prView is the PR as the API shows it; the caller holds gh.mu.
func (r *Repo) prView(pr *pullRequest) control.PullRequest {
	out := pr.PullRequest
	out.HeadSHA = r.headSHA(pr)

	return out
}

// ancestors is every commit reachable from sha, sha included; the caller
// holds gh.mu.
func (r *Repo) ancestors(sha string) map[string]bool {
	seen := make(map[string]bool)
	stack := []string{sha}

	for len(stack) > 0 {
		s := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		if s == "" || seen[s] {
			continue
		}

		seen[s] = true

		if c, ok := r.commits[s]; ok {
			stack = append(stack, c.parents...)
		}
	}

	return seen
}

// mergeBase is a common ancestor of a and b nearest to b (breadth-first
// from b); the caller holds gh.mu.
func (r *Repo) mergeBase(a, b string) string {
	inA := r.ancestors(a)
	queue := []string{b}
	seen := make(map[string]bool)

	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]

		if seen[s] {
			continue
		}

		seen[s] = true

		if inA[s] {
			return s
		}

		if c, ok := r.commits[s]; ok {
			queue = append(queue, c.parents...)
		}
	}

	return ""
}

// merge creates a merge commit of theirs into ours as actor: per path,
// the side that changed from the merge base wins, both sides changing
// it differently is a conflict. Merging an ancestor is a no-op; the
// caller holds gh.mu.
func (r *Repo) merge(actor Actor, ours, theirs, message string) (string, error) {
	if r.ancestors(ours)[theirs] {
		return ours, nil
	}

	base := r.mergeBase(ours, theirs)

	var baseTree map[string]string
	if c, ok := r.commits[base]; ok {
		baseTree = c.tree
	}

	o, t := r.commits[ours].tree, r.commits[theirs].tree
	tree := make(map[string]string)
	paths := make(map[string]bool)

	for _, m := range []map[string]string{baseTree, o, t} {
		for p := range m {
			paths[p] = true
		}
	}

	for p := range paths {
		bv, bok := baseTree[p]
		ov, ook := o[p]
		tv, tok := t[p]

		switch {
		case ov == tv && ook == tok:
			if ook {
				tree[p] = ov
			}
		case ov == bv && ook == bok:
			if tok {
				tree[p] = tv
			}
		case tv == bv && tok == bok:
			if ook {
				tree[p] = ov
			}
		default:
			return "", fmt.Errorf("%s: %w", p, control.ErrMergeConflict)
		}
	}

	c := &commit{sha: r.gh.newSHA(), parents: []string{ours, theirs}, tree: tree, message: message, author: actor, committer: webFlow, verified: true}
	r.commits[c.sha] = c

	return c.sha, nil
}

// Client is a repository as one actor reaches it. It implements
// control.Writer and control.PRObserver, and control.Reader at the
// commit At pins (the default branch head when empty).
type Client struct {
	repo  *Repo
	actor Actor
	at    string
}

var (
	_ control.Writer     = (*Client)(nil)
	_ control.Reader     = (*Client)(nil)
	_ control.PRObserver = (*Client)(nil)
)

// As returns the repository as actor.
func (r *Repo) As(actor Actor) *Client { return &Client{repo: r, actor: actor} }

// At returns c's Reader pinned to sha.
func (c *Client) At(sha string) *Client {
	out := *c
	out.at = sha

	return &out
}

// begin locks the fake, logs the call and returns its injected failure.
// The caller defers end.
func (c *Client) begin(method, detail string) error {
	c.repo.gh.mu.Lock()
	c.repo.gh.record(c.repo, c.actor, method, detail)

	return c.repo.failure(method)
}

func (c *Client) end() { c.repo.gh.mu.Unlock() }

// tree is the pinned commit's tree; the caller holds gh.mu.
func (c *Client) tree() (map[string]string, error) {
	sha := c.at
	if sha == "" {
		sha = c.repo.refs[c.repo.DefaultBranch]
	}

	cm, ok := c.repo.commits[sha]
	if !ok {
		return nil, fmt.Errorf("commit %s: %w", sha, ErrNotFound)
	}

	return cm.tree, nil
}

// BlobSHA is git's blob id for content.
func BlobSHA(content string) string {
	sum := sha1.Sum(fmt.Appendf(nil, "blob %d\x00%s", len(content), content)) //nolint:gosec // git object ids are sha1

	return hex.EncodeToString(sum[:])
}

// Commit is createCommitOnBranch with expectedHeadOid = baseSHA.
func (c *Client) Commit(_ context.Context, branch, baseSHA string, changes []control.FileChange, message string) (string, error) {
	if err := c.begin("Commit", branch); err != nil {
		c.end()

		return "", err
	}
	defer c.end()

	r := c.repo

	head, ok := r.refs[branch]
	if !ok {
		return "", fmt.Errorf("branch %s: %w", branch, ErrNotFound)
	}

	if head != baseSHA {
		return "", fmt.Errorf("commit on %s at %s, head %s: %w", branch, baseSHA, head, control.ErrExpectedHeadMismatch)
	}

	tree := maps.Clone(r.commits[head].tree)

	for _, ch := range changes {
		if ch.Delete {
			delete(tree, ch.Path)
		} else {
			tree[ch.Path] = string(ch.Content)
		}
	}

	cm := &commit{sha: r.gh.newSHA(), parents: []string{head}, tree: tree, message: message, author: c.actor, committer: webFlow, verified: true}
	r.commits[cm.sha] = cm
	r.refs[branch] = cm.sha
	r.gh.tick()

	return cm.sha, nil
}

// CreateRef creates branch at sha; ErrRefExists when it exists.
func (c *Client) CreateRef(_ context.Context, branch, sha string) error {
	if err := c.begin("CreateRef", branch); err != nil {
		c.end()

		return err
	}
	defer c.end()

	if _, ok := c.repo.refs[branch]; ok {
		return fmt.Errorf("create %s: %w", branch, ErrRefExists)
	}

	if _, ok := c.repo.commits[sha]; !ok {
		return fmt.Errorf("create %s at %s: %w", branch, sha, ErrNotFound)
	}

	c.repo.refs[branch] = sha

	return nil
}

// UpdateBranch merges the base into PR number's head when the head is
// expectedHeadSHA.
func (c *Client) UpdateBranch(_ context.Context, number int, expectedHeadSHA string) error {
	if err := c.begin("UpdateBranch", fmt.Sprint(number)); err != nil {
		c.end()

		return err
	}
	defer c.end()

	r := c.repo

	pr, ok := r.prs[number]
	if !ok || !pr.sameRepo {
		return fmt.Errorf("update-branch #%d: %w", number, ErrNotFound)
	}

	head := r.refs[pr.HeadRef]
	if head != expectedHeadSHA {
		return fmt.Errorf("update-branch #%d: %w", number, control.ErrExpectedHeadMismatch)
	}

	sha, err := r.merge(c.actor, head, r.refs[pr.BaseRef], "Merge branch '"+pr.BaseRef+"' into "+pr.HeadRef)
	if err != nil {
		return err
	}

	r.refs[pr.HeadRef] = sha
	r.gh.tick()

	return nil
}

// UpdatePullRequestBase points PR number at base.
func (c *Client) UpdatePullRequestBase(_ context.Context, number int, base string) error {
	return c.withPR("UpdatePullRequestBase", number, func(pr *pullRequest) { pr.BaseRef = base })
}

// CreatePullRequest opens a PR from head (a branch here) into base, as
// the actor.
func (c *Client) CreatePullRequest(_ context.Context, head, base, title, body string) (*control.PullRequest, error) {
	if err := c.begin("CreatePullRequest", head+"→"+base); err != nil {
		c.end()

		return nil, err
	}
	defer c.end()

	if _, ok := c.repo.refs[head]; !ok {
		return nil, fmt.Errorf("head %s: %w", head, ErrNotFound)
	}

	for _, pr := range c.repo.prs {
		if pr.State == stateOpen && pr.sameRepo && pr.HeadRef == head && pr.BaseRef == base {
			return nil, fmt.Errorf("a pull request already exists for %s", head)
		}
	}

	out := c.repo.prView(c.repo.newPR(c.actor, head, base, title, body))

	return &out, nil
}

// UpdatePullRequest replaces PR number's title and body.
func (c *Client) UpdatePullRequest(_ context.Context, number int, title, body string) error {
	return c.withPR("UpdatePullRequest", number, func(pr *pullRequest) { pr.Title, pr.Body = title, body })
}

// ClosePullRequest closes PR number without merging.
func (c *Client) ClosePullRequest(_ context.Context, number int) error {
	return c.withPR("ClosePullRequest", number, func(pr *pullRequest) {
		if pr.State == stateOpen {
			pr.State, pr.ClosedAt = stateClosed, c.repo.gh.tick()
		}
	})
}

// UpsertPRComment edits the comment whose first line is marker or adds one.
func (c *Client) UpsertPRComment(_ context.Context, number int, marker, body string) error {
	return c.withPR("UpsertPRComment", number, func(pr *pullRequest) {
		for i, existing := range pr.comments {
			if strings.HasPrefix(existing, marker) {
				pr.comments[i] = body

				return
			}
		}

		pr.comments = append(pr.comments, body)
	})
}

func (c *Client) withPR(method string, number int, fn func(*pullRequest)) error {
	if err := c.begin(method, fmt.Sprint(number)); err != nil {
		c.end()

		return err
	}
	defer c.end()

	pr, ok := c.repo.prs[number]
	if !ok {
		return fmt.Errorf("pull request #%d: %w", number, ErrNotFound)
	}

	fn(pr)

	return nil
}

// UpdateRepository writes the non-nil fields of s.
func (c *Client) UpdateRepository(_ context.Context, s control.RepositorySettings) error { //nolint:gocritic // control.Writer's signature
	if err := c.begin("UpdateRepository", ""); err != nil {
		c.end()

		return err
	}
	defer c.end()

	mergeSettings(&c.repo.settings, &s)

	return nil
}

// mergeSettings copies src's non-nil fields into dst.
func mergeSettings(dst, src *control.RepositorySettings) {
	for _, f := range []struct{ d, s **bool }{
		{&dst.HasWiki, &src.HasWiki},
		{&dst.HasIssues, &src.HasIssues},
		{&dst.HasProjects, &src.HasProjects},
		{&dst.AllowMergeCommit, &src.AllowMergeCommit},
		{&dst.AllowSquashMerge, &src.AllowSquashMerge},
		{&dst.AllowRebaseMerge, &src.AllowRebaseMerge},
		{&dst.AllowAutoMerge, &src.AllowAutoMerge},
		{&dst.AllowUpdateBranch, &src.AllowUpdateBranch},
		{&dst.DeleteBranchOnMerge, &src.DeleteBranchOnMerge},
		{&dst.WebCommitSignoffRequired, &src.WebCommitSignoffRequired},
	} {
		if *f.s != nil {
			*f.d = *f.s
		}
	}

	for _, f := range []struct{ d, s **string }{
		{&dst.DefaultBranch, &src.DefaultBranch},
		{&dst.SquashMergeCommitTitle, &src.SquashMergeCommitTitle},
		{&dst.SquashMergeCommitMessage, &src.SquashMergeCommitMessage},
		{&dst.MergeCommitTitle, &src.MergeCommitTitle},
		{&dst.MergeCommitMessage, &src.MergeCommitMessage},
	} {
		if *f.s != nil {
			*f.d = *f.s
		}
	}

	if src.SecurityAndAnalysis != nil {
		if dst.SecurityAndAnalysis == nil {
			dst.SecurityAndAnalysis = make(map[string]string)
		}

		maps.Copy(dst.SecurityAndAnalysis, src.SecurityAndAnalysis)
	}
}

// UpsertRuleset creates rs or replaces it by ID; only a repository
// ruleset may be written.
func (c *Client) UpsertRuleset(_ context.Context, rs control.Ruleset) error { //nolint:gocritic // control.Writer's signature
	if err := c.begin("UpsertRuleset", rs.Name); err != nil {
		c.end()

		return err
	}
	defer c.end()

	if rs.Source != "" && rs.Source != control.RulesetSourceRepository {
		return fmt.Errorf("ruleset %s is %s-owned: %w", rs.Name, rs.Source, control.ErrPermission)
	}

	rs.Source = control.RulesetSourceRepository

	for i := range c.repo.rulesets {
		if rs.ID != 0 && c.repo.rulesets[i].ID == rs.ID {
			c.repo.rulesets[i] = rs

			return nil
		}
	}

	if rs.ID == 0 {
		rs.ID = int64(len(c.repo.rulesets) + 1)
	}

	c.repo.rulesets = append(c.repo.rulesets, rs)

	return nil
}

// SetCustomProperties writes props; a nil value clears one.
func (c *Client) SetCustomProperties(_ context.Context, props map[string]*string) error {
	if err := c.begin("SetCustomProperties", ""); err != nil {
		c.end()

		return err
	}
	defer c.end()

	maps.Copy(c.repo.properties, props)

	return nil
}

// UpsertLabel creates l or updates the same-named label, case-insensitively.
func (c *Client) UpsertLabel(_ context.Context, l control.Label) error {
	if err := c.begin("UpsertLabel", l.Name); err != nil {
		c.end()

		return err
	}
	defer c.end()

	for i := range c.repo.labels {
		if strings.EqualFold(c.repo.labels[i].Name, l.Name) {
			c.repo.labels[i] = l

			return nil
		}
	}

	c.repo.labels = append(c.repo.labels, l)

	return nil
}

// DeleteLabel deletes the label name.
func (c *Client) DeleteLabel(_ context.Context, name string) error {
	if err := c.begin("DeleteLabel", name); err != nil {
		c.end()

		return err
	}
	defer c.end()

	c.repo.labels = slices.DeleteFunc(c.repo.labels, func(l control.Label) bool { return strings.EqualFold(l.Name, name) })

	return nil
}

// SetVulnerabilityAlerts enables or disables Dependabot alerts.
func (c *Client) SetVulnerabilityAlerts(_ context.Context, enabled bool) error {
	if err := c.begin("SetVulnerabilityAlerts", fmt.Sprint(enabled)); err != nil {
		c.end()

		return err
	}
	defer c.end()

	c.repo.alerts = enabled

	return nil
}

// GetContents reads file at the pinned commit.
func (c *Client) GetContents(_ context.Context, file string) ([]byte, string, bool, error) {
	if err := c.begin("GetContents", file); err != nil {
		c.end()

		return nil, "", false, err
	}
	defer c.end()

	tree, err := c.tree()
	if err != nil {
		return nil, "", false, err
	}

	content, ok := tree[file]
	if !ok {
		return nil, "", false, nil
	}

	return []byte(content), BlobSHA(content), true, nil
}

// ListDirectory lists the entries directly under dir at the pinned commit.
func (c *Client) ListDirectory(_ context.Context, dir string) ([]string, error) {
	if err := c.begin("ListDirectory", dir); err != nil {
		c.end()

		return nil, err
	}
	defer c.end()

	tree, err := c.tree()
	if err != nil {
		return nil, err
	}

	prefix := strings.TrimSuffix(dir, "/") + "/"
	if dir == "" || dir == "." {
		prefix = ""
	}

	seen := make(map[string]bool)

	for p := range tree {
		rest, ok := strings.CutPrefix(p, prefix)
		if !ok {
			continue
		}

		if i := strings.IndexByte(rest, '/'); i >= 0 {
			rest = rest[:i]
		}

		seen[path.Clean(rest)] = true
	}

	return slices.Sorted(maps.Keys(seen)), nil
}

// GetRepository reads the settings, with the default branch.
func (c *Client) GetRepository(context.Context) (control.RepositorySettings, error) {
	if err := c.begin("GetRepository", ""); err != nil {
		c.end()

		return control.RepositorySettings{}, err
	}
	defer c.end()

	s := c.repo.settings
	def := c.repo.DefaultBranch
	s.DefaultBranch = &def

	return s, nil
}

// ListRulesets returns the rulesets.
func (c *Client) ListRulesets(context.Context) ([]control.Ruleset, error) {
	if err := c.begin("ListRulesets", ""); err != nil {
		c.end()

		return nil, err
	}
	defer c.end()

	return slices.Clone(c.repo.rulesets), nil
}

// GetCustomProperties returns the property values.
func (c *Client) GetCustomProperties(context.Context) (map[string]*string, error) {
	if err := c.begin("GetCustomProperties", ""); err != nil {
		c.end()

		return nil, err
	}
	defer c.end()

	return maps.Clone(c.repo.properties), nil
}

// OrgPropertySchema returns the org schema's property names.
func (c *Client) OrgPropertySchema(context.Context) ([]string, error) {
	if err := c.begin("OrgPropertySchema", ""); err != nil {
		c.end()

		return nil, err
	}
	defer c.end()

	return slices.Clone(c.repo.schema), nil
}

// ListLabels returns the labels.
func (c *Client) ListLabels(context.Context) ([]control.Label, error) {
	if err := c.begin("ListLabels", ""); err != nil {
		c.end()

		return nil, err
	}
	defer c.end()

	return slices.Clone(c.repo.labels), nil
}

// GetVulnerabilityAlerts reports whether alerts are enabled.
func (c *Client) GetVulnerabilityAlerts(context.Context) (bool, error) {
	if err := c.begin("GetVulnerabilityAlerts", ""); err != nil {
		c.end()

		return false, err
	}
	defer c.end()

	return c.repo.alerts, nil
}

// ListPullRequests returns the open PRs whose head starts with headPrefix.
func (c *Client) ListPullRequests(_ context.Context, headPrefix string) ([]control.PullRequest, error) {
	if err := c.begin("ListPullRequests", headPrefix); err != nil {
		c.end()

		return nil, err
	}
	defer c.end()

	var out []control.PullRequest

	for _, n := range slices.Sorted(maps.Keys(c.repo.prs)) {
		pr := c.repo.prs[n]
		if pr.State == stateOpen && strings.HasPrefix(pr.HeadRef, headPrefix) {
			out = append(out, c.repo.prView(pr))
		}
	}

	return out, nil
}

// GetPullRequest returns PR number.
func (c *Client) GetPullRequest(_ context.Context, number int) (*control.PullRequest, error) {
	if err := c.begin("GetPullRequest", fmt.Sprint(number)); err != nil {
		c.end()

		return nil, err
	}
	defer c.end()

	pr, ok := c.repo.prs[number]
	if !ok {
		return nil, fmt.Errorf("pull request #%d: %w", number, ErrNotFound)
	}

	out := c.repo.prView(pr)

	return &out, nil
}

// ListCommits returns branch's first-parent history, newest first.
func (c *Client) ListCommits(_ context.Context, branch string) ([]control.Commit, error) {
	if err := c.begin("ListCommits", branch); err != nil {
		c.end()

		return nil, err
	}
	defer c.end()

	sha, ok := c.repo.refs[branch]
	if !ok {
		return nil, fmt.Errorf("branch %s: %w", branch, ErrNotFound)
	}

	var out []control.Commit

	for sha != "" {
		cm := c.repo.commits[sha]
		out = append(out, control.Commit{
			SHA: cm.sha, Message: cm.message, AuthorID: cm.author.ID, AuthorLogin: cm.author.Login,
			CommitterLogin: cm.committer, Verified: cm.verified,
		})

		sha = ""
		if len(cm.parents) > 0 {
			sha = cm.parents[0]
		}
	}

	return out, nil
}

// GetRef returns branch's head.
func (c *Client) GetRef(_ context.Context, branch string) (string, bool, error) {
	if err := c.begin("GetRef", branch); err != nil {
		c.end()

		return "", false, err
	}
	defer c.end()

	sha, ok := c.repo.refs[branch]

	return sha, ok, nil
}

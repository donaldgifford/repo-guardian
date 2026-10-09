//go:build spike

// NOTE: IMPL-0028 Phase 0 GitHub spikes (INV-0022 spikes 1, 2, 3 and 10).
// They write to a real repository, so they are behind the `spike` build
// tag and run by the maintainer against a THROWAWAY repository with an
// unprotected default branch. Every request goes through the production
// transport chain (otelhttp -> rate-limit transport -> ghinstallation)
// via getInstallClient. Each test writes its raw observations to
// build/spike/<test>.json for INV-0022's Phase-0 addendum.
//
// Environment (the key is read from a file, never passed as a value):
//
//	RG_SPIKE_APP_ID            App id
//	RG_SPIKE_PRIVATE_KEY_PATH  path to the App's PEM key
//	RG_SPIKE_INSTALLATION_ID   installation id on the throwaway repo's owner
//	RG_SPIKE_REPO              owner/name of the throwaway repository
//
// Run: go test -tags spike -count=1 -v -run 'TestSpike_' ./internal/github/
package github

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const spikeAPI = "https://api.github.com"

type spikeObs struct {
	Step     string            `json:"step"`
	Method   string            `json:"method"`
	Path     string            `json:"path"`
	Status   int               `json:"status"`
	Elapsed  string            `json:"elapsed"`
	Headers  map[string]string `json:"headers"`
	Body     json.RawMessage   `json:"body,omitempty"`
	BodyText string            `json:"body_text,omitempty"`
	Err      string            `json:"err,omitempty"`
}

type spikeRun struct {
	t     *testing.T
	hc    *http.Client
	owner string
	name  string
	obs   []spikeObs
}

func newSpikeRun(t *testing.T, prefix string) *spikeRun {
	t.Helper()

	appID := spikeEnv(t, prefix+"APP_ID")
	keyPath := spikeEnv(t, prefix+"PRIVATE_KEY_PATH")
	inst := spikeEnv(t, prefix+"INSTALLATION_ID")
	repo := spikeEnv(t, "RG_SPIKE_REPO")

	id, err := strconv.ParseInt(appID, 10, 64)
	if err != nil {
		t.Fatalf("%sAPP_ID: %v", prefix, err)
	}

	instID, err := strconv.ParseInt(inst, 10, 64)
	if err != nil {
		t.Fatalf("%sINSTALLATION_ID: %v", prefix, err)
	}

	owner, name, ok := strings.Cut(repo, "/")
	if !ok {
		t.Fatalf("RG_SPIKE_REPO = %q, want owner/name", repo)
	}

	c, err := NewClient(id, keyPath, slog.New(slog.NewTextHandler(os.Stderr, nil)), 0.1)
	if err != nil {
		t.Fatal(err)
	}

	ghc, err := c.getInstallClient(instID)
	if err != nil {
		t.Fatal(err)
	}

	r := &spikeRun{t: t, hc: ghc.Client(), owner: owner, name: name}
	t.Cleanup(r.write)

	return r
}

func spikeEnv(t *testing.T, key string) string {
	t.Helper()

	v := os.Getenv(key)
	if v == "" {
		t.Skipf("%s is not set", key)
	}

	return v
}

func (r *spikeRun) write() {
	dir := filepath.Join("..", "..", "build", "spike")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		r.t.Errorf("mkdir: %v", err)
		return
	}

	b, err := json.MarshalIndent(r.obs, "", "  ")
	if err != nil {
		r.t.Errorf("marshal: %v", err)
		return
	}

	p := filepath.Join(dir, r.t.Name()+".json")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		r.t.Errorf("write: %v", err)
		return
	}

	r.t.Logf("observations written to %s", p)
}

// do sends one request and records it. It returns the status and the
// decoded JSON body (nil when the body is not JSON).
func (r *spikeRun) do(step, method, path string, body any) (int, map[string]any) {
	r.t.Helper()

	var rd io.Reader = http.NoBody

	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			r.t.Fatal(err)
		}

		rd = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(context.Background(), method, spikeAPI+path, rd)
	if err != nil {
		r.t.Fatal(err)
	}

	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	o := spikeObs{Step: step, Method: method, Path: path, Headers: map[string]string{}}
	start := time.Now()

	resp, err := r.hc.Do(req)
	o.Elapsed = time.Since(start).String()

	if err != nil {
		o.Err = err.Error()
		r.obs = append(r.obs, o)
		r.t.Logf("%s: %v", step, err)

		return 0, nil
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	o.Status = resp.StatusCode

	for _, h := range []string{"X-Ratelimit-Resource", "X-Ratelimit-Remaining", "X-Ratelimit-Used", "X-Ratelimit-Limit", "Retry-After", "X-Github-Request-Id"} {
		if v := resp.Header.Get(h); v != "" {
			o.Headers[h] = v
		}
	}

	var decoded map[string]any
	if json.Valid(raw) {
		if len(raw) > 8192 {
			o.BodyText = string(raw[:8192]) + "...(truncated)"
		} else {
			o.Body = raw
		}

		_ = json.Unmarshal(raw, &decoded)
	} else {
		o.BodyText = string(raw)
	}

	r.obs = append(r.obs, o)
	r.t.Logf("%s: %s %s -> %d (%s)", step, method, path, o.Status, o.Elapsed)

	return resp.StatusCode, decoded
}

func (r *spikeRun) repoPath(suffix string) string {
	return "/repos/" + r.owner + "/" + r.name + suffix
}

func (r *spikeRun) defaultBranch() string {
	r.t.Helper()

	_, repo := r.do("read repository", http.MethodGet, r.repoPath(""), nil)

	b, _ := repo["default_branch"].(string)
	if b == "" {
		r.t.Fatal("no default_branch")
	}

	return b
}

func (r *spikeRun) head(branch string) string {
	r.t.Helper()

	_, ref := r.do("read ref "+branch, http.MethodGet, r.repoPath("/git/ref/heads/"+branch), nil)

	obj, _ := ref["object"].(map[string]any)

	sha, _ := obj["sha"].(string)
	if sha == "" {
		r.t.Fatalf("no head for %s", branch)
	}

	return sha
}

func (r *spikeRun) branch(name, from string) {
	r.t.Helper()

	if st, _ := r.do("create branch "+name, http.MethodPost, r.repoPath("/git/refs"),
		map[string]string{"ref": "refs/heads/" + name, "sha": from}); st != http.StatusCreated {
		r.t.Fatalf("create branch %s: %d", name, st)
	}
}

type spikeFile struct {
	path    string
	content []byte
}

// commit runs createCommitOnBranch and returns the new oid ("" on error).
func (r *spikeRun) commit(step, branch, expected string, files []spikeFile) string {
	r.t.Helper()

	adds := make([]map[string]string, len(files))
	for i, f := range files {
		adds[i] = map[string]string{"path": f.path, "contents": base64.StdEncoding.EncodeToString(f.content)}
	}

	q := map[string]any{
		"query": `mutation($input: CreateCommitOnBranchInput!) {
  createCommitOnBranch(input: $input) {
    commit { oid url signature { isValid state wasSignedByGitHub signer { login } } }
  }
}`,
		"variables": map[string]any{"input": map[string]any{
			"branch":          map[string]string{"repositoryNameWithOwner": r.owner + "/" + r.name, "branchName": branch},
			"message":         map[string]string{"headline": "rg-spike: " + step},
			"expectedHeadOid": expected,
			"fileChanges":     map[string]any{"additions": adds},
		}},
	}

	_, body := r.do(step, http.MethodPost, "/graphql", q)

	data, _ := body["data"].(map[string]any)
	cc, _ := data["createCommitOnBranch"].(map[string]any)
	c, _ := cc["commit"].(map[string]any)
	oid, _ := c["oid"].(string)

	return oid
}

func spikeFiles(n, size int, prefix string) []spikeFile {
	out := make([]spikeFile, n)
	for i := range out {
		out[i] = spikeFile{path: fmt.Sprintf("%s/f%03d.txt", prefix, i), content: bytes.Repeat([]byte("a"), size)}
	}

	return out
}

// TestSpike_GraphQLCommit is IMPL-0028 task 0.6 (INV-0022 spike 1).
func TestSpike_GraphQLCommit(t *testing.T) {
	r := newSpikeRun(t, "RG_SPIKE_")
	stamp := strconv.FormatInt(time.Now().Unix(), 10)
	b := "rg-spike/commit-" + stamp

	base := r.head(r.defaultBranch())
	r.branch(b, base)

	o1 := r.commit("correct expectedHeadOid", b, base, spikeFiles(1, 16, "spike-"+stamp+"/one"))
	if o1 == "" {
		t.Fatal("the correct-head commit failed; see build/spike")
	}

	r.commit("stale expectedHeadOid", b, base, spikeFiles(1, 16, "spike-"+stamp+"/stale"))

	cur := r.head(b)
	if o := r.commit("100 files", b, cur, spikeFiles(100, 16, "spike-"+stamp+"/hundred")); o != "" {
		cur = o
	}

	if o := r.commit("101 files", b, cur, spikeFiles(101, 16, "spike-"+stamp+"/hundred-one")); o != "" {
		cur = o
	}

	if o := r.commit("one 1 MiB file", b, cur, spikeFiles(1, 1<<20, "spike-"+stamp+"/mib")); o != "" {
		cur = o
	}

	r.commit("one 1 MiB + 1 byte file", b, cur, spikeFiles(1, 1<<20+1, "spike-"+stamp+"/mib-plus"))

	r.do("commit verification", http.MethodGet, r.repoPath("/commits/"+o1), nil)

	t.Logf("branch %s left on %s/%s for inspection", b, r.owner, r.name)
}

// TestSpike_UpdateBranch is IMPL-0028 task 0.7 (INV-0022 spike 2).
func TestSpike_UpdateBranch(t *testing.T) {
	r := newSpikeRun(t, "RG_SPIKE_")
	stamp := strconv.FormatInt(time.Now().Unix(), 10)
	main := r.defaultBranch()
	dir := "spike-" + stamp

	// A file both sides will later change, for the conflict case.
	h0 := r.commit("seed conflict file on default branch", main, r.head(main),
		[]spikeFile{{dir + "/conflict.txt", []byte("base\n")}})
	if h0 == "" {
		t.Fatal("cannot commit to the default branch; is it protected?")
	}

	// PR 1: behind-able, non-conflicting.
	b1 := "rg-spike/update-" + stamp
	r.branch(b1, h0)
	b1Head := r.commit("pr1 change", b1, h0, []spikeFile{{dir + "/pr1.txt", []byte("pr1\n")}})
	pr1 := r.pr("open pr1", b1, main)

	r.do("update-branch: already up to date", http.MethodPut, r.repoPath(fmt.Sprintf("/pulls/%d/update-branch", pr1)),
		map[string]string{"expected_head_sha": b1Head})

	h1 := r.commit("move default branch", main, h0, []spikeFile{{dir + "/main.txt", []byte("main\n")}})

	r.do("update-branch: stale expected_head_sha", http.MethodPut, r.repoPath(fmt.Sprintf("/pulls/%d/update-branch", pr1)),
		map[string]string{"expected_head_sha": h0})

	start := time.Now()

	r.do("update-branch: behind, clean", http.MethodPut, r.repoPath(fmt.Sprintf("/pulls/%d/update-branch", pr1)),
		map[string]string{"expected_head_sha": b1Head})

	for time.Since(start) < 2*time.Minute {
		if r.headQuiet(b1) != b1Head {
			r.obs = append(r.obs, spikeObs{Step: "head moved after update-branch", Elapsed: time.Since(start).String()})
			t.Logf("head moved after %s", time.Since(start))

			break
		}

		time.Sleep(500 * time.Millisecond)
	}

	// PR 2: conflicting.
	b2 := "rg-spike/conflict-" + stamp
	r.branch(b2, h1)
	b2Head := r.commit("pr2 changes conflict file", b2, h1, []spikeFile{{dir + "/conflict.txt", []byte("branch\n")}})
	pr2 := r.pr("open pr2", b2, main)
	r.commit("default branch changes conflict file", main, r.head(main), []spikeFile{{dir + "/conflict.txt", []byte("main\n")}})

	r.do("update-branch: conflict", http.MethodPut, r.repoPath(fmt.Sprintf("/pulls/%d/update-branch", pr2)),
		map[string]string{"expected_head_sha": b2Head})

	t.Logf("PRs #%d and #%d left open on %s/%s", pr1, pr2, r.owner, r.name)
}

func (r *spikeRun) pr(step, head, base string) int {
	r.t.Helper()

	st, body := r.do(step, http.MethodPost, r.repoPath("/pulls"),
		map[string]string{"title": "rg-spike: " + step, "head": head, "base": base, "body": "IMPL-0028 Phase 0 spike; safe to close."})
	if st != http.StatusCreated {
		r.t.Fatalf("%s: %d", step, st)
	}

	n, _ := body["number"].(float64)

	return int(n)
}

// headQuiet reads a branch head without recording the read.
func (r *spikeRun) headQuiet(branch string) string {
	n := len(r.obs)
	h := r.head(branch)
	r.obs = r.obs[:n]

	return h
}

// TestSpike_LabelCase is IMPL-0028 task 0.15 (INV-0022 spike 10).
func TestSpike_LabelCase(t *testing.T) {
	r := newSpikeRun(t, "RG_SPIKE_")
	stamp := strconv.FormatInt(time.Now().Unix(), 10)
	mixed, lower, upper := "Spike-Case-"+stamp, "spike-case-"+stamp, "SPIKE-CASE-"+stamp

	r.do("create mixed-case label", http.MethodPost, r.repoPath("/labels"), map[string]string{"name": mixed, "color": "ededed"})
	r.do("create the lower-case twin", http.MethodPost, r.repoPath("/labels"), map[string]string{"name": lower, "color": "ededed"})
	r.do("get by lower-case name", http.MethodGet, r.repoPath("/labels/"+lower), nil)
	r.do("rename via lower-case name to upper case", http.MethodPatch, r.repoPath("/labels/"+lower),
		map[string]string{"new_name": upper, "color": "000000"})
	r.do("get by mixed-case name after rename", http.MethodGet, r.repoPath("/labels/"+mixed), nil)
	r.do("list labels", http.MethodGet, r.repoPath("/labels?per_page=100"), nil)
}

// TestSpike_EvalAppPermissions is IMPL-0028 task 0.8 (INV-0022 spike 3).
// It runs as a separately registered App holding only Metadata read,
// Contents read, Pull requests read and organisation Custom properties
// read, configured through RG_SPIKE_EVAL_APP_ID,
// RG_SPIKE_EVAL_PRIVATE_KEY_PATH and RG_SPIKE_EVAL_INSTALLATION_ID. It
// makes no writes.
func TestSpike_EvalAppPermissions(t *testing.T) {
	r := newSpikeRun(t, "RG_SPIKE_EVAL_")

	_, repo := r.do("repository settings", http.MethodGet, r.repoPath(""), nil)

	for _, f := range []string{
		"allow_merge_commit", "allow_squash_merge", "allow_rebase_merge", "allow_auto_merge",
		"delete_branch_on_merge", "allow_update_branch", "squash_merge_commit_title",
		"squash_merge_commit_message", "merge_commit_title", "merge_commit_message",
		"web_commit_signoff_required", "has_wiki", "has_issues", "has_projects", "security_and_analysis",
	} {
		v, ok := repo[f]
		t.Logf("repository.%s present=%v value=%v", f, ok, v)
	}

	_, _ = r.do("rulesets including parents", http.MethodGet, r.repoPath("/rulesets?includes_parents=true"), nil)

	st, list := r.doList("rulesets list (for ids)", r.repoPath("/rulesets?includes_parents=true"))
	if st == http.StatusOK && len(list) > 0 {
		if id, ok := list[0]["id"].(float64); ok {
			r.do("ruleset by id", http.MethodGet, r.repoPath(fmt.Sprintf("/rulesets/%d", int64(id))), nil)
		}
	}

	r.do("custom property values", http.MethodGet, r.repoPath("/properties/values"), nil)
	r.do("org custom property schema", http.MethodGet, "/orgs/"+r.owner+"/properties/schema", nil)
	r.do("vulnerability alerts (expect admin-only)", http.MethodGet, r.repoPath("/vulnerability-alerts"), nil)
	r.do("branch protection (expect admin-only)", http.MethodGet, r.repoPath("/branches/"+r.defaultBranch()+"/protection"), nil)
	r.do("CODEOWNERS errors", http.MethodGet, r.repoPath("/codeowners/errors"), nil)
}

// doList is do for endpoints returning a JSON array.
func (r *spikeRun) doList(step, path string) (int, []map[string]any) {
	r.t.Helper()

	st, _ := r.do(step, http.MethodGet, path, nil)

	var out []map[string]any

	if o := r.obs[len(r.obs)-1]; o.Body != nil {
		_ = json.Unmarshal(o.Body, &out)
	}

	return st, out
}

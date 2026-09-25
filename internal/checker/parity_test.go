package checker

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
)

// The parity suite (DESIGN-0025, IMPL-0025 Phase 4) pins the engine's
// GitHub write behavior and v1's actionable verdicts across the outcome
// enrichment. Every scenario in the engine test files runs CheckRepo
// through parityCheckRepo, which records each write call and outcome
// into testdata/parity/<test>.golden.json. Enrichment is data-only, so
// the goldens captured before it must still match byte for byte after.
//
// Regenerate with: go test ./internal/checker -run . -update-parity
// Only regenerate for a deliberate behavior change.

var updateParity = flag.Bool("update-parity", false, "rewrite the parity goldens in testdata/parity")

// parityCall is one normalized GitHub write.
type parityCall struct {
	Method string `json:"method"`
	Args   []any  `json:"args"`
}

// parityOutcome is one outcome as v1 saw it.
type parityOutcome struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Actionable bool   `json:"actionable"`
}

// parityCheck is one CheckRepo call.
type parityCheck struct {
	Repo     string          `json:"repo"`
	Writes   []parityCall    `json:"writes"`
	Outcomes []parityOutcome `json:"outcomes"`
	Result   string          `json:"result"`
	Error    string          `json:"error,omitempty"`
}

var (
	parityMu   sync.Mutex
	parityLogs = map[string]*[]parityCheck{}
)

// parityCheckRepo runs e.CheckRepo with client wrapped in a recorder,
// and registers the per-test golden comparison on first use.
func parityCheckRepo(ctx context.Context, tb testing.TB, e *Engine, client ghclient.Client, owner, repo string) (*CheckResult, error) {
	tb.Helper()

	rec := &recordingClient{Client: client}
	res, err := e.CheckRepo(ctx, rec, owner, repo)

	check := parityCheck{Repo: owner + "/" + repo, Writes: rec.snapshot(), Result: "nil"}
	if res != nil {
		check.Result = "non-nil"

		for _, o := range res.Outcomes {
			check.Outcomes = append(check.Outcomes, parityOutcome{Kind: string(o.Kind), Name: o.RuleName, Actionable: v1Actionable(o)})
		}
	}

	if err != nil {
		check.Error = normalizeParity(err.Error())
	}

	parityMu.Lock()
	log, ok := parityLogs[tb.Name()]
	if !ok {
		log = &[]parityCheck{}
		parityLogs[tb.Name()] = log
	}
	*log = append(*log, check)
	parityMu.Unlock()

	if !ok {
		tb.Cleanup(func() { compareParityGolden(tb, log) })
	}

	return res, err
}

// v1Actionable is the verdict v1 would have recorded for o.
func v1Actionable(o RuleOutcome) bool {
	return o.Actionable
}

func compareParityGolden(tb testing.TB, log *[]parityCheck) {
	tb.Helper()

	if tb.Failed() {
		return
	}

	parityMu.Lock()
	got, err := json.MarshalIndent(*log, "", "  ")
	parityMu.Unlock()

	if err != nil {
		tb.Fatalf("parity: marshal: %v", err)
	}

	got = append(got, '\n')
	path := filepath.Join("testdata", "parity", parityFileName(tb.Name())+".golden.json")

	if *updateParity {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			tb.Fatalf("parity: mkdir: %v", err)
		}

		if err := os.WriteFile(path, got, 0o600); err != nil {
			tb.Fatalf("parity: write golden: %v", err)
		}

		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		tb.Fatalf("parity: read golden %s (run with -update-parity to create it): %v", path, err)
	}

	if !bytes.Equal(got, want) {
		tb.Errorf("parity: GitHub writes or actionable verdicts changed for %s\n--- want\n%s\n--- got\n%s", tb.Name(), want, got)
	}
}

var parityUnsafe = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)

func parityFileName(name string) string {
	return parityUnsafe.ReplaceAllString(name, "__")
}

// Timestamps are the only nondeterministic text in write arguments
// (the sticky reconcile-log comment carries one).
var parityTimestamp = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})`)

func normalizeParity(s string) string {
	return parityTimestamp.ReplaceAllString(s, "<TIME>")
}

// recordingClient decorates a client and logs every write call with
// normalized arguments. Reads pass through unrecorded.
type recordingClient struct {
	ghclient.Client

	mu    sync.Mutex
	calls []parityCall
}

func (r *recordingClient) record(method string, args ...any) {
	norm := make([]any, len(args))

	for i, a := range args {
		switch v := a.(type) {
		case string:
			norm[i] = normalizeParity(v)
		case int, int64, bool, []string:
			norm[i] = v
		default:
			b, err := json.Marshal(v)
			if err != nil {
				norm[i] = fmt.Sprintf("<unmarshalable %T>", v)

				continue
			}

			norm[i] = json.RawMessage(normalizeParity(string(b)))
		}
	}

	r.mu.Lock()
	r.calls = append(r.calls, parityCall{Method: method, Args: norm})
	r.mu.Unlock()
}

func (r *recordingClient) snapshot() []parityCall {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.calls)
}

func (r *recordingClient) CreateBranch(ctx context.Context, owner, repo, branch, baseSHA string) error {
	r.record("CreateBranch", owner, repo, branch, baseSHA)

	return r.Client.CreateBranch(ctx, owner, repo, branch, baseSHA)
}

func (r *recordingClient) DeleteBranch(ctx context.Context, owner, repo, branch string) error {
	r.record("DeleteBranch", owner, repo, branch)

	return r.Client.DeleteBranch(ctx, owner, repo, branch)
}

func (r *recordingClient) CreateOrUpdateFile(ctx context.Context, owner, repo, branch, path, content, message string) error {
	r.record("CreateOrUpdateFile", owner, repo, branch, path, content, message)

	return r.Client.CreateOrUpdateFile(ctx, owner, repo, branch, path, content, message)
}

func (r *recordingClient) CreatePullRequest(ctx context.Context, owner, repo, title, body, head, base string) (*ghclient.PullRequest, error) {
	r.record("CreatePullRequest", owner, repo, title, body, head, base)

	return r.Client.CreatePullRequest(ctx, owner, repo, title, body, head, base)
}

func (r *recordingClient) AddLabelsToPR(ctx context.Context, owner, repo string, prNumber int, labels []string) error {
	r.record("AddLabelsToPR", owner, repo, prNumber, labels)

	return r.Client.AddLabelsToPR(ctx, owner, repo, prNumber, labels)
}

func (r *recordingClient) SetCustomPropertyValues(ctx context.Context, owner, repo string, properties []*ghclient.CustomPropertyValue) error {
	r.record("SetCustomPropertyValues", owner, repo, properties)

	return r.Client.SetCustomPropertyValues(ctx, owner, repo, properties)
}

func (r *recordingClient) EnableVulnerabilityAlerts(ctx context.Context, owner, repo string) error {
	r.record("EnableVulnerabilityAlerts", owner, repo)

	return r.Client.EnableVulnerabilityAlerts(ctx, owner, repo)
}

func (r *recordingClient) DisableVulnerabilityAlerts(ctx context.Context, owner, repo string) error {
	r.record("DisableVulnerabilityAlerts", owner, repo)

	return r.Client.DisableVulnerabilityAlerts(ctx, owner, repo)
}

func (r *recordingClient) UpdateRepository(ctx context.Context, owner, repo string, opts *ghclient.RepoUpdateOpts) error {
	r.record("UpdateRepository", owner, repo, opts)

	return r.Client.UpdateRepository(ctx, owner, repo, opts)
}

func (r *recordingClient) CreateRepositoryRuleset(ctx context.Context, owner, repo string, ruleset *ghclient.Ruleset) (*ghclient.Ruleset, error) {
	r.record("CreateRepositoryRuleset", owner, repo, ruleset)

	return r.Client.CreateRepositoryRuleset(ctx, owner, repo, ruleset)
}

func (r *recordingClient) UpdateRepositoryRuleset(
	ctx context.Context, owner, repo string, rulesetID int64, ruleset *ghclient.Ruleset,
) (*ghclient.Ruleset, error) {
	r.record("UpdateRepositoryRuleset", owner, repo, rulesetID, ruleset)

	return r.Client.UpdateRepositoryRuleset(ctx, owner, repo, rulesetID, ruleset)
}

func (r *recordingClient) CreateLabel(ctx context.Context, owner, repo string, label *ghclient.Label) error {
	r.record("CreateLabel", owner, repo, label)

	return r.Client.CreateLabel(ctx, owner, repo, label)
}

func (r *recordingClient) UpdateLabel(ctx context.Context, owner, repo, name string, label *ghclient.Label) error {
	r.record("UpdateLabel", owner, repo, name, label)

	return r.Client.UpdateLabel(ctx, owner, repo, name, label)
}

func (r *recordingClient) DeleteLabel(ctx context.Context, owner, repo, name string) error {
	r.record("DeleteLabel", owner, repo, name)

	return r.Client.DeleteLabel(ctx, owner, repo, name)
}

func (r *recordingClient) DeleteFile(ctx context.Context, owner, repo, branch, path, sha, message string) error {
	r.record("DeleteFile", owner, repo, branch, path, sha, message)

	return r.Client.DeleteFile(ctx, owner, repo, branch, path, sha, message)
}

func (r *recordingClient) UpdatePullRequest(ctx context.Context, owner, repo string, number int, title, body string) error {
	r.record("UpdatePullRequest", owner, repo, number, title, body)

	return r.Client.UpdatePullRequest(ctx, owner, repo, number, title, body)
}

func (r *recordingClient) UpdatePRBranch(ctx context.Context, owner, repo string, number int) error {
	r.record("UpdatePRBranch", owner, repo, number)

	return r.Client.UpdatePRBranch(ctx, owner, repo, number)
}

func (r *recordingClient) ClosePullRequest(ctx context.Context, owner, repo string, number int) error {
	r.record("ClosePullRequest", owner, repo, number)

	return r.Client.ClosePullRequest(ctx, owner, repo, number)
}

func (r *recordingClient) UpsertPRComment(ctx context.Context, owner, repo string, number int, marker, body string) error {
	r.record("UpsertPRComment", owner, repo, number, marker, body)

	return r.Client.UpsertPRComment(ctx, owner, repo, number, marker, body)
}

// parityGoldenCount is used by the suite's non-vacuity check: a suite
// with no goldens would pass trivially.
func parityGoldenCount(tb testing.TB) int {
	tb.Helper()

	entries, err := os.ReadDir(filepath.Join("testdata", "parity"))
	if err != nil {
		return 0
	}

	n := 0

	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".golden.json") {
			n++
		}
	}

	return n
}

func TestParity_GoldensExist(t *testing.T) {
	t.Parallel()

	if *updateParity {
		t.Skip("regenerating goldens")
	}

	if n := parityGoldenCount(t); n < 50 {
		t.Fatalf("parity goldens = %d, want at least 50; the suite would be vacuous", n)
	}
}

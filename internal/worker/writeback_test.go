package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/mock"

	"github.com/donaldgifford/repo-guardian/internal/checker"
	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/queue"
	"github.com/donaldgifford/repo-guardian/internal/store"
	storemocks "github.com/donaldgifford/repo-guardian/internal/store/mocks"
)

// silentLogger discards all log output. Keeps the test runner quiet
// while still satisfying the *slog.Logger non-nil requirement on the
// writeBack call path.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newPool(stateStore store.Store, policyVersion string) *Pool {
	return &Pool{
		stateStore:    stateStore,
		policyVersion: policyVersion,
		logger:        silentLogger(),
	}
}

func TestWriteBack_SuccessPath(t *testing.T) {
	metrics.StoreWritebackTotal.Reset()

	var captured *store.RepoState

	ms := storemocks.NewMockStore(t)
	ms.EXPECT().
		UpdateRepoState(mock.Anything, mock.Anything).
		Run(func(_ context.Context, s *store.RepoState) { captured = s }).
		Return(nil).
		Once()

	p := newPool(ms, "v1")
	p.writeBack(context.Background(), silentLogger(), queue.Job{
		InstallationID: 42,
		Owner:          "octo",
		Repo:           "alpha",
	}, nil, nil)

	if captured == nil {
		t.Fatal("UpdateRepoState was not invoked")
	}

	if captured.LastCheckStatus != store.StatusSuccess {
		t.Errorf("LastCheckStatus = %q, want %q", captured.LastCheckStatus, store.StatusSuccess)
	}

	if captured.LastError != "" {
		t.Errorf("LastError = %q, want empty on success", captured.LastError)
	}

	if captured.PolicyVersion != "v1" {
		t.Errorf("PolicyVersion = %q, want v1", captured.PolicyVersion)
	}

	if captured.LastCheckedAt == nil {
		t.Error("LastCheckedAt is nil")
	}

	if got := testutil.ToFloat64(metrics.StoreWritebackTotal.WithLabelValues("42", "ok")); got != 1 {
		t.Errorf("store_writeback_total{ok}: got %v, want 1", got)
	}
}

func TestWriteBack_ErrorPath(t *testing.T) {
	metrics.StoreWritebackTotal.Reset()

	var captured *store.RepoState

	ms := storemocks.NewMockStore(t)
	ms.EXPECT().
		UpdateRepoState(mock.Anything, mock.Anything).
		Run(func(_ context.Context, s *store.RepoState) { captured = s }).
		Return(nil).
		Once()

	p := newPool(ms, "v2")
	p.writeBack(context.Background(), silentLogger(), queue.Job{
		InstallationID: 7,
		Owner:          "octo",
		Repo:           "beta",
	}, errors.New("boom"), nil)

	if captured == nil {
		t.Fatal("UpdateRepoState was not invoked")
	}

	if captured.LastCheckStatus != store.StatusError {
		t.Errorf("LastCheckStatus = %q, want %q", captured.LastCheckStatus, store.StatusError)
	}

	if captured.LastError != "boom" {
		t.Errorf("LastError = %q, want %q", captured.LastError, "boom")
	}

	if got := testutil.ToFloat64(metrics.StoreWritebackTotal.WithLabelValues("7", "ok")); got != 1 {
		t.Errorf("store_writeback_total{ok}: got %v, want 1", got)
	}
}

func TestWriteBack_ErrorPath_TruncatesLongError(t *testing.T) {
	long := strings.Repeat("x", errMaxRunes+200)

	var captured *store.RepoState

	ms := storemocks.NewMockStore(t)
	ms.EXPECT().
		UpdateRepoState(mock.Anything, mock.Anything).
		Run(func(_ context.Context, s *store.RepoState) { captured = s }).
		Return(nil).
		Once()

	p := newPool(ms, "v1")
	p.writeBack(context.Background(), silentLogger(), queue.Job{
		InstallationID: 1,
		Owner:          "o",
		Repo:           "r",
	}, errors.New(long), nil)

	if captured == nil {
		t.Fatal("UpdateRepoState was not invoked")
	}

	if got := len([]rune(captured.LastError)); got != errMaxRunes {
		t.Errorf("LastError rune count = %d, want %d", got, errMaxRunes)
	}

	if !strings.HasSuffix(captured.LastError, "…") {
		t.Error("LastError missing ellipsis suffix")
	}
}

func TestWriteBack_StoreErrorIsLoggedAndCounted(t *testing.T) {
	metrics.StoreWritebackTotal.Reset()

	ms := storemocks.NewMockStore(t)
	ms.EXPECT().
		UpdateRepoState(mock.Anything, mock.Anything).
		Return(errors.New("DB unavailable")).
		Once()

	p := newPool(ms, "v1")
	// writeBack must not panic or propagate the error.
	p.writeBack(context.Background(), silentLogger(), queue.Job{
		InstallationID: 9,
		Owner:          "o",
		Repo:           "r",
	}, nil, nil)

	if got := testutil.ToFloat64(metrics.StoreWritebackTotal.WithLabelValues("9", "error")); got != 1 {
		t.Errorf("store_writeback_total{error}: got %v, want 1", got)
	}

	if got := testutil.ToFloat64(metrics.StoreWritebackTotal.WithLabelValues("9", "ok")); got != 0 {
		t.Errorf("store_writeback_total{ok}: got %v, want 0", got)
	}
}

func TestWriteBack_NilStore_NoPanic(t *testing.T) {
	t.Parallel()

	p := newPool(nil, "v1")
	// Must be a no-op; no mockery expectations to assert.
	p.writeBack(context.Background(), silentLogger(), queue.Job{
		InstallationID: 1,
		Owner:          "o",
		Repo:           "r",
	}, nil, nil)
}

// --- IMPL-0023 task 1.4: rule-state write-back ---

// TestWriteBack_PersistsRuleStates verifies the CheckResult reaches the
// store intact: every outcome becomes a RuleState stamped with the
// job's identity and the pool's policy version, and the per-repo
// catalog verdict rides on the repo_state row rather than a rule row.
func TestWriteBack_PersistsRuleStates(t *testing.T) {
	metrics.StoreWritebackTotal.Reset()

	var (
		capturedRepo  *store.RepoState
		capturedRules []store.RuleState
	)

	ms := storemocks.NewMockStore(t)
	ms.EXPECT().
		UpdateRepoState(mock.Anything, mock.Anything).
		Run(func(_ context.Context, s *store.RepoState) { capturedRepo = s }).
		Return(nil).
		Once()
	ms.EXPECT().
		UpsertRuleStates(mock.Anything, int64(42), "octo", "alpha", mock.Anything).
		Run(func(_ context.Context, _ int64, _, _ string, states []store.RuleState) {
			capturedRules = states
		}).
		Return(nil).
		Once()

	parseOK := false

	p := newPool(ms, "v1")
	p.writeBack(context.Background(), silentLogger(), queue.Job{
		InstallationID: 42,
		Owner:          "octo",
		Repo:           "alpha",
	}, nil, &checker.CheckResult{
		Outcomes: []checker.RuleOutcome{
			{RuleName: "codeowners", Kind: checker.RuleKindFile, Actionable: true},
			{RuleName: "enable_issues", Kind: checker.RuleKindSetting, Actionable: false},
			{RuleName: "protect_main", Kind: checker.RuleKindBranchProtection, Actionable: true},
		},
		CatalogParseOK: &parseOK,
	})

	if len(capturedRules) != 3 {
		t.Fatalf("UpsertRuleStates states = %d, want 3", len(capturedRules))
	}

	for _, got := range capturedRules {
		if got.InstallationID != 42 || got.Owner != "octo" || got.Repo != "alpha" {
			t.Errorf("rule %q identity = %d/%s/%s, want 42/octo/alpha",
				got.RuleName, got.InstallationID, got.Owner, got.Repo)
		}

		if got.PolicyVersion != "v1" {
			t.Errorf("rule %q PolicyVersion = %q, want the pool's %q", got.RuleName, got.PolicyVersion, "v1")
		}
	}

	// Kind must survive as the string the schema stores; a mismatch here
	// silently makes every rule_kind filter in a report return nothing.
	wantKind := map[string]string{
		"codeowners":    "file",
		"enable_issues": "setting",
		"protect_main":  "branch_protection",
	}
	for _, got := range capturedRules {
		if want := wantKind[got.RuleName]; got.RuleKind != want {
			t.Errorf("rule %q RuleKind = %q, want %q", got.RuleName, got.RuleKind, want)
		}
	}

	if capturedRepo == nil || capturedRepo.CatalogParseOK == nil || *capturedRepo.CatalogParseOK {
		t.Errorf("repo_state CatalogParseOK = %v, want false carried from the CheckResult", capturedRepo.CatalogParseOK)
	}
}

// TestWriteBack_EmptyOutcomesStillWrites is the counterpart to
// TestWriteBack_NilResultSkipsRuleWrite. An empty outcome set is a
// verdict — "no rule applies to this repo any more" — and must reach
// the store, because UpsertRuleStates with an empty slice is what
// clears an archived or out-of-scope repo's rows. Skipping the call
// would leave it counted as failing forever.
func TestWriteBack_EmptyOutcomesStillWrites(t *testing.T) {
	metrics.StoreWritebackTotal.Reset()

	called := false

	ms := storemocks.NewMockStore(t)
	ms.EXPECT().UpdateRepoState(mock.Anything, mock.Anything).Return(nil).Once()
	ms.EXPECT().
		UpsertRuleStates(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, _ int64, _, _ string, states []store.RuleState) {
			called = true

			if len(states) != 0 {
				t.Errorf("states = %v, want empty", states)
			}
		}).
		Return(nil).
		Once()

	p := newPool(ms, "v1")
	p.writeBack(context.Background(), silentLogger(), queue.Job{
		InstallationID: 1, Owner: "o", Repo: "r",
	}, nil, &checker.CheckResult{})

	if !called {
		t.Error("UpsertRuleStates was not invoked for an empty outcome set, want the clearing write")
	}
}

// TestWriteBack_NilResultSkipsRuleWrite guards the error paths. A check
// that failed produced no trustworthy verdict, so touching rule_state
// would let delete-not-in read "we evaluated nothing" as "no rule
// applies" and wipe the repo's real posture on a transient API error.
func TestWriteBack_NilResultSkipsRuleWrite(t *testing.T) {
	metrics.StoreWritebackTotal.Reset()

	ms := storemocks.NewMockStore(t)
	// No UpsertRuleStates expectation: mockery fails the test if the
	// call happens at all.
	ms.EXPECT().UpdateRepoState(mock.Anything, mock.Anything).Return(nil).Once()

	p := newPool(ms, "v1")
	p.writeBack(context.Background(), silentLogger(), queue.Job{
		InstallationID: 1, Owner: "o", Repo: "r",
	}, errors.New("boom"), nil)
}

// TestWriteBack_RuleStateFailureIsBestEffort locks the IMPL-0015
// Phase 0 contract for the new write: a posture failure logs and counts
// but never propagates, and it must not suppress the repo_state write
// that keeps the sweep's freshness bookkeeping honest.
func TestWriteBack_RuleStateFailureIsBestEffort(t *testing.T) {
	metrics.StoreWritebackTotal.Reset()

	repoWritten := false

	ms := storemocks.NewMockStore(t)
	ms.EXPECT().
		UpdateRepoState(mock.Anything, mock.Anything).
		Run(func(_ context.Context, _ *store.RepoState) { repoWritten = true }).
		Return(nil).
		Once()
	ms.EXPECT().
		UpsertRuleStates(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(errors.New("deadlock detected")).
		Once()

	p := newPool(ms, "v1")
	// writeBack returns nothing — the absence of a panic or propagated
	// error IS the contract.
	p.writeBack(context.Background(), silentLogger(), queue.Job{
		InstallationID: 3, Owner: "o", Repo: "r",
	}, nil, &checker.CheckResult{
		Outcomes: []checker.RuleOutcome{
			{RuleName: "codeowners", Kind: checker.RuleKindFile, Actionable: true},
		},
	})

	if !repoWritten {
		t.Error("repo_state write did not happen; a rule-state failure must not suppress it")
	}

	if v := testutil.ToFloat64(metrics.StoreWritebackTotal.WithLabelValues("3", "error")); v != 1 {
		t.Errorf("store_writeback_total{installation_id=3,outcome=error} = %v, want 1", v)
	}

	if v := testutil.ToFloat64(metrics.StoreWritebackTotal.WithLabelValues("3", "ok")); v != 1 {
		t.Errorf("store_writeback_total{installation_id=3,outcome=ok} = %v, want 1 (the repo_state write)", v)
	}
}

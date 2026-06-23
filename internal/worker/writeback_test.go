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
	}, nil)

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
	}, errors.New("boom"))

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
	}, errors.New(long))

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
	}, nil)

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
	}, nil)
}

package activities

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

const (
	testRepoID       int64 = 42
	testInstallation int64 = 7
	testKey                = "evaluation/42/run/0"
)

func TestDeferUntil(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		reset     time.Time
		deferrals int
		want      time.Duration
	}{
		{"future reset", now.Add(10 * time.Minute), 3, 10 * time.Minute},
		{"stale reset backs off from 30s", now.Add(-time.Minute), 0, 30 * time.Second},
		{"backoff doubles", time.Time{}, 2, 2 * time.Minute},
		{"backoff caps at 30m", time.Time{}, 20, 30 * time.Minute},
	}

	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := deferUntil(now, tt.reset, tt.deferrals).Sub(now)
			if got < tt.want || got >= tt.want+min(tt.want/4, time.Minute) {
				t.Errorf("delay = %v, want %v plus jitter", got, tt.want)
			}
		})
	}
}

func TestAcquireResult_Label(t *testing.T) {
	t.Parallel()

	tests := map[string]workflows.AcquireResult{
		"granted":    {Granted: true},
		"optimistic": {Granted: true, Optimistic: true},
		"wait":       {WaitUntil: time.Now()},
	}

	for want, r := range tests {
		if got := acquireResult(&r); got != want {
			t.Errorf("acquireResult(%+v) = %q, want %q", r, got, want)
		}
	}
}

// TestAcquireBudget_RefusesAnotherAppsBudget: a budget worker serves its
// own App only, before any Temporal call.
func TestAcquireBudget_RefusesAnotherAppsBudget(t *testing.T) {
	t.Parallel()

	b := NewBudget(nil, workflows.AppEval, "repo-guardian-eval", 0.1)

	if _, err := b.AcquireBudget(t.Context(), &workflows.AcquireInput{App: workflows.AppRemediate, InstallationID: 7}); err == nil {
		t.Error("AcquireBudget for the Remediation App on the Evaluation App's worker = nil, want a refusal")
	}
}

// logRecorder captures log records for the E4 contract.
type logRecorder struct {
	mu      sync.Mutex
	records []slog.Record
}

func (*logRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (h *logRecorder) Handle(_ context.Context, r slog.Record) error { //nolint:gocritic // slog.Handler's signature
	h.mu.Lock()
	defer h.mu.Unlock()

	h.records = append(h.records, r)

	return nil
}

func (h *logRecorder) WithAttrs(attrs []slog.Attr) slog.Handler { return &attrHandler{h, attrs} }
func (h *logRecorder) WithGroup(string) slog.Handler            { return h }

// attrHandler folds With attributes into each record.
type attrHandler struct {
	*logRecorder

	attrs []slog.Attr
}

func (a *attrHandler) Handle(ctx context.Context, r slog.Record) error { //nolint:gocritic // slog.Handler's signature
	r.AddAttrs(a.attrs...)

	return a.logRecorder.Handle(ctx, r)
}

func (a *attrHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &attrHandler{a.logRecorder, append(append([]slog.Attr{}, a.attrs...), attrs...)}
}

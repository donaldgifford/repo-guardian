package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/api"
	"github.com/donaldgifford/repo-guardian/internal/api/gen"
	"github.com/donaldgifford/repo-guardian/internal/store"
)

// fakeRechecker records re-checks and answers err.
type fakeRechecker struct {
	mu    sync.Mutex
	calls []int64
	err   error
}

func (f *fakeRechecker) Recheck(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, id)

	return f.err
}

func (f *fakeRechecker) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.calls)
}

func problemType(t *testing.T, body []byte) string {
	t.Helper()

	var p gen.Problem
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("decode problem %s: %v", body, err)
	}

	return p.Type
}

func TestEvaluate_WithoutTemporalIsNotImplementedAfterVisibility(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	token := h.issuer.Valid(t, "team-web")

	if resp := h.client.Post("/repositories/3/evaluate", token); resp.Status != http.StatusNotFound {
		t.Errorf("evaluate an invisible repository = %d, want 404 before 501", resp.Status)
	}

	if resp := h.client.Post("/repositories/1/evaluate", token); resp.Status != http.StatusNotImplemented {
		t.Errorf("evaluate without a Temporal client = %d, want 501", resp.Status)
	}
}

func TestEvaluate_SignalsOncePerMinute(t *testing.T) {
	t.Parallel()

	rc := &fakeRechecker{}
	clock := now

	var mu sync.Mutex

	h := newHarness(t, func(o *api.Options) {
		o.Rechecker = rc
		o.Now = func() time.Time {
			mu.Lock()
			defer mu.Unlock()

			return clock
		}
	})
	token := h.issuer.Valid(t, "team-web")

	for range 2 {
		if resp := h.client.Post("/repositories/1/evaluate", token); resp.Status != http.StatusAccepted {
			t.Fatalf("evaluate = %d: %s, want 202", resp.Status, resp.Body)
		}
	}

	if rc.count() != 1 {
		t.Errorf("two requests within the minute sent %d signals, want 1", rc.count())
	}

	mu.Lock()
	clock = now.Add(time.Minute)
	mu.Unlock()

	if resp := h.client.Post("/repositories/1/evaluate", token); resp.Status != http.StatusAccepted || rc.count() != 2 {
		t.Errorf("evaluate a minute later = %d with %d signals, want 202 and a second signal", resp.Status, rc.count())
	}
}

func TestEvaluate_Conflicts(t *testing.T) {
	t.Parallel()

	rc := &fakeRechecker{err: api.ErrWorkflowMissing}
	h := newHarness(t, func(o *api.Options) { o.Rechecker = rc })
	h.reader.repos = append(h.reader.repos, store.Repository{ID: 4, Org: "acme-web", Name: "old", DiscoveredAt: now})
	token := h.issuer.Valid(t, "team-web")

	resp := h.client.Post("/repositories/4/evaluate", token)
	if resp.Status != http.StatusConflict || problemType(t, resp.Body) != "parked" {
		t.Errorf("evaluate a parked repository = %d %s, want 409 parked", resp.Status, resp.Body)
	}

	if rc.count() != 0 {
		t.Errorf("a parked repository was signalled %d times", rc.count())
	}

	for range 2 {
		resp = h.client.Post("/repositories/1/evaluate", token)
		if resp.Status != http.StatusConflict || problemType(t, resp.Body) != "workflow_missing" {
			t.Errorf("evaluate with no workflow = %d %s, want 409 workflow_missing", resp.Status, resp.Body)
		}
	}

	// A failed signal releases its claim: the retry inside the minute
	// reaches Temporal instead of being swallowed as a duplicate.
	if rc.count() != 2 {
		t.Errorf("two failed requests sent %d signals, want 2", rc.count())
	}

	rc.err = errors.New("temporal unavailable")
	if resp := h.client.Post("/repositories/1/evaluate", token); resp.Status != http.StatusInternalServerError {
		t.Errorf("evaluate on a signal error = %d, want 500", resp.Status)
	}
}

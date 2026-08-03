package checker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/policy"
)

// TestCheckRepo_ThrottledErrorSurvivesWrapChain is the permanent
// DESIGN-0021 Phase 3 chain invariant (IMPL-0022 task 3.4): a
// rate-limit deferral signal raised below Engine.CheckRepo must stay
// recoverable via github.AsThrottled at the worker boundary, through
// every wrap on the way — url.Error (net/http), go-github's BareDo,
// our client's %w, and the engine's %w. A break in any link makes
// the worker nack instead of defer, which reads as ordinary retry
// rather than a bug — so this test drives a REAL client against a
// REAL httptest server rather than short-circuiting with a fake.
//
// The signal takes two real shapes, both exercised here:
//
//   - remaining at or below the reserve threshold but nonzero — our
//     transport raises *github.ThrottledError before sending;
//   - remaining zero — go-github's own client-side pre-check
//     short-circuits ABOVE our transport with *gh.RateLimitError
//     (unbypassable in v68), which AsThrottled normalises. This
//     second shape was discovered by this very test; a plain
//     errors.As on ThrottledError misses it.
//
// Timeline in both cases: request 1 (GetRepository) succeeds and
// primes both rate caches; request 2 (ListOpenPullRequests) defers
// before it is ever sent.
func TestCheckRepo_ThrottledErrorSurvivesWrapChain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		remaining int
		// wantDirectAs: the transport's own ThrottledError is
		// recoverable with plain errors.As. False for the exhausted
		// case, where the signal is go-github's RateLimitError and
		// only AsThrottled recovers it.
		wantDirectAs bool
	}{
		{
			name:         "reserve threshold defers via transport ThrottledError",
			remaining:    20,
			wantDirectAs: true,
		},
		{
			name:      "exhausted budget defers via go-github precheck",
			remaining: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			resetAt := time.Unix(time.Now().Add(50*time.Minute).Unix(), 0)

			var apiCalls atomic.Int32

			mux := http.NewServeMux()
			mux.HandleFunc("GET /api/v3/repos/o/r", func(w http.ResponseWriter, _ *http.Request) {
				apiCalls.Add(1)
				w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(tt.remaining))
				w.Header().Set("X-RateLimit-Limit", "5000")
				w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetAt.Unix(), 10))
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"name":"r","full_name":"o/r","default_branch":"main","archived":false,"fork":false}`)
			})
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				apiCalls.Add(1)
				t.Errorf("unexpected API call past the throttle: %s %s", r.Method, r.URL.Path)
				w.WriteHeader(http.StatusInternalServerError)
			})

			server := httptest.NewServer(mux)
			defer server.Close()

			client, err := ghclient.NewClientForBaseURL(server.URL, server.Client().Transport, slog.Default(), 0.10)
			if err != nil {
				t.Fatalf("NewClientForBaseURL: %v", err)
			}

			engine := testPolicyEngine(policy.BuiltinDefaults())

			_, checkErr := engine.CheckRepo(context.Background(), client, "o", "r")
			if checkErr == nil {
				t.Fatal("CheckRepo() = nil error, want throttle deferral surfacing through the chain")
			}

			thr, ok := ghclient.AsThrottled(checkErr)
			if !ok {
				t.Fatalf(
					"AsThrottled(CheckRepo() error = %v) = false, want the deferral signal recovered — a broken link in the wrap chain turns deferrals into nacks",
					checkErr,
				)
			}

			if thr.Remaining != tt.remaining || thr.Limit != 5000 || !thr.ResetAt.Equal(resetAt) {
				t.Errorf("AsThrottled() = %+v, want remaining=%d limit=5000 resetAt=%v", thr, tt.remaining, resetAt)
			}

			var direct *ghclient.ThrottledError
			if gotDirect := errors.As(checkErr, &direct); gotDirect != tt.wantDirectAs {
				t.Errorf("errors.As(*ThrottledError) = %v, want %v", gotDirect, tt.wantDirectAs)
			}

			// The engine-side wrap proves the error travelled the real
			// path (CheckRepo → ListOpenPullRequests), not a shortcut.
			if !strings.Contains(checkErr.Error(), "listing open PRs") {
				t.Errorf("CheckRepo() error = %q, want the engine's %q wrap in the chain", checkErr, "listing open PRs")
			}

			if calls := apiCalls.Load(); calls != 1 {
				t.Errorf("api calls = %d, want 1 (GetRepository primes the caches; the deferred request is never sent)", calls)
			}
		})
	}
}

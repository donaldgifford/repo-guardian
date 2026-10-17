//go:build integration && perf

package postgres_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/api"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres"
)

// perfSeed is IMPL-0025 15.10's fixture on the controls schema
// (IMPL-0029 7.4): 20 orgs, 20k repositories, 10 controls each (200k
// assignments and results, 1 in 7 failing, 1 in 11 not applicable), 50
// result events per repository (1M), and 20 checks per repository,
// bulk-inserted in SQL as the owner.
const perfSeed = `
INSERT INTO repositories (org, name, last_checked_at)
SELECT 'org-' || (i % 20), 'repo-' || i, now() FROM generate_series(1, 20000) i;

INSERT INTO repository_policy_state (repository_id, state, max_open_prs, reopen_after, policy_version, resolved_at)
SELECT r.id, 'managed', 3, '336 hours', 'c:1', now() FROM repositories r WHERE r.org LIKE 'org-%';

INSERT INTO control_assignments (repository_id, control_id, control_version, revision, state, decision, baseline_control,
                                 source, ordinal, requested_mode, effective_mode, mode_source, epoch, policy_version, resolved_at)
SELECT r.id, 'control-' || n, 1, 'r1', 'active', 'baseline', 'control-' || n, 'enterprise', n,
       'evaluate', 'evaluate', 'enterprise', 1, 'c:1', now()
FROM repositories r CROSS JOIN generate_series(1, 10) n WHERE r.org LIKE 'org-%';

INSERT INTO control_results (repository_id, control_id, control_version, revision, assignment_epoch, status, fingerprint,
                             intent_revision, eval_generation, evaluated_sha, evaluated_at, last_changed_at)
SELECT r.id, 'control-' || n, 1, 'r1', 1,
       CASE WHEN (r.id + n) % 7 = 0 THEN 'non_compliant' WHEN (r.id + n) % 11 = 0 THEN 'not_applicable' ELSE 'compliant' END,
       'f', 'i', 1, 'sha', now(), now() - ((r.id % 90) || ' days')::interval
FROM repositories r CROSS JOIN generate_series(1, 10) n WHERE r.org LIKE 'org-%';

INSERT INTO result_events (repository_id, control_id, control_version, revision, assignment_epoch, from_status, to_status,
                           eval_generation, occurred_at)
SELECT r.id, 'control-' || (e % 10 + 1), 1, 'r1', 1, 'compliant', 'non_compliant', e, now() - (e || ' hours')::interval
FROM repositories r CROSS JOIN generate_series(1, 50) e WHERE r.org LIKE 'org-%';

INSERT INTO checks (repository_id, check_key, trigger, outcome, policy_version, started_at, finished_at)
SELECT r.id, r.id || '/' || c, 'schedule', 'success', 'c:1', now() - (c || ' hours')::interval, now() - (c || ' hours')::interval
FROM repositories r CROSS JOIN generate_series(1, 20) c WHERE r.org LIKE 'org-%';

ANALYZE;
`

// TestAPIPerf is run by hand (make test does not include the perf tag):
//
//	go test -tags 'integration perf' -run TestAPIPerf -v ./internal/store/postgres/
//
// It reports p50/p99 per endpoint over 200 requests through the full
// handler (auth disabled, so every request reads the whole fleet — the
// worst case) and fails when a p99 exceeds 100ms.
func TestAPIPerf(t *testing.T) {
	f := newAPIFixture(t)

	start := time.Now()
	run(t, f.dsns.Owner, perfSeed)

	t.Logf("seeded in %s", time.Since(start).Round(time.Second))

	pool, err := postgres.NewReadOnlyPool(t.Context(), f.dsns.ReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	h, err := api.New(&api.Options{
		Reader: postgres.NewAPIReader(pool), Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}

	const requests = 200

	for _, path := range []string{
		"/orgs", "/repositories?limit=50", "/repositories?org=org-3&limit=50", "/repositories/5000/events?limit=50",
		"/repositories/5000/evaluations?limit=50",
	} {
		var d []time.Duration

		for range requests {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, api.BaseURL+path, http.NoBody)
			rec := httptest.NewRecorder()

			begin := time.Now()
			h.ServeHTTP(rec, req)
			d = append(d, time.Since(begin))

			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s = %d: %s", path, rec.Code, rec.Body)
			}
		}

		slices.Sort(d)
		p50, p99 := d[len(d)/2], d[len(d)*99/100]
		t.Logf("%-55s p50 %6.1fms  p99 %6.1fms", path, ms(p50), ms(p99))

		if p99 > 100*time.Millisecond {
			t.Errorf("%s p99 = %s, over the 100ms target", path, p99)
		}
	}
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

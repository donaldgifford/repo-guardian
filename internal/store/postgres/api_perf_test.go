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

// perfSeed is IMPL-0025 15.10's fixture: 20 orgs, 20k repositories, 10
// rules each (200k findings, 1 in 7 failing with a PR), and 50 finding
// events per repository (1M), bulk-inserted in SQL.
const perfSeed = `
INSERT INTO installations (installation_id, account_login)
SELECT 1000 + o, 'org-' || o FROM generate_series(0, 19) o;

INSERT INTO repositories (org, name, installation_id, last_check_outcome, policy_version)
SELECT 'org-' || (i % 20), 'repo-' || i, 1000 + (i % 20), 'success', 'v2:perf' FROM generate_series(1, 20000) i;

INSERT INTO findings (repository_id, rule_kind, rule_name, status, reason, remediation, evidence,
                      status_since, last_evaluated_at, policy_version)
SELECT r.id, 'file', 'rule-' || n,
       CASE WHEN (r.id + n) % 7 = 0 THEN 'non_compliant' WHEN (r.id + n) % 11 = 0 THEN 'not_applicable' ELSE 'compliant' END,
       CASE WHEN (r.id + n) % 7 = 0 THEN 'file_missing' WHEN (r.id + n) % 11 = 0 THEN 'out_of_scope_rule' END,
       CASE WHEN (r.id + n) % 7 = 0 THEN 'pr_open' ELSE 'none' END,
       CASE WHEN (r.id + n) % 7 = 0
            THEN jsonb_build_object('paths_checked', jsonb_build_array('X'),
                   'pr', jsonb_build_object('number', r.id, 'url', 'https://example/pull/' || r.id,
                                            'created_at', to_char(now() - ((r.id % 60) || ' days')::interval, 'YYYY-MM-DD"T"HH24:MI:SS"Z"')))
            ELSE '{}'::jsonb END,
       now() - ((r.id % 90) || ' days')::interval, now(), 'v2:perf'
FROM repositories r CROSS JOIN generate_series(1, 10) n;

INSERT INTO finding_events (repository_id, rule_kind, rule_name, from_status, to_status, policy_version, occurred_at)
SELECT r.id, 'file', 'rule-' || (e % 10 + 1), 'compliant', 'non_compliant', 'v2:perf', now() - (e || ' hours')::interval
FROM repositories r CROSS JOIN generate_series(1, 50) e;

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
	f := newV2Fixture(t)

	start := time.Now()
	if _, err := f.pool.Exec(t.Context(), perfSeed); err != nil {
		t.Fatal(err)
	}

	t.Logf("seeded in %s", time.Since(start).Round(time.Second))

	pool, err := postgres.NewReadOnlyPool(t.Context(), f.dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	h, err := api.New(&api.Options{
		Reader: postgres.NewAPIReader(pool), StaleAfter: 30 * 24 * time.Hour, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}

	const requests = 200

	for _, path := range []string{
		"/summary", "/rules", "/orgs", "/findings?status=non_compliant&org=org-3&limit=50", "/findings?pr_stale=true&limit=50",
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

//go:build integration && perf

package postgres_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/donaldgifford/repo-guardian/internal/api"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres"
)

// perfSeed is IMPL-0025 15.10's fixture on the controls schema
// (IMPL-0029 7.4): 20 orgs, 20k repositories, 10 controls each (200k
// assignments and results, 1 in 7 failing, 1 in 11 not applicable), 50
// result events per repository (1M), 20 checks per repository, and for
// IMPL-0030 7.14 one rule result per result (failing where the result
// does) and 90 days of daily compliance snapshots per org and control,
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

INSERT INTO rule_results (repository_id, control_id, rule_id, status, remediate, evidence, evidence_kind, evidence_version)
SELECT cr.repository_id, cr.control_id, 'exists',
       CASE cr.status WHEN 'non_compliant' THEN 'fail' WHEN 'not_applicable' THEN 'not_applicable' ELSE 'pass' END, true,
       '{"evidence_kind":"file.exists","evidence_version":1}', 'file.exists', 1
FROM control_results cr;

INSERT INTO compliance_snapshots (org, control_id, decision, snapshot_at, compliant, non_compliant, pending, stale,
                                  unmeasurable_access_denied, unmeasurable_suspended, unmeasurable_conflict, not_applicable,
                                  unknown, excluded, assigned, percent, coverage)
SELECT 'org-' || o, 'control-' || n, 'baseline', date_trunc('day', now()) - (d || ' days')::interval + interval '23 hours',
       780, 140, 0, 0, 0, 0, 0, 80, 0, 0, 1000, 84.7, 100.0
FROM generate_series(0, 19) o CROSS JOIN generate_series(1, 10) n CROSS JOIN generate_series(0, 89) d;

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
		"/results?limit=50", "/results?bucket=non_compliant&limit=50", "/results?control=control-3&limit=50",
		"/results?rule=exists&bucket=non_compliant&limit=50", "/results?org=org-3&limit=50",
		"/results?decision=baseline&limit=50", "/results?sort=since_desc&limit=50", "/results?sort=pr_age&limit=50",
		"/results?pr_stale=true&limit=50",
		"/compliance/history?days=90&aggregate=fleet", "/compliance/history?days=90&aggregate=org",
		"/compliance/history?days=90&aggregate=control", "/summary",
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

	explainResults(t, f.dsns.ReadOnly)
}

// explainResults logs the plan of /results' default page, its heaviest
// shape (non-compliant across the fleet), for IMPL-0030 7.14.
func explainResults(t *testing.T, dsn string) {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(t.Context())

	rows, err := conn.Query(t.Context(), `EXPLAIN (ANALYZE, BUFFERS) `+resultsQuery(t))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var plan strings.Builder

	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}

		plan.WriteString(line + "\n")
	}

	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	t.Logf("EXPLAIN /results?bucket=non_compliant&limit=50:\n%s", plan.String())
}

// resultsQuery is APIResults from its .sql file with its parameters
// bound as literals: the whole fleet, bucket non_compliant, first page.
func resultsQuery(t *testing.T) string {
	t.Helper()

	b, err := os.ReadFile("queries_controls/api_results.sql")
	if err != nil {
		t.Fatal(err)
	}

	q := string(b)
	q = q[strings.Index(q, "-- name: APIResults :many"):strings.Index(q, "-- name: APIResultsTotal")]
	q = strings.TrimSuffix(strings.TrimSpace(q), ";")

	values := map[string]string{
		"scope_all": "true", "scope_orgs": "'{}'", "bucket": "'non_compliant'", "stale_before": "now()",
		"pr_age": "false", "descending": "false", "has_after": "false", "after_at": "now()",
		"after_repository_id": "0", "after_control_id": "''", "page_limit": "51",
	}

	return regexp.MustCompile(`sqlc\.narg\((\w+)\)|@(\w+)`).ReplaceAllStringFunc(q, func(m string) string {
		name := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(m, "sqlc.narg("), "@"), ")")
		if v, ok := values[name]; ok {
			return v
		}

		return "NULL"
	})
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

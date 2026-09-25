//go:build integration

package postgres_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/store/postgres"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres/pgtest"
)

var (
	v1T     = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	v1Since = v1T.Add(-30 * 24 * time.Hour)
	v1Upd   = v1T.Add(-time.Hour)
)

// seedV1Fleet writes a representative v1 dataset: every park reason
// plus one unknown, never-checked repositories, a case collision, a
// two-owner installation, rule_state rows with and without
// actionable_since, and compliance snapshots.
func seedV1Fleet(t *testing.T, db *sql.DB) {
	t.Helper()

	rs := `INSERT INTO repo_state (installation_id, owner, repo, last_checked_at, last_check_status, last_error,
	                              policy_version, active, catalog_parse_ok) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`
	for _, r := range []struct {
		inst          int64
		owner, repo   string
		checked       *time.Time
		status, error string
		active        bool
	}{
		{7, "acme", "checked", &v1T, "success", "", true},
		{7, "acme", "denied", &v1T, "error", "GET 403 Resource not accessible", false},
		{7, "acme", "archived", &v1T, "skipped", "archived", false},
		{7, "acme", "forked", &v1T, "skipped", "fork", false},
		{7, "acme", "mystery", &v1T, "skipped", "empty", false},
		{7, "acme", "fresh1", nil, "pending", "", true},
		{7, "acme", "fresh2", nil, "pending", "", true},
		{7, "acme", "dup", ptrTime(v1T.Add(-2 * time.Hour)), "success", "", true},
		{7, "acme", "DUP", ptrTime(v1T.Add(-time.Hour)), "success", "", true},
		{8, "old-login", "a", ptrTime(v1T.Add(-5 * time.Hour)), "success", "", true},
		{8, "new-login", "b", &v1T, "success", "", true},
	} {
		var lastErr *string
		if r.error != "" {
			lastErr = &r.error
		}

		if _, err := db.Exec(rs, r.inst, r.owner, r.repo, r.checked, r.status, lastErr, "abc", r.active, true); err != nil {
			t.Fatalf("seed repo_state %s: %v", r.repo, err)
		}
	}

	rule := `INSERT INTO rule_state (installation_id, owner, repo, rule_name, rule_kind, actionable, actionable_since,
	                                policy_version, updated_at) VALUES (7, 'acme', $1, $2, $3, $4, $5, 'abc', $6)`
	for _, r := range []struct {
		repo, name, kind string
		actionable       bool
		since            *time.Time
	}{
		{"checked", "codeowners", "file", true, &v1Since},
		{"checked", "renovate", "file", true, nil},
		{"checked", "vuln_alerts", "setting", false, nil},
		{"dup", "codeowners", "file", true, &v1Since},
		{"DUP", "codeowners", "file", false, nil},
	} {
		if _, err := db.Exec(rule, r.repo, r.name, r.kind, r.actionable, r.since, v1Upd); err != nil {
			t.Fatalf("seed rule_state %s/%s: %v", r.repo, r.name, err)
		}
	}

	snap := `INSERT INTO compliance_snapshot (org, rule_name, actionable_count, tracked_count, snapshot_at)
	         VALUES ('acme', $1, $2, $3, $4)`
	for _, s := range []struct {
		rule              string
		actionable, total int
	}{{"codeowners", 3, 10}, {"vuln_alerts", 0, 10}, {"retired_rule", 1, 4}} {
		if _, err := db.Exec(snap, s.rule, s.actionable, s.total, v1T); err != nil {
			t.Fatalf("seed snapshot %s: %v", s.rule, err)
		}
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

func count(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()

	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}

	return n
}

// migratedV1 is a seeded v1 database after a dry run and the real run.
type migratedV1 struct {
	dsn    string
	db     *sql.DB
	up     func(context.Context) (int, error)
	dry    *postgres.BackfillReport
	before time.Time
}

func newMigratedV1(t *testing.T) *migratedV1 {
	t.Helper()

	ctx := postgres.WithBackfillFreshness(context.Background(), 24*time.Hour)
	dsn := pgtest.Start(t)
	pgtest.SeedV1(t, dsn)

	db, up := openMigrator(t, dsn)
	seedV1Fleet(t, db)

	dry, err := postgres.DryRun(ctx, db, "seed")
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}

	for _, table := range []string{"repositories", "v2_meta", "goose_db_version"} {
		if count(t, db, `SELECT count(*) FROM pg_class WHERE relname = $1 AND relkind = 'r'`, table) != 0 {
			t.Fatalf("dry run left table %s behind", table)
		}
	}

	m := &migratedV1{dsn: dsn, db: db, dry: dry, before: time.Now()}

	upCtx := func(context.Context) (int, error) { return up(ctx) }
	m.up = upCtx

	if _, err := m.up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	return m
}

func TestBackfill_DryRunMatchesRealRun(t *testing.T) {
	t.Parallel()

	m := newMigratedV1(t)

	got := map[string]int{
		"installations": count(t, m.db, `SELECT count(*) FROM installations`),
		"repositories":  count(t, m.db, `SELECT count(*) FROM repositories`),
		"findings":      count(t, m.db, `SELECT count(*) FROM findings`),
		"events":        count(t, m.db, `SELECT count(*) FROM finding_events`),
		"snapshots":     count(t, m.db, `SELECT count(*) FROM compliance_snapshots`),
	}
	want := map[string]int{
		"installations": m.dry.Installations, "repositories": m.dry.Repositories, "findings": m.dry.Findings,
		"events": m.dry.Events, "snapshots": m.dry.Snapshots,
	}

	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s: real %d, dry run %d", k, got[k], w)
		}
	}

	if !m.dry.Ran || m.dry.Repositories != 10 || m.dry.NeverChecked != 2 {
		t.Errorf("dry run report = %+v", m.dry)
	}

	if len(m.dry.Collisions) != 1 || m.dry.Collisions[0] != "7/acme/dup" {
		t.Errorf("collisions = %v, want [7/acme/dup]", m.dry.Collisions)
	}

	if len(m.dry.MultiOwner) != 1 || m.dry.MultiOwner[0] != "8: new-login (old-login)" {
		t.Errorf("multi owner = %v", m.dry.MultiOwner)
	}

	wantPark := map[string]int{"access_denied": 1, "archived": 1, "fork": 1, "unknown": 1}
	for k, v := range wantPark {
		if m.dry.ParkReasons[k] != v {
			t.Errorf("park histogram = %v, want %v", m.dry.ParkReasons, wantPark)

			break
		}
	}
}

func TestBackfill_RepositoryMappings(t *testing.T) {
	t.Parallel()

	m := newMigratedV1(t)

	park := func(name string) string {
		t.Helper()

		var reason sql.NullString
		if err := m.db.QueryRow(`SELECT park_reason FROM repositories WHERE name = $1`, name).Scan(&reason); err != nil {
			t.Fatalf("read %s: %v", name, err)
		}

		return reason.String
	}

	for name, want := range map[string]string{
		"checked": "", "denied": "access_denied", "archived": "archived", "forked": "fork", "mystery": "unknown",
	} {
		if got := park(name); got != want {
			t.Errorf("%s park_reason = %q, want %q", name, got, want)
		}
	}

	var (
		pv      string
		due     time.Time
		outcome string
		catalog bool
	)
	if err := m.db.QueryRow(`SELECT policy_version, next_due_at, last_check_outcome, catalog_parse_ok
		FROM repositories WHERE name = 'checked'`).Scan(&pv, &due, &outcome, &catalog); err != nil {
		t.Fatalf("read checked: %v", err)
	}

	if pv != "v1:abc" || !due.Equal(v1T.Add(24*time.Hour)) || outcome != "success" || !catalog {
		t.Errorf("checked = %s %v %s %v", pv, due, outcome, catalog)
	}

	var d1, d2 time.Time
	if err := m.db.QueryRow(`SELECT min(next_due_at), max(next_due_at) FROM repositories
		WHERE last_checked_at IS NULL`).Scan(&d1, &d2); err != nil {
		t.Fatalf("read never-checked: %v", err)
	}

	if d1.Before(m.before) || d2.After(time.Now().Add(24*time.Hour)) {
		t.Errorf("never-checked due %v..%v outside one freshness window from %v", d1, d2, m.before)
	}

	if gap := d2.Sub(d1); gap < 11*time.Hour || gap > 13*time.Hour {
		t.Errorf("never-checked spread gap = %v, want ~12h (two rows evenly over 24h)", gap)
	}

	if n := count(t, m.db, `SELECT count(*) FROM repositories WHERE lower(name) = 'dup'`); n != 1 {
		t.Fatalf("case collision kept %d rows", n)
	}

	if n := count(t, m.db, `SELECT count(*) FROM repositories WHERE name = 'DUP'`); n != 1 {
		t.Error("case collision did not keep the most recently checked row")
	}

	var login string
	if err := m.db.QueryRow(`SELECT account_login FROM installations WHERE installation_id = 8`).Scan(&login); err != nil {
		t.Fatalf("read installation: %v", err)
	}

	if login != "new-login" {
		t.Errorf("installation 8 login = %q, want the most recent owner", login)
	}

	if n := count(t, m.db, `SELECT count(*) FROM policy_versions`); n != 0 {
		t.Errorf("policy_versions rows = %d, want 0", n)
	}
}

func TestBackfill_FindingMappings(t *testing.T) {
	t.Parallel()

	m := newMigratedV1(t)

	type row struct {
		kind, status string
		reason       sql.NullString
		since        time.Time
		evidence     []byte
	}

	read := func(repo, rule string) row {
		t.Helper()

		var r row
		if err := m.db.QueryRow(`SELECT f.rule_kind, f.status, f.reason, f.status_since, f.evidence
			FROM findings f JOIN repositories r ON r.id = f.repository_id
			WHERE r.name = $1 AND f.rule_name = $2`, repo, rule).
			Scan(&r.kind, &r.status, &r.reason, &r.since, &r.evidence); err != nil {
			t.Fatalf("read %s/%s: %v", repo, rule, err)
		}

		return r
	}

	withSince := read("checked", "codeowners")

	var ev struct {
		Since *time.Time `json:"v1_actionable_since"`
	}
	if err := json.Unmarshal(withSince.evidence, &ev); err != nil {
		t.Fatalf("evidence: %v", err)
	}

	if withSince.status != "non_compliant" || withSince.reason.String != "migrated_from_v1" ||
		!withSince.since.Equal(v1Since) || ev.Since == nil || !ev.Since.Equal(v1Since) {
		t.Errorf("actionable with since = %+v (evidence %s)", withSince, withSince.evidence)
	}

	if r := read("checked", "renovate"); r.status != "non_compliant" || !r.since.Equal(v1Upd) {
		t.Errorf("actionable without since = %+v, want status_since = updated_at", r)
	}

	if r := read("checked", "vuln_alerts"); r.status != "compliant" || r.kind != "setting" || r.reason.Valid || !r.since.Equal(v1Upd) {
		t.Errorf("compliant setting = %+v", r)
	}

	if r := read("DUP", "codeowners"); r.status != "compliant" {
		t.Errorf("kept collision row finding = %+v, want its own compliant row", r)
	}

	if n := count(t, m.db, `SELECT count(*) FROM findings`); n != 4 {
		t.Errorf("findings = %d, want 4 (the dropped collision row's rule_state is not migrated)", n)
	}

	if n := count(t, m.db, `SELECT count(*) FROM finding_events
		WHERE to_reason = 'migrated_from_v1' AND from_status IS NULL`); n != 4 {
		t.Errorf("created events = %d, want one per finding", n)
	}

	kinds := map[string]string{}

	rows, err := m.db.Query(`SELECT rule_name, rule_kind, compliant, non_compliant FROM compliance_snapshots`)
	if err != nil {
		t.Fatalf("read snapshots: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			name, kind string
			c, nc      int
		)
		if err := rows.Scan(&name, &kind, &c, &nc); err != nil {
			t.Fatalf("scan snapshot: %v", err)
		}

		kinds[name] = kind

		if name == "codeowners" && (c != 7 || nc != 3) {
			t.Errorf("codeowners snapshot = %d/%d, want 7 compliant, 3 non-compliant", c, nc)
		}
	}

	want := map[string]string{"codeowners": "file", "vuln_alerts": "setting", "retired_rule": "file"}
	for k, v := range want {
		if kinds[k] != v {
			t.Errorf("snapshot kinds = %v, want %v", kinds, want)

			break
		}
	}
}

func TestBackfill_RerunIsNoOp(t *testing.T) {
	t.Parallel()

	m := newMigratedV1(t)
	before := count(t, m.db, `SELECT count(*) FROM finding_events`)

	n, err := m.up(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("second migrate applied %d, err %v; want 0", n, err)
	}

	rep, err := postgres.DryRun(context.Background(), m.db, "seed")
	if err != nil || rep != nil {
		t.Errorf("dry run after migrate = %+v, %v; want nothing pending", rep, err)
	}

	if after := count(t, m.db, `SELECT count(*) FROM finding_events`); after != before {
		t.Errorf("events %d -> %d on re-run", before, after)
	}
}

//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/api"
	"github.com/donaldgifford/repo-guardian/internal/api/apitest"
	"github.com/donaldgifford/repo-guardian/internal/api/gen"
	"github.com/donaldgifford/repo-guardian/internal/findings"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres"
)

// seedTwoOrgs adds acme-web (two repositories, one failing, one with an
// open PR) and acme-pay (one compliant, one parked) to f.
func seedTwoOrgs(t *testing.T, f *v2Fixture) {
	t.Helper()

	ctx := context.Background()
	prAt := t0.Add(-40 * 24 * time.Hour)

	repo := func(org, name string) int64 {
		t.Helper()

		res, err := f.store.UpsertDiscovered(ctx, &store.DiscoveredRepo{Org: org, Name: name, InstallationID: testInstallation})
		if err != nil {
			t.Fatal(err)
		}

		return res.ID
	}

	record := func(id int64, key string, outcomes ...store.Outcome) {
		t.Helper()

		if _, err := f.store.RecordCheck(ctx, &store.CheckRecord{
			Key: key, RepositoryID: id, InstallationID: testInstallation, Trigger: store.TriggerSchedule,
			PolicyVersion: "v2:test", StartedAt: t0, FinishedAt: t0, Outcomes: outcomes,
		}); err != nil {
			t.Fatal(err)
		}
	}

	withPR := missing("codeowners", "CODEOWNERS")
	withPR.Remediation = findings.RemediationPROpen
	withPR.PR = &findings.PREvidence{Number: 7, URL: "https://github.com/acme-web/b/pull/7", CreatedAt: prAt}

	record(f.repoID, "w1", compliant("codeowners"))
	record(repo("acme-web", "b"), "w2", withPR)
	record(repo("acme-pay", "c"), "p1", compliant("codeowners"))

	parked := repo("Acme-Pay", "d")
	if err := f.store.Park(ctx, parked, store.ParkArchived, true); err != nil {
		t.Fatal(err)
	}
}

func TestAPIReader_SummaryValidatesAndIsScopedInSQL(t *testing.T) {
	t.Parallel()

	f := newV2Fixture(t)
	seedTwoOrgs(t, f)

	pool, err := postgres.NewReadOnlyPool(t.Context(), f.dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	reader := postgres.NewAPIReader(pool)
	c := apitest.New(t, &api.Options{
		Reader: reader, StaleAfter: 30 * 24 * time.Hour, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { return t0 },
	})

	resp := c.Get("/summary", "")
	if resp.Status != http.StatusOK {
		t.Fatalf("summary = %d: %s", resp.Status, resp.Body)
	}

	var all gen.Summary
	if err := json.Unmarshal(resp.Body, &all); err != nil {
		t.Fatal(err)
	}

	if all.Tracked != 3 || all.Findings.Compliant != 2 || all.Findings.NonCompliant != 1 || all.StalePrs != 1 ||
		len(all.Parked) != 1 || all.Parked[0].Reason != "archived" {
		t.Errorf("all-orgs summary = %+v", all)
	}

	if all.CompliantPercent == nil || *all.CompliantPercent != 66.6 {
		t.Errorf("compliant_percent = %v, want 66.6", all.CompliantPercent)
	}

	// Scoping is SQL's job: the reader alone must honor it.
	pay, err := reader.Summary(t.Context(), store.ScopeOrgs("ACME-PAY"))
	if err != nil {
		t.Fatal(err)
	}

	if pay.Tracked != 1 || pay.Findings.NonCompliant != 0 || len(pay.OpenPRs) != 0 || len(pay.Parked) != 1 {
		t.Errorf("acme-pay summary = %+v, want one tracked, one parked, no PRs", pay)
	}

	none, err := reader.Summary(t.Context(), store.ScopeOrgs())
	if err != nil {
		t.Fatal(err)
	}

	if none.Tracked != 0 || len(none.Parked) != 0 || none.CompliantPercent != nil {
		t.Errorf("empty-scope summary = %+v, want nothing", none)
	}
}

func TestReadOnlyPool_RefusesWrites(t *testing.T) {
	t.Parallel()

	f := newV2Fixture(t)

	pool, err := postgres.NewReadOnlyPool(t.Context(), f.dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	_, err = pool.Exec(t.Context(), `INSERT INTO installations (installation_id, account_login) VALUES (99, 'x')`)
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Errorf("INSERT through the read-only pool = %v, want a read-only transaction error", err)
	}

	var timeout string
	if err := pool.QueryRow(t.Context(), `SHOW statement_timeout`).Scan(&timeout); err != nil || timeout != "5s" {
		t.Errorf("statement_timeout = %q (%v), want 5s", timeout, err)
	}
}

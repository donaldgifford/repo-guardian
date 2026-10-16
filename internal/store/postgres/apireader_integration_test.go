//go:build integration

package postgres_test

import (
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
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres"
)

func TestAPIReader_OrgsValidateAndAreScopedInSQL(t *testing.T) {
	t.Parallel()

	f := newAPIFixture(t)
	seedTwoOrgs(t, f)

	pool, err := postgres.NewReadOnlyPool(t.Context(), f.dsns.Evaluator)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	reader := postgres.NewAPIReader(pool)
	c := apitest.New(t, &api.Options{
		Reader: reader, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { return t0 },
	})

	resp := c.Get("/orgs", "")
	if resp.Status != http.StatusOK {
		t.Fatalf("orgs = %d: %s", resp.Status, resp.Body)
	}

	var all gen.OrgList
	if err := json.Unmarshal(resp.Body, &all); err != nil {
		t.Fatal(err)
	}

	if len(all.Items) != 3 {
		t.Fatalf("all-orgs list = %+v, want acme, acme-pay and acme-web", all.Items)
	}

	pay, web := all.Items[1], all.Items[2]
	// d is archived: parked, and out of compliance with its results cleared.
	if pay.Tracked != 1 || pay.Parked != 1 || pay.Counts.Compliant != 1 || pay.Counts.NonCompliant != 0 {
		t.Errorf("acme-pay = %+v, want one tracked, one parked (case-folded), one compliant", pay)
	}

	if web.Tracked != 1 || web.Counts.NonCompliant != 1 || web.OpenPrs != 1 || web.CompliantPercent == nil || *web.CompliantPercent != 0 {
		t.Errorf("acme-web = %+v, want one tracked, failing with an open PR, 0%%", web)
	}

	// Scoping is SQL's job: the reader alone must honor it.
	scoped, err := reader.Orgs(t.Context(), store.ScopeOrgs("ACME-PAY"))
	if err != nil {
		t.Fatal(err)
	}

	for _, r := range scoped.Repositories {
		if !strings.EqualFold(r.Org, "acme-pay") {
			t.Errorf("acme-pay scope read %+v", r)
		}
	}

	if len(scoped.OpenPRs) != 0 {
		t.Errorf("acme-pay scope open PRs = %v, want none", scoped.OpenPRs)
	}

	none, err := reader.Orgs(t.Context(), store.ScopeOrgs())
	if err != nil {
		t.Fatal(err)
	}

	if len(none.Repositories) != 0 || len(none.Compliance) != 0 {
		t.Errorf("empty-scope orgs = %+v, want nothing", none)
	}
}

func TestReadOnlyPool_RefusesWrites(t *testing.T) {
	t.Parallel()

	f := newAPIFixture(t)

	pool, err := postgres.NewReadOnlyPool(t.Context(), f.dsns.Evaluator)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	_, err = pool.Exec(t.Context(), `INSERT INTO repositories (org, name) VALUES ('x', 'y')`)
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Errorf("INSERT through the read-only pool = %v, want a read-only transaction error", err)
	}

	var timeout string
	if err := pool.QueryRow(t.Context(), `SHOW statement_timeout`).Scan(&timeout); err != nil || timeout != "5s" {
		t.Errorf("statement_timeout = %q (%v), want 5s", timeout, err)
	}
}

//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres"
)

func (f *v2Fixture) repoEvents(t *testing.T, kind string) int {
	t.Helper()

	var n int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM repository_events WHERE repository_id = $1 AND kind = $2`, f.repoID, kind).Scan(&n); err != nil {
		t.Fatalf("count repository events: %v", err)
	}

	return n
}

func (f *v2Fixture) repository(t *testing.T) *store.Repository {
	t.Helper()

	r, err := f.store.GetRepository(context.Background(), f.repoID)
	if err != nil {
		t.Fatalf("GetRepository: %v", err)
	}

	return r
}

func (f *v2Fixture) discover(t *testing.T, org, name string, installation, id int64) store.UpsertResult {
	t.Helper()

	res, err := f.store.UpsertDiscovered(context.Background(), &store.DiscoveredRepo{
		Org: org, Name: name, InstallationID: installation, ProviderRepoID: &id,
	})
	if err != nil {
		t.Fatalf("UpsertDiscovered %s/%s: %v", org, name, err)
	}

	return res
}

func TestRecordCheck_NeverSetsActive(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newV2Fixture(t)

	if err := f.store.Park(ctx, f.repoID, store.ParkArchived, true); err != nil {
		t.Fatalf("Park: %v", err)
	}

	id := int64(42)
	if _, err := f.store.RecordCheck(ctx, &store.CheckRecord{
		Key: "k1", RepositoryID: f.repoID, InstallationID: testInstallation, Trigger: store.TriggerSchedule,
		PolicyVersion: "v2:test", StartedAt: t0, FinishedAt: t0, Outcomes: []store.Outcome{},
		ProviderRepoID: &id, Org: "acme", Name: "widgets",
	}); err != nil {
		t.Fatalf("RecordCheck: %v", err)
	}

	if f.repository(t).Active {
		t.Error("RecordCheck un-parked the repository")
	}
}

func TestIdentity_NullIDFilledOnFirstMatch(t *testing.T) {
	t.Parallel()

	f := newV2Fixture(t)

	res := f.discover(t, "acme", "widgets", testInstallation, 42)
	if res.ID != f.repoID || res.Renamed {
		t.Fatalf("result = %+v, want the existing row, no rename", res)
	}

	if r := f.repository(t); r.ProviderRepoID == nil || *r.ProviderRepoID != 42 {
		t.Errorf("provider_repo_id = %v, want 42", r.ProviderRepoID)
	}
}

func TestIdentity_RenameKeepsIDAndHistory(t *testing.T) {
	t.Parallel()

	f := newV2Fixture(t)
	f.discover(t, "acme", "widgets", testInstallation, 42)
	f.record(t, "k1", t0, missing("codeowners", "CODEOWNERS"))

	res := f.discover(t, "acme", "gadgets", testInstallation, 42)
	if res.ID != f.repoID || !res.Renamed {
		t.Fatalf("result = %+v, want row %d renamed", res, f.repoID)
	}

	if r := f.repository(t); r.Name != "gadgets" {
		t.Errorf("name = %q, want gadgets", r.Name)
	}

	if n := f.repoEvents(t, "renamed"); n != 1 {
		t.Errorf("renamed events = %d, want 1", n)
	}

	if n := f.eventCount(t); n != 1 {
		t.Errorf("finding events = %d, want the pre-rename history kept", n)
	}
}

func TestIdentity_TransferUpdatesOrgAndInstallation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newV2Fixture(t)
	f.discover(t, "acme", "widgets", testInstallation, 42)

	const other int64 = 8
	if err := f.store.UpsertInstallation(ctx, store.Installation{InstallationID: other, AccountLogin: "globex"}); err != nil {
		t.Fatalf("UpsertInstallation: %v", err)
	}

	f.discover(t, "globex", "widgets", other, 42)

	r := f.repository(t)
	if r.Org != "globex" || r.InstallationID != other {
		t.Errorf("repository = %s installation %d, want globex installation %d", r.Org, r.InstallationID, other)
	}

	if n := f.repoEvents(t, "transferred"); n != 1 {
		t.Errorf("transferred events = %d, want 1", n)
	}
}

func TestIdentity_CaseOnlyRenameWritesNoEvent(t *testing.T) {
	t.Parallel()

	f := newV2Fixture(t)
	f.discover(t, "acme", "widgets", testInstallation, 42)

	if res := f.discover(t, "Acme", "Widgets", testInstallation, 42); res.Renamed {
		t.Error("case-only change reported as a rename")
	}

	if r := f.repository(t); r.Name != "Widgets" {
		t.Errorf("name = %q, want the new casing stored", r.Name)
	}

	if n := f.repoEvents(t, "renamed") + f.repoEvents(t, "transferred"); n != 0 {
		t.Errorf("identity events = %d, want 0", n)
	}
}

func TestIdentity_RecordCheckRefreshesRename(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newV2Fixture(t)
	f.discover(t, "acme", "widgets", testInstallation, 42)

	id := int64(42)
	if _, err := f.store.RecordCheck(ctx, &store.CheckRecord{
		Key: "k1", RepositoryID: f.repoID, InstallationID: testInstallation, Trigger: store.TriggerSchedule,
		PolicyVersion: "v2:test", StartedAt: t0, FinishedAt: t0, Outcomes: []store.Outcome{},
		ProviderRepoID: &id, Org: "acme", Name: "gadgets",
	}); err != nil {
		t.Fatalf("RecordCheck: %v", err)
	}

	if r := f.repository(t); r.Name != "gadgets" {
		t.Errorf("name = %q, want gadgets", r.Name)
	}

	if n := f.repoEvents(t, "renamed"); n != 1 {
		t.Errorf("renamed events = %d, want 1", n)
	}
}

func TestIdentity_CaseVariantNamesCollide(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newV2Fixture(t)

	_, err := f.pool.Exec(ctx,
		`INSERT INTO repositories (provider, host, org, name, installation_id) VALUES ('github', 'github.com', 'ACME', 'WIDGETS', $1)`,
		testInstallation)
	if err == nil {
		t.Fatal("case-variant insert succeeded, want a unique violation")
	}
}

func TestIdentity_NameHeldByAnotherIDConflicts(t *testing.T) {
	t.Parallel()

	f := newV2Fixture(t)
	f.discover(t, "acme", "widgets", testInstallation, 42)

	id := int64(99)

	_, err := f.store.UpsertDiscovered(context.Background(), &store.DiscoveredRepo{
		Org: "acme", Name: "widgets", InstallationID: testInstallation, ProviderRepoID: &id,
	})
	if !errors.Is(err, postgres.ErrIdentityConflict) {
		t.Errorf("err = %v, want ErrIdentityConflict", err)
	}
}

func TestIdentity_WithHostKeysRecords(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newV2Fixture(t)
	s := postgres.NewV2Store(f.pool, slog.New(slog.NewTextHandler(io.Discard, nil)), postgres.WithHost("ghe.example.com"))

	if err := s.UpsertInstallation(ctx, store.Installation{InstallationID: 9, AccountLogin: "acme"}); err != nil {
		t.Fatalf("UpsertInstallation: %v", err)
	}

	res, err := s.UpsertDiscovered(ctx, &store.DiscoveredRepo{Org: "acme", Name: "widgets", InstallationID: 9})
	if err != nil {
		t.Fatalf("UpsertDiscovered: %v", err)
	}

	if res.ID == f.repoID {
		t.Fatal("github.com row matched under another host")
	}

	r, err := s.GetRepository(ctx, res.ID)
	if err != nil {
		t.Fatalf("GetRepository: %v", err)
	}

	if r.Host != "ghe.example.com" {
		t.Errorf("host = %q, want ghe.example.com", r.Host)
	}
}

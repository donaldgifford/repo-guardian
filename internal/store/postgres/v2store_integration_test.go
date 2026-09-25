//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/donaldgifford/repo-guardian/internal/findings"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres/pgtest"
)

const testInstallation int64 = 7

// v2Fixture is a migrated database with one installation and one
// repository.
type v2Fixture struct {
	dsn    string
	pool   *pgxpool.Pool
	store  *postgres.V2Store
	repoID int64
}

func newV2Fixture(t *testing.T) *v2Fixture {
	t.Helper()

	ctx := context.Background()
	dsn := pgtest.Start(t)

	_, up := openMigrator(t, dsn)
	if _, err := up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}

	t.Cleanup(pool.Close)

	s := postgres.NewV2Store(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if err := s.UpsertInstallation(ctx, store.Installation{InstallationID: testInstallation, AccountLogin: "acme"}); err != nil {
		t.Fatalf("upsert installation: %v", err)
	}

	res, err := s.UpsertDiscovered(ctx, &store.DiscoveredRepo{Org: "acme", Name: "widgets", InstallationID: testInstallation})
	if err != nil {
		t.Fatalf("upsert repository: %v", err)
	}

	return &v2Fixture{dsn: dsn, pool: pool, store: s, repoID: res.ID}
}

func (f *v2Fixture) record(t *testing.T, key string, at time.Time, outcomes ...store.Outcome) *store.CheckApplied {
	t.Helper()

	if outcomes == nil {
		outcomes = []store.Outcome{}
	}

	applied, err := f.store.RecordCheck(context.Background(), &store.CheckRecord{
		Key:            key,
		RepositoryID:   f.repoID,
		InstallationID: testInstallation,
		Trigger:        store.TriggerSchedule,
		PolicyVersion:  "v2:test",
		StartedAt:      at.Add(-time.Second),
		FinishedAt:     at,
		Outcomes:       outcomes,
	})
	if err != nil {
		t.Fatalf("RecordCheck %s: %v", key, err)
	}

	return applied
}

func (f *v2Fixture) eventCount(t *testing.T) int {
	t.Helper()

	var n int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM finding_events WHERE repository_id = $1`, f.repoID).Scan(&n); err != nil {
		t.Fatalf("count events: %v", err)
	}

	return n
}

type findingRow struct {
	status        string
	statusSince   time.Time
	lastEvaluated time.Time
	evidence      json.RawMessage
}

// finding reads one finding; ok is false when the row does not exist.
func (f *v2Fixture) finding(t *testing.T, name string) (findingRow, bool) {
	t.Helper()

	var r findingRow

	err := f.pool.QueryRow(context.Background(),
		`SELECT status, status_since, last_evaluated_at, evidence FROM findings
		 WHERE repository_id = $1 AND rule_kind = 'file' AND rule_name = $2`, f.repoID, name).
		Scan(&r.status, &r.statusSince, &r.lastEvaluated, &r.evidence)
	if err != nil {
		return findingRow{}, false
	}

	return r, true
}

func missing(name string, paths ...string) store.Outcome {
	return store.Outcome{
		RuleKind:    findings.RuleKindFile,
		RuleName:    name,
		Status:      findings.StatusNonCompliant,
		Reason:      findings.ReasonFileMissing,
		Remediation: findings.RemediationNone,
		Evidence:    findings.FileMissingEvidence{PathsChecked: paths},
	}
}

func compliant(name string) store.Outcome {
	return store.Outcome{RuleKind: findings.RuleKindFile, RuleName: name, Status: findings.StatusCompliant}
}

var t0 = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

func TestRecordCheck_SameKeyTwiceIsIdempotent(t *testing.T) {
	t.Parallel()

	f := newV2Fixture(t)

	first := f.record(t, "k1", t0, missing("codeowners", "CODEOWNERS"), compliant("renovate"))
	if len(first.Transitions) != 2 || first.AlreadyFinal {
		t.Fatalf("first = %+v, want 2 created transitions", first)
	}

	second := f.record(t, "k1", t0.Add(time.Hour), missing("codeowners", "CODEOWNERS"))
	if !second.AlreadyFinal {
		t.Fatal("second call: AlreadyFinal = false")
	}

	if !reflect.DeepEqual(first.Transitions, second.Transitions) {
		t.Errorf("transitions differ:\nfirst  %+v\nsecond %+v", first.Transitions, second.Transitions)
	}

	if n := f.eventCount(t); n != 2 {
		t.Errorf("events = %d, want 2", n)
	}
}

func TestRecordCheck_IdenticalOutcomesWriteNoEvents(t *testing.T) {
	t.Parallel()

	f := newV2Fixture(t)
	f.record(t, "k1", t0, missing("codeowners", "CODEOWNERS"))
	before, _ := f.finding(t, "codeowners")

	applied := f.record(t, "k2", t0.Add(time.Hour), missing("codeowners", "CODEOWNERS"))
	if len(applied.Transitions) != 0 {
		t.Errorf("transitions = %+v, want none", applied.Transitions)
	}

	if n := f.eventCount(t); n != 1 {
		t.Errorf("events = %d, want 1", n)
	}

	after, _ := f.finding(t, "codeowners")
	if !after.lastEvaluated.After(before.lastEvaluated) {
		t.Errorf("last_evaluated_at did not advance: %v -> %v", before.lastEvaluated, after.lastEvaluated)
	}
}

func TestRecordCheck_RemovedRuleWritesEventAndDeletesRow(t *testing.T) {
	t.Parallel()

	f := newV2Fixture(t)
	f.record(t, "k1", t0, missing("codeowners", "CODEOWNERS"), compliant("renovate"))

	applied := f.record(t, "k2", t0.Add(time.Hour), compliant("renovate"))
	if len(applied.Transitions) != 1 || applied.Transitions[0].ToStatus != nil {
		t.Fatalf("transitions = %+v, want one removal", applied.Transitions)
	}

	if _, ok := f.finding(t, "codeowners"); ok {
		t.Error("removed finding still present")
	}
}

func TestRecordCheck_StatusChangeMovesStatusSince(t *testing.T) {
	t.Parallel()

	f := newV2Fixture(t)
	f.record(t, "k1", t0, missing("codeowners", "CODEOWNERS"))

	t1 := t0.Add(time.Hour)
	f.record(t, "k2", t1, compliant("codeowners"))

	row, _ := f.finding(t, "codeowners")
	if !row.statusSince.Equal(t1) {
		t.Errorf("status_since = %v, want %v", row.statusSince, t1)
	}
}

func TestRecordCheck_EvidenceOnlyChangeWritesNoEvent(t *testing.T) {
	t.Parallel()

	f := newV2Fixture(t)
	f.record(t, "k1", t0, missing("codeowners", "CODEOWNERS"))

	applied := f.record(t, "k2", t0.Add(time.Hour), missing("codeowners", "CODEOWNERS", ".github/CODEOWNERS"))
	if len(applied.Transitions) != 0 {
		t.Errorf("transitions = %+v, want none", applied.Transitions)
	}

	row, _ := f.finding(t, "codeowners")
	if !row.statusSince.Equal(t0) {
		t.Errorf("status_since moved to %v", row.statusSince)
	}

	var ev findings.FileMissingEvidence
	if err := json.Unmarshal(row.evidence, &ev); err != nil {
		t.Fatalf("evidence: %v", err)
	}

	if len(ev.PathsChecked) != 2 {
		t.Errorf("evidence not updated: %s", row.evidence)
	}
}

func TestRecordCheck_StagedPayloadIsClearedOnCommit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newV2Fixture(t)

	c := &store.CheckRecord{
		Key:           "staged",
		RepositoryID:  f.repoID,
		Trigger:       store.TriggerSchedule,
		PolicyVersion: "v2:test",
		StartedAt:     t0,
		FinishedAt:    t0,
		Outcomes:      []store.Outcome{missing("codeowners", "CODEOWNERS")},
	}
	if err := f.store.StageCheck(ctx, c); err != nil {
		t.Fatalf("StageCheck: %v", err)
	}

	c.Outcomes = nil

	applied, err := f.store.RecordCheck(ctx, c)
	if err != nil {
		t.Fatalf("RecordCheck: %v", err)
	}

	if len(applied.Transitions) != 1 {
		t.Errorf("transitions = %+v, want the staged creation", applied.Transitions)
	}

	var payloadNull bool
	if err := f.pool.QueryRow(ctx, `SELECT pending_result IS NULL FROM checks WHERE check_key = 'staged'`).Scan(&payloadNull); err != nil {
		t.Fatalf("read check: %v", err)
	}

	if !payloadNull {
		t.Error("pending_result not cleared on commit")
	}
}

func TestRecordCheck_OlderRateObservationNeverWins(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newV2Fixture(t)

	withRate := func(key string, remaining int, observed time.Time) {
		t.Helper()

		if _, err := f.store.RecordCheck(ctx, &store.CheckRecord{
			Key: key, RepositoryID: f.repoID, InstallationID: testInstallation,
			Trigger: store.TriggerSchedule, PolicyVersion: "v2:test", StartedAt: t0, FinishedAt: t0,
			Outcomes: []store.Outcome{},
			Rate:     &store.RateSnapshot{Limit: 5000, Remaining: remaining, ResetAt: observed.Add(time.Hour), ObservedAt: observed},
		}); err != nil {
			t.Fatalf("RecordCheck %s: %v", key, err)
		}
	}

	withRate("new", 100, t0.Add(time.Minute))
	withRate("old", 4000, t0)

	var remaining int
	if err := f.pool.QueryRow(ctx, `SELECT rate_remaining FROM installations WHERE installation_id = $1`,
		testInstallation).Scan(&remaining); err != nil {
		t.Fatalf("read rate: %v", err)
	}

	if remaining != 100 {
		t.Errorf("rate_remaining = %d, want the newer 100", remaining)
	}
}

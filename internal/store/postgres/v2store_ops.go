package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/donaldgifford/repo-guardian/internal/findings"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres/sqlcdb"
)

// Defaults for the provider identity columns until a second provider
// exists (INV-0007).
const (
	defaultProvider = "github"
	defaultHost     = "github.com"
)

// Compile-time checks that V2Store satisfies both halves of the v2
// store seam.
var (
	_ store.Writer = (*V2Store)(nil)
	_ store.Reader = (*V2Store)(nil)
)

// RecordCheckError implements store.Writer. It finalizes the check as an
// error and updates the repository, and never touches findings (the nil
// contract). A retry of an already-final key is a no-op.
func (s *V2Store) RecordCheckError(ctx context.Context, c *store.CheckErrorRecord) (err error) {
	defer func(start time.Time) { observeQuery("v2_record_check_error", start, err) }(time.Now())

	finished := c.FinishedAt
	if finished.IsZero() {
		finished = time.Now().UTC()
	}

	msg := findings.Clip(c.Err, findings.ClipRunes)

	err = s.inTx(ctx, func(q *sqlcdb.Queries) error {
		_, err := q.FinalizeCheck(ctx, sqlcdb.FinalizeCheckParams{
			RepositoryID:  c.RepositoryID,
			CheckKey:      c.Key,
			Trigger:       string(c.Trigger),
			Outcome:       outcomeError,
			Error:         &msg,
			PolicyVersion: c.PolicyVersion,
			StartedAt:     c.StartedAt,
			FinishedAt:    &finished,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}

		if err != nil {
			return fmt.Errorf("finalize check: %w", err)
		}

		return q.UpdateRepositoryAfterError(ctx, sqlcdb.UpdateRepositoryAfterErrorParams{
			ID: c.RepositoryID, CheckedAt: &finished, LastError: &msg,
		})
	})
	if err != nil {
		return fmt.Errorf("postgres.RecordCheckError %q: %w", c.Key, err)
	}

	return nil
}

// Park implements store.Writer. It writes a parked event and, when
// clearFindings is set, deletes the findings with removal events.
// Parking an already-parked repository is a no-op; a missing one
// returns store.ErrNotFound.
func (s *V2Store) Park(ctx context.Context, repoID int64, reason store.ParkReason, clearFindings bool) (err error) {
	defer func(start time.Time) { observeQuery("v2_park", start, err) }(time.Now())

	err = s.inTx(ctx, func(q *sqlcdb.Queries) error {
		n, err := q.ParkRepository(ctx, sqlcdb.ParkRepositoryParams{ID: repoID, ParkReason: ptr(string(reason))})
		if err != nil {
			return fmt.Errorf("park: %w", err)
		}

		if n == 0 {
			if _, err := q.GetRepository(ctx, repoID); err != nil {
				return notFound(err)
			}

			return nil
		}

		if err := insertRepoEvent(ctx, q, repoID, "parked", map[string]string{"reason": string(reason)}); err != nil {
			return err
		}

		if !clearFindings {
			return nil
		}

		return clearRepositoryFindings(ctx, q, repoID)
	})
	if err != nil {
		return fmt.Errorf("postgres.Park %d: %w", repoID, err)
	}

	return nil
}

func clearRepositoryFindings(ctx context.Context, q *sqlcdb.Queries, repoID int64) error {
	current, err := q.LockFindings(ctx, repoID)
	if err != nil {
		return fmt.Errorf("lock findings: %w", err)
	}

	events := make([]sqlcdb.InsertFindingEventParams, 0, len(current))

	for i := range current {
		old := &current[i]

		if err := q.DeleteFinding(ctx, sqlcdb.DeleteFindingParams{
			RepositoryID: repoID, RuleKind: old.RuleKind, RuleName: old.RuleName,
		}); err != nil {
			return fmt.Errorf("delete finding %s/%s: %w", old.RuleKind, old.RuleName, err)
		}

		e := removedEvent(old)
		e.PolicyVersion = old.PolicyVersion
		events = append(events, e)
	}

	_, err = writeEvents(ctx, q, repoID, nil, "", time.Now().UTC(), events)

	return err
}

// UpsertDiscovered implements store.Writer. It matches by name only;
// IMPL-0025 Phase 5 adds matching by provider_repo_id and renames.
// Reactivation here is the only way a parked repository comes back
// (INV-0015's subset invariant).
func (s *V2Store) UpsertDiscovered(ctx context.Context, r *store.DiscoveredRepo) (res store.UpsertResult, err error) {
	defer func(start time.Time) { observeQuery("v2_upsert_discovered", start, err) }(time.Now())

	provider, host := orDefault(r.Provider, defaultProvider), orDefault(r.Host, defaultHost)

	err = s.inTx(ctx, func(q *sqlcdb.Queries) error {
		existing, err := q.LockRepositoryByName(ctx, sqlcdb.LockRepositoryByNameParams{
			Provider: provider, Host: host, Org: r.Org, Name: r.Name,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			res, err = insertDiscovered(ctx, q, r, provider, host)

			return err
		}

		if err != nil {
			return fmt.Errorf("lock repository: %w", err)
		}

		res.ID = existing.ID

		if existing.Active {
			return q.RefreshRepositoryIdentity(ctx, sqlcdb.RefreshRepositoryIdentityParams{
				ID: existing.ID, InstallationID: r.InstallationID, ProviderRepoID: r.ProviderRepoID,
			})
		}

		if err := q.ReactivateRepository(ctx, sqlcdb.ReactivateRepositoryParams{
			ID: existing.ID, InstallationID: r.InstallationID, ProviderRepoID: r.ProviderRepoID,
		}); err != nil {
			return fmt.Errorf("reactivate: %w", err)
		}

		res.Reactivated = true

		return insertRepoEvent(ctx, q, existing.ID, "unparked", map[string]string{"previous_reason": derefOr(existing.ParkReason)})
	})
	if err != nil {
		return store.UpsertResult{}, fmt.Errorf("postgres.UpsertDiscovered %s/%s: %w", r.Org, r.Name, err)
	}

	return res, nil
}

func insertDiscovered(ctx context.Context, q *sqlcdb.Queries, r *store.DiscoveredRepo, provider, host string) (store.UpsertResult, error) {
	var nextDue *time.Time
	if !r.NextDueAt.IsZero() {
		nextDue = &r.NextDueAt
	}

	id, err := q.InsertRepository(ctx, sqlcdb.InsertRepositoryParams{
		Provider:       provider,
		Host:           host,
		Org:            r.Org,
		Name:           r.Name,
		ProviderRepoID: r.ProviderRepoID,
		InstallationID: r.InstallationID,
		NextDueAt:      nextDue,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// A concurrent upsert inserted it between our lock and insert.
		return store.UpsertResult{}, ErrConflict
	}

	if err != nil {
		return store.UpsertResult{}, fmt.Errorf("insert repository: %w", err)
	}

	if err := insertRepoEvent(ctx, q, id, "discovered", map[string]string{}); err != nil {
		return store.UpsertResult{}, err
	}

	return store.UpsertResult{ID: id, Created: true}, nil
}

// UpsertInstallation implements store.Writer. It clears removed_at, so
// a reinstalled App is live again.
func (s *V2Store) UpsertInstallation(ctx context.Context, in store.Installation) (err error) {
	defer func(start time.Time) { observeQuery("v2_upsert_installation", start, err) }(time.Now())

	err = sqlcdb.New(s.pool).UpsertInstallation(ctx, sqlcdb.UpsertInstallationParams{
		InstallationID: in.InstallationID,
		Provider:       orDefault(in.Provider, defaultProvider),
		Host:           orDefault(in.Host, defaultHost),
		AccountLogin:   in.AccountLogin,
		SuspendedAt:    in.SuspendedAt,
	})
	if err != nil {
		return fmt.Errorf("postgres.UpsertInstallation %d: %w", in.InstallationID, err)
	}

	return nil
}

// MarkInstallationRemoved implements store.Writer. It parks every active
// repository of the installation with reason installation_removed and
// keeps their findings: we can no longer read them, which teaches us
// nothing about their rules.
func (s *V2Store) MarkInstallationRemoved(ctx context.Context, installationID int64, at time.Time) (err error) {
	defer func(start time.Time) { observeQuery("v2_mark_installation_removed", start, err) }(time.Now())

	err = s.inTx(ctx, func(q *sqlcdb.Queries) error {
		if _, err := q.MarkInstallationRemoved(ctx, sqlcdb.MarkInstallationRemovedParams{
			InstallationID: installationID, RemovedAt: &at,
		}); err != nil {
			return fmt.Errorf("mark removed: %w", err)
		}

		ids, err := q.ParkInstallationRepositories(ctx, installationID)
		if err != nil {
			return fmt.Errorf("park repositories: %w", err)
		}

		detail := map[string]string{"reason": string(store.ParkInstallationRemoved)}
		for _, id := range ids {
			if err := insertRepoEvent(ctx, q, id, "parked", detail); err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("postgres.MarkInstallationRemoved %d: %w", installationID, err)
	}

	return nil
}

// RecordPolicyVersion implements store.Writer. firstSeen is true only
// for the call that inserted the version.
func (s *V2Store) RecordPolicyVersion(ctx context.Context, version string, summary store.PolicySummary) (firstSeen bool, err error) {
	defer func(start time.Time) { observeQuery("v2_record_policy_version", start, err) }(time.Now())

	b, err := json.Marshal(summary)
	if err != nil {
		return false, fmt.Errorf("postgres.RecordPolicyVersion: marshal summary: %w", err)
	}

	n, err := sqlcdb.New(s.pool).InsertPolicyVersion(ctx, sqlcdb.InsertPolicyVersionParams{Version: version, Summary: b})
	if err != nil {
		return false, fmt.Errorf("postgres.RecordPolicyVersion %q: %w", version, err)
	}

	return n == 1, nil
}

// CompletePolicyRollout implements store.Writer. The first completion
// wins; later calls leave the timestamp alone.
func (s *V2Store) CompletePolicyRollout(ctx context.Context, version string) (err error) {
	defer func(start time.Time) { observeQuery("v2_complete_policy_rollout", start, err) }(time.Now())

	if err = sqlcdb.New(s.pool).CompletePolicyRollout(ctx, version); err != nil {
		return fmt.Errorf("postgres.CompletePolicyRollout %q: %w", version, err)
	}

	return nil
}

// RecordServiceRun implements store.Writer.
func (s *V2Store) RecordServiceRun(ctx context.Context, run *store.ServiceRun) (err error) {
	defer func(start time.Time) { observeQuery("v2_record_service_run", start, err) }(time.Now())

	detail := run.Detail
	if detail == nil {
		detail = map[string]any{}
	}

	b, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("postgres.RecordServiceRun: marshal detail: %w", err)
	}

	outcome := outcomeError
	if run.Success {
		outcome = outcomeSuccess
	}

	if err = sqlcdb.New(s.pool).InsertServiceRun(ctx, sqlcdb.InsertServiceRunParams{
		Kind:       string(run.Kind),
		Outcome:    outcome,
		StartedAt:  run.StartedAt,
		FinishedAt: run.FinishedAt,
		Detail:     b,
	}); err != nil {
		return fmt.Errorf("postgres.RecordServiceRun %s: %w", run.Kind, err)
	}

	return nil
}

// InsertComplianceSnapshot implements store.Writer. It is idempotent on
// (org, kind, rule, at): a retry writes zero rows.
func (s *V2Store) InsertComplianceSnapshot(ctx context.Context, at time.Time) (rows int, err error) {
	defer func(start time.Time) { observeQuery("v2_insert_compliance_snapshot", start, err) }(time.Now())

	n, err := sqlcdb.New(s.pool).InsertComplianceSnapshot(ctx, at)
	if err != nil {
		return 0, fmt.Errorf("postgres.InsertComplianceSnapshot: %w", err)
	}

	return int(n), nil
}

// PruneChecks implements store.Writer. Pending checks are never pruned.
func (s *V2Store) PruneChecks(ctx context.Context, before time.Time) (n int64, err error) {
	defer func(start time.Time) { observeQuery("v2_prune_checks", start, err) }(time.Now())

	n, err = sqlcdb.New(s.pool).PruneChecks(ctx, &before)
	if err != nil {
		return 0, fmt.Errorf("postgres.PruneChecks: %w", err)
	}

	return n, nil
}

// GetRepository implements store.Reader.
func (s *V2Store) GetRepository(ctx context.Context, id int64) (repo *store.Repository, err error) {
	defer func(start time.Time) { observeQuery("v2_get_repository", start, err) }(time.Now())

	row, err := sqlcdb.New(s.pool).GetRepository(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("postgres.GetRepository %d: %w", id, notFound(err))
	}

	r := toRepository(&row)

	return &r, nil
}

// ListActiveRepositories implements store.Reader with keyset paging on
// id.
func (s *V2Store) ListActiveRepositories(ctx context.Context, afterID int64, limit int) (repos []store.Repository, err error) {
	defer func(start time.Time) { observeQuery("v2_list_active_repositories", start, err) }(time.Now())

	rows, err := sqlcdb.New(s.pool).ListActiveRepositories(ctx, sqlcdb.ListActiveRepositoriesParams{
		AfterID: afterID, PageLimit: int32(limit), //nolint:gosec // page sizes are small
	})
	if err != nil {
		return nil, fmt.Errorf("postgres.ListActiveRepositories: %w", err)
	}

	repos = make([]store.Repository, 0, len(rows))
	for i := range rows {
		repos = append(repos, toRepository(&rows[i]))
	}

	return repos, nil
}

func toRepository(r *sqlcdb.Repository) store.Repository {
	return store.Repository{
		ID:               r.ID,
		Provider:         r.Provider,
		Host:             r.Host,
		Org:              r.Org,
		Name:             r.Name,
		ProviderRepoID:   r.ProviderRepoID,
		InstallationID:   r.InstallationID,
		Active:           r.Active,
		ParkReason:       typedPtr[store.ParkReason](r.ParkReason),
		ParkedAt:         r.ParkedAt,
		DiscoveredAt:     r.DiscoveredAt,
		NextDueAt:        r.NextDueAt,
		LastCheckedAt:    r.LastCheckedAt,
		LastCheckOutcome: r.LastCheckOutcome,
		LastError:        derefOr(r.LastError),
		PolicyVersion:    r.PolicyVersion,
		CatalogParseOK:   r.CatalogParseOk,
	}
}

func insertRepoEvent(ctx context.Context, q *sqlcdb.Queries, repoID int64, kind string, detail map[string]string) error {
	b, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("marshal %s event: %w", kind, err)
	}

	if err := q.InsertRepositoryEvent(ctx, sqlcdb.InsertRepositoryEventParams{
		RepositoryID: repoID, Kind: kind, Detail: b,
	}); err != nil {
		return fmt.Errorf("insert %s event: %w", kind, err)
	}

	return nil
}

// notFound maps pgx.ErrNoRows to store.ErrNotFound.
func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return store.ErrNotFound
	}

	return err
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}

	return s
}

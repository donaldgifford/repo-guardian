package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres/sqlcdb"
)

// APIReader is the api role's read side (DESIGN-0027). Every method runs
// in its own read-only transaction and is scoped in SQL by a
// store.APIScope; nothing is filtered in Go after a query.
type APIReader struct {
	pool *pgxpool.Pool
}

// NewAPIReader returns an APIReader on pool, normally a NewReadOnlyPool.
func NewAPIReader(pool *pgxpool.Pool) *APIReader {
	return &APIReader{pool: pool}
}

// Ping checks the pool reaches the database.
func (r *APIReader) Ping(ctx context.Context) error {
	return r.pool.Ping(ctx)
}

// readOnly runs fn in a read-only, repeatable-read transaction, so a
// view built from several queries reads one snapshot.
func (r *APIReader) readOnly(ctx context.Context, fn func(q *sqlcdb.Queries) error) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly, IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return fmt.Errorf("begin read-only transaction: %w", err)
	}

	// Rollback after Commit is a no-op; its error carries nothing new.
	defer tx.Rollback(ctx) //nolint:errcheck // see above

	if err := fn(sqlcdb.New(tx)); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// Summary returns the fleet summary over scope.
func (r *APIReader) Summary(ctx context.Context, scope store.APIScope) (sum *store.Summary, err error) {
	defer func(start time.Time) { observeQuery("api_summary", start, err) }(time.Now())

	all, orgs := scope.All(), scope.Orgs()
	sum = &store.Summary{Parked: []store.ParkedCount{}, OpenPRs: []time.Time{}}

	err = r.readOnly(ctx, func(q *sqlcdb.Queries) error {
		repos, err := q.APISummaryRepositories(ctx, sqlcdb.APISummaryRepositoriesParams{ScopeAll: all, ScopeOrgs: orgs})
		if err != nil {
			return fmt.Errorf("repositories: %w", err)
		}

		for _, row := range repos {
			if row.Active {
				sum.Tracked += int(row.Repositories)

				continue
			}

			sum.Parked = append(sum.Parked, store.ParkedCount{Reason: store.ParkReason(row.ParkReason), Count: int(row.Repositories)})
		}

		f, err := q.APISummaryFindings(ctx, sqlcdb.APISummaryFindingsParams{ScopeAll: all, ScopeOrgs: orgs})
		if err != nil {
			return fmt.Errorf("findings: %w", err)
		}

		sum.Findings = store.StatusCounts{
			Compliant: int(f.Compliant), NonCompliant: int(f.NonCompliant), NotApplicable: int(f.NotApplicable), Unknown: int(f.Unknown),
		}

		if sum.CompliantPercent, err = numericPtr(f.CompliantPercent); err != nil {
			return err
		}

		prs, err := q.APISummaryOpenPRs(ctx, sqlcdb.APISummaryOpenPRsParams{ScopeAll: all, ScopeOrgs: orgs})
		if err != nil {
			return fmt.Errorf("open PRs: %w", err)
		}

		for _, pr := range prs {
			sum.OpenPRs = append(sum.OpenPRs, pr.CreatedAt)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("postgres.APIReader.Summary: %w", err)
	}

	return sum, nil
}

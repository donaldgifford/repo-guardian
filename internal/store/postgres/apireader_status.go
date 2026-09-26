package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres/sqlcdb"
)

// StatusInputs reads the status page's inputs as of now. It is global
// and unscoped, and is called by the status refresher only.
func (r *APIReader) StatusInputs(ctx context.Context, now time.Time) (in *store.StatusInputs, err error) {
	defer func(start time.Time) { observeQuery("api_status", start, err) }(time.Now())

	in = &store.StatusInputs{LastServiceSuccess: map[store.ServiceRunKind]time.Time{}}

	err = r.readOnly(ctx, func(q *sqlcdb.Queries) error {
		c, err := q.StatusChecks(ctx, now.Add(-time.Hour))
		if err != nil {
			return fmt.Errorf("checks: %w", err)
		}

		in.LastCheckSuccess, in.LastWebhookCheck = anyTime(c.LastSuccess), anyTime(c.LastWebhook)
		in.ChecksLastHour, in.ErrorsLastHour, in.ActiveRepositories = int(c.Finished), int(c.Errors), int(c.ActiveRepositories)

		runs, err := q.StatusServiceRuns(ctx)
		if err != nil {
			return fmt.Errorf("service runs: %w", err)
		}

		for _, run := range runs {
			in.LastServiceSuccess[store.ServiceRunKind(run.Kind)] = run.LastSuccess
		}

		rates, err := q.StatusRates(ctx)
		if err != nil {
			return fmt.Errorf("rates: %w", err)
		}

		for _, rate := range rates {
			in.Rates = append(in.Rates, store.RateSnapshot{Limit: int(rate.RateLimit), Remaining: int(rate.RateRemaining), ResetAt: rate.RateResetAt})
		}

		rows, err := compliance(ctx, q, store.Scope{})
		if err != nil {
			return err
		}

		for i := range rows {
			in.Compliance = in.Compliance.Add(rows[i].Counts())
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("postgres.APIReader.StatusInputs: %w", err)
	}

	return in, nil
}

// anyTime converts an untyped max(timestamptz), which pgx decodes to a
// time.Time or nil.
func anyTime(v any) *time.Time {
	t, ok := v.(time.Time)
	if !ok {
		return nil
	}

	return &t
}

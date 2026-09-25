package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/donaldgifford/repo-guardian/internal/store/postgres/sqlcdb"
)

// ErrIdentityConflict is returned when a repository's name is held by a
// row carrying a different provider id: the name was reused by a new
// repository before discovery saw the old one leave. It needs an
// operator, not a guess.
var ErrIdentityConflict = errors.New("repository name held by a different provider id")

// Repository event kinds (the repository_events.kind CHECK constraint).
const (
	eventRenamed     = "renamed"
	eventTransferred = "transferred"
)

// matchRepository locks the repository a write refers to (DESIGN-0025
// § Identity): by provider id first; if unmatched, by name among rows
// without a conflicting id. found is false when neither matches.
func matchRepository(
	ctx context.Context,
	q *sqlcdb.Queries,
	provider, host, org, name string,
	providerRepoID *int64,
) (row sqlcdb.Repository, found bool, err error) {
	if providerRepoID != nil {
		row, err = q.LockRepositoryByProviderID(ctx, sqlcdb.LockRepositoryByProviderIDParams{
			Provider: provider, Host: host, ProviderRepoID: providerRepoID,
		})
		if err == nil {
			return row, true, nil
		}

		if !errors.Is(err, pgx.ErrNoRows) {
			return row, false, fmt.Errorf("lock repository by id: %w", err)
		}
	}

	row, err = q.LockRepositoryByName(ctx, sqlcdb.LockRepositoryByNameParams{
		Provider: provider, Host: host, Org: org, Name: name,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return row, false, nil
	}

	if err != nil {
		return row, false, fmt.Errorf("lock repository by name: %w", err)
	}

	if providerRepoID != nil && row.ProviderRepoID != nil && *row.ProviderRepoID != *providerRepoID {
		return row, false, fmt.Errorf("%s/%s (row %d id %d, incoming id %d): %w",
			org, name, row.ID, *row.ProviderRepoID, *providerRepoID, ErrIdentityConflict)
	}

	return row, true, nil
}

// applyIdentity brings a locked row's org, name, installation and
// provider id up to date. A change of org is a transfer and a change of
// name a rename, each with an event; a case-only change updates the row
// silently, because GitHub names are case-insensitive. renamed reports
// whether an event was written. It never touches active.
func applyIdentity(
	ctx context.Context,
	q *sqlcdb.Queries,
	row *sqlcdb.Repository,
	org, name string,
	installationID int64,
	providerRepoID *int64,
) (renamed bool, err error) {
	if org == "" {
		org = row.Org
	}

	if name == "" {
		name = row.Name
	}

	if installationID == 0 {
		installationID = row.InstallationID
	}

	unchanged := org == row.Org && name == row.Name && installationID == row.InstallationID &&
		(providerRepoID == nil || row.ProviderRepoID != nil)
	if unchanged {
		return false, nil
	}

	if err := q.UpdateRepositoryIdentity(ctx, sqlcdb.UpdateRepositoryIdentityParams{
		ID: row.ID, Org: org, Name: name, InstallationID: installationID, ProviderRepoID: providerRepoID,
	}); err != nil {
		return false, fmt.Errorf("update identity: %w", err)
	}

	from, to := row.Org+"/"+row.Name, org+"/"+name

	switch {
	case !strings.EqualFold(org, row.Org):
		err = insertRepoEvent(ctx, q, row.ID, eventTransferred, map[string]string{
			"from": from, "to": to, "from_org": row.Org, "to_org": org,
		})
	case !strings.EqualFold(name, row.Name):
		err = insertRepoEvent(ctx, q, row.ID, eventRenamed, map[string]string{"from": from, "to": to})
	default:
		return false, nil
	}

	return err == nil, err
}

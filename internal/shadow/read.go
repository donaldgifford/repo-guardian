package shadow

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/donaldgifford/repo-guardian/internal/findings"
)

// v1's tables are not in sqlc's schema (it reads the v2 migrations), so
// these few read-only queries are written out here.
const (
	v1ReposSQL = `SELECT owner || '/' || repo FROM repo_state WHERE active`

	v1RulesSQL = `
SELECT r.owner, r.repo, r.rule_kind, r.rule_name, r.actionable
FROM rule_state r
JOIN repo_state s USING (installation_id, owner, repo)
WHERE s.active`

	v2ReposSQL = `SELECT org || '/' || name FROM repositories WHERE active`

	v2FindingsSQL = `
SELECT r.org, r.name, f.rule_kind, f.rule_name, f.status, coalesce(f.reason, ''), f.remediation
FROM findings f
JOIN repositories r ON r.id = f.repository_id
WHERE r.active`
)

// Verify reads v1 from v1db and v2 from v2db and compares them. The two
// may be the same database: a shadow restored from a v1 dump keeps v1's
// tables beside v2's.
func Verify(ctx context.Context, v1db, v2db *sql.DB) (*Report, error) {
	v1Repos, err := readStrings(ctx, v1db, v1ReposSQL)
	if err != nil {
		return nil, fmt.Errorf("reading v1 repositories: %w", err)
	}

	v2Repos, err := readStrings(ctx, v2db, v2ReposSQL)
	if err != nil {
		return nil, fmt.Errorf("reading v2 repositories: %w", err)
	}

	v1, err := readV1(ctx, v1db)
	if err != nil {
		return nil, fmt.Errorf("reading v1 rule_state: %w", err)
	}

	v2, err := readV2(ctx, v2db)
	if err != nil {
		return nil, fmt.Errorf("reading v2 findings: %w", err)
	}

	return Compare(v1Repos, v2Repos, v1, v2), nil
}

func readStrings(ctx context.Context, db *sql.DB, q string) ([]string, error) {
	return query(ctx, db, q, func(rows *sql.Rows) (s string, err error) {
		err = rows.Scan(&s)

		return s, err
	})
}

func readV1(ctx context.Context, db *sql.DB) ([]V1Row, error) {
	return query(ctx, db, v1RulesSQL, func(rows *sql.Rows) (r V1Row, err error) {
		err = rows.Scan(&r.Org, &r.Repo, &r.RuleKind, &r.RuleName, &r.Actionable)

		return r, err
	})
}

func readV2(ctx context.Context, db *sql.DB) ([]V2Row, error) {
	return query(ctx, db, v2FindingsSQL, func(rows *sql.Rows) (r V2Row, err error) {
		var status, reason, remediation string

		err = rows.Scan(&r.Org, &r.Repo, &r.RuleKind, &r.RuleName, &status, &reason, &remediation)
		r.Status, r.Reason, r.Remediation = findings.Status(status), findings.Reason(reason), findings.Remediation(remediation)

		return r, err
	})
}

// query runs q and scans every row with scan.
func query[T any](ctx context.Context, db *sql.DB, q string, scan func(*sql.Rows) (T, error)) (_ []T, retErr error) {
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}

	defer func() { retErr = errors.Join(retErr, rows.Close()) }()

	var out []T

	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}

		out = append(out, v)
	}

	return out, rows.Err()
}

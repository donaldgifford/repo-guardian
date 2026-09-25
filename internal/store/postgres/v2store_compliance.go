package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/donaldgifford/repo-guardian/internal/findings"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres/sqlcdb"
)

// Compliance implements store.Reader.
func (s *V2Store) Compliance(ctx context.Context, scope store.Scope) (rows []store.ComplianceCount, err error) {
	defer func(start time.Time) { observeQuery("v2_compliance", start, err) }(time.Now())

	rows, err = compliance(ctx, sqlcdb.New(s.pool), scope)
	if err != nil {
		return nil, fmt.Errorf("postgres.Compliance: %w", err)
	}

	return rows, nil
}

// ComplianceReport implements store.Reader.
func (s *V2Store) ComplianceReport(ctx context.Context, scope store.Scope) (rep *store.ComplianceReport, err error) {
	defer func(start time.Time) { observeQuery("v2_compliance_report", start, err) }(time.Now())

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly, IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return nil, fmt.Errorf("postgres.ComplianceReport: begin: %w", err)
	}

	defer func() {
		_ = tx.Rollback(ctx) //nolint:errcheck // read-only tx; nothing to lose on cleanup
	}()

	q := sqlcdb.New(tx)
	rep = &store.ComplianceReport{}

	if rep.Current, err = compliance(ctx, q, scope); err != nil {
		return nil, fmt.Errorf("postgres.ComplianceReport: %w", err)
	}

	orgs := scopeOrgs(scope)

	failing, err := q.FailingFindings(ctx, orgs)
	if err != nil {
		return nil, fmt.Errorf("postgres.ComplianceReport: findings: %w", err)
	}

	for i := range failing {
		f := &failing[i]
		rep.Findings = append(rep.Findings, store.FailingFinding{
			InstallationID: f.InstallationID,
			Org:            f.Org,
			Repo:           f.Name,
			Kind:           findings.RuleKind(f.RuleKind),
			RuleName:       f.RuleName,
			Reason:         findings.Reason(derefOr(f.Reason)),
			Remediation:    findings.Remediation(f.Remediation),
			Since:          f.StatusSince,
			PRURL:          f.PrUrl,
		})
	}

	snaps, err := q.LatestComplianceSnapshots(ctx, orgs)
	if err != nil {
		return nil, fmt.Errorf("postgres.ComplianceReport: snapshots: %w", err)
	}

	for _, sn := range snaps {
		rep.Previous = append(rep.Previous, store.ComplianceSnapshot{
			ComplianceCount: store.ComplianceCount{
				Org: sn.Org, Kind: findings.RuleKind(sn.RuleKind), RuleName: sn.RuleName,
				Compliant: int(sn.Compliant), NonCompliant: int(sn.NonCompliant),
				NotApplicable: int(sn.NotApplicable), Unknown: int(sn.Unknown),
			},
			SnapshotAt: sn.SnapshotAt,
		})
	}

	return rep, nil
}

// InsertComplianceSnapshot implements store.Writer. It stores exactly
// the counts the shared compliance query returns, read and written in
// one transaction, so a snapshot can never disagree with the report.
func (s *V2Store) InsertComplianceSnapshot(ctx context.Context, at time.Time) (n int, err error) {
	defer func(start time.Time) { observeQuery("v2_insert_compliance_snapshot", start, err) }(time.Now())

	err = s.inTx(ctx, func(q *sqlcdb.Queries) error {
		rows, err := compliance(ctx, q, store.Scope{})
		if err != nil {
			return err
		}

		p := sqlcdb.InsertComplianceSnapshotRowsParams{SnapshotAt: at}
		for i := range rows {
			r := &rows[i]
			p.Orgs = append(p.Orgs, r.Org)
			p.RuleKinds = append(p.RuleKinds, string(r.Kind))
			p.RuleNames = append(p.RuleNames, r.RuleName)
			p.Compliant = append(p.Compliant, int32(r.Compliant))             //nolint:gosec // counts fit int32 by construction
			p.NonCompliant = append(p.NonCompliant, int32(r.NonCompliant))    //nolint:gosec // counts fit int32 by construction
			p.NotApplicable = append(p.NotApplicable, int32(r.NotApplicable)) //nolint:gosec // counts fit int32 by construction
			p.Unknown = append(p.Unknown, int32(r.Unknown))                   //nolint:gosec // counts fit int32 by construction
		}

		written, err := q.InsertComplianceSnapshotRows(ctx, p)
		n = int(written)

		return err
	})
	if err != nil {
		return 0, fmt.Errorf("postgres.InsertComplianceSnapshot: %w", err)
	}

	return n, nil
}

func compliance(ctx context.Context, q *sqlcdb.Queries, scope store.Scope) ([]store.ComplianceCount, error) {
	rows, err := q.ComplianceByRule(ctx, scopeOrgs(scope))
	if err != nil {
		return nil, fmt.Errorf("compliance: %w", err)
	}

	out := make([]store.ComplianceCount, 0, len(rows))

	for _, r := range rows {
		c := store.ComplianceCount{
			Org: r.Org, Kind: findings.RuleKind(r.RuleKind), RuleName: r.RuleName,
			Compliant: int(r.Compliant), NonCompliant: int(r.NonCompliant),
			NotApplicable: int(r.NotApplicable), Unknown: int(r.Unknown),
		}

		if c.Percent, err = numericPtr(r.CompliantPercent); err != nil {
			return nil, err
		}

		out = append(out, c)
	}

	return out, nil
}

// scopeOrgs lower-cases the scope for the queries' lower(org) match; an
// empty slice means every org.
func scopeOrgs(scope store.Scope) []string {
	orgs := make([]string, len(scope.Orgs))
	for i, o := range scope.Orgs {
		orgs[i] = strings.ToLower(o)
	}

	return orgs
}

func numericPtr(n pgtype.Numeric) (*float64, error) {
	if !n.Valid {
		return nil, nil //nolint:nilnil // NULL percent: unmeasured
	}

	f, err := n.Float64Value()
	if err != nil {
		return nil, fmt.Errorf("compliance percent: %w", err)
	}

	return &f.Float64, nil
}

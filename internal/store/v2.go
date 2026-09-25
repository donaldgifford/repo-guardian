package store

import (
	"context"
	"time"
)

// Writer is the v2 write side (DESIGN-0025 § Store interface), used by
// the worker and ingest roles. It sits beside v1's Store until the v1
// runtime is deleted (IMPL-0025 OQ1).
//
// Un-parking is exclusive to UpsertDiscovered, INV-0015's subset
// invariant carried over unchanged.
type Writer interface {
	// UpsertDiscovered records a repository seen by discovery or a
	// webhook: it creates, reactivates or renames the row.
	UpsertDiscovered(ctx context.Context, r *DiscoveredRepo) (UpsertResult, error)
	UpsertInstallation(ctx context.Context, in Installation) error
	MarkInstallationRemoved(ctx context.Context, installationID int64, at time.Time) error

	// StageCheck writes a pending checks row carrying the outcome set,
	// so the control plane can hand it from CheckRepo to RecordCheck
	// through the database (DESIGN-0026 OQ17). Staging an already-final
	// key is a no-op.
	StageCheck(ctx context.Context, c *CheckRecord) error
	// RecordCheck applies a check's outcomes to the findings in one
	// transaction. It is idempotent on c.Key: a retry after commit
	// returns the stored transitions with AlreadyFinal set. With nil
	// c.Outcomes it finalizes the row StageCheck wrote.
	RecordCheck(ctx context.Context, c *CheckRecord) (*CheckApplied, error)
	// RecordCheckError records a check that learned nothing. It never
	// touches findings.
	RecordCheckError(ctx context.Context, c *CheckErrorRecord) error
	// Park deactivates a repository. clearFindings deletes its findings
	// with removal events: true when we know no rule applies (archived,
	// fork), false when we learned nothing (access_denied).
	Park(ctx context.Context, repoID int64, reason ParkReason, clearFindings bool) error

	RecordPolicyVersion(ctx context.Context, version string, summary PolicySummary) (firstSeen bool, err error)
	CompletePolicyRollout(ctx context.Context, version string) error
	RecordServiceRun(ctx context.Context, run *ServiceRun) error
	// InsertComplianceSnapshot writes one row per (org, kind, rule) at
	// at. It is idempotent on that key and returns the rows written.
	InsertComplianceSnapshot(ctx context.Context, at time.Time) (int, error)
	// PruneChecks deletes checks that finished before before.
	PruneChecks(ctx context.Context, before time.Time) (int64, error)
}

// Reader is the v2 read side, used by the control plane for paging and
// by the api role. The api role compiles against Reader alone, so it
// cannot write (INV-0009 Obs 3). The report read joins in Phase 8 and
// the API reads in Phase 15.
type Reader interface {
	// GetRepository returns ErrNotFound when id does not exist.
	GetRepository(ctx context.Context, id int64) (*Repository, error)
	// ListActiveRepositories pages active repositories by ascending id,
	// starting after afterID.
	ListActiveRepositories(ctx context.Context, afterID int64, limit int) ([]Repository, error)
}

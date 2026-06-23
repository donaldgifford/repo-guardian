// Package store defines the persistent per-repo state interface for
// repo-guardian. It captures the last reconcile attempt's outcome and
// the policy hash under which it ran, so a sweep can identify repos
// that need re-checking either because their state is stale or
// because the active policy has changed.
//
// Implementations live in subpackages (`store/postgres`)
// and are selected at runtime via `STORE_BACKEND`. See DESIGN-0012 for
// the architectural rationale.
package store

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound indicates the requested repo state is not in the store.
// Implementations MUST return this exact sentinel (or wrap it via
// fmt.Errorf with %w) so callers can `errors.Is(err, store.ErrNotFound)`.
var ErrNotFound = errors.New("repo state not found")

// Status values for RepoState.LastCheckStatus. Defined as constants so
// the engine and tests share the canonical set without magic strings.
const (
	StatusSuccess = "success"
	StatusError   = "error"
	StatusSkipped = "skipped"
	StatusPending = "pending"
)

// RepoState is the persistent record of repo-guardian's most recent
// interaction with a single repository. Identity is the
// (InstallationID, Owner, Repo) tuple; everything else is mutable
// across reconcile attempts.
//
// LastCheckedAt is a pointer to distinguish "never checked" (nil)
// from "checked at the zero time" (which Postgres would coerce to
// 0001-01-01). The Postgres schema uses NULL for the same purpose.
type RepoState struct {
	InstallationID  int64
	Owner           string
	Repo            string
	LastCheckedAt   *time.Time
	LastCheckStatus string
	LastError       string
	PolicyVersion   string
}

// Store is the persistence boundary for per-repo reconcile state.
// All methods are context-cancellable; implementations must respect
// ctx.Done() for in-flight queries.
//
// StaleRepos drives the sweep loop. A row is "stale" when either its
// LastCheckedAt is older than freshness OR its PolicyVersion differs
// from currentPolicyVersion (handles new-rule rollouts without per-rule
// freshness tracking — see DESIGN-0012 Q2 resolution).
type Store interface {
	GetRepoState(ctx context.Context, installationID int64, owner, repo string) (*RepoState, error)
	UpdateRepoState(ctx context.Context, s *RepoState) error
	StaleRepos(ctx context.Context, freshness time.Duration, currentPolicyVersion string, limit int) ([]RepoState, error)
	Close() error
}

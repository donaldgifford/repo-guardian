// Package store defines the persistent per-repo state interface for
// repo-guardian. It captures the last reconcile attempt's outcome and
// the policy hash under which it ran, so a sweep can identify repos
// that need re-checking either because their state is stale or
// because the active policy has changed.
//
// Since IMPL-0023 it also holds compliance posture: RuleState records
// one rule's verdict for one repo, with actionable_since tracking how
// long a repo has been failing. That is the fact a metric cannot carry
// — Prometheus can say "40 repos lack CODEOWNERS" but never "these
// forty, since these dates" — and it is why posture lives in the
// database and is projected to Prometheus rather than the reverse.
//
// Implementations live in subpackages (`store/postgres`)
// and are selected at runtime via `STORE_BACKEND`. See DESIGN-0012 for
// the architectural rationale and DESIGN-0022 for the posture model.
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

	// CatalogParseOK reports whether this repo's catalog-info.yaml
	// parsed into a Backstage Component on the last check. nil means no
	// catalog rule was evaluated, which is a different operator signal
	// from "evaluated and malformed" (false) — see DESIGN-0022
	// §Per-rule posture state.
	CatalogParseOK *bool
}

// RuleState is the persistent record of one rule's verdict for one
// repository (IMPL-0023 Phase 1 / DESIGN-0022 §Per-rule posture state).
// Identity is the (InstallationID, Owner, Repo, RuleName) tuple.
//
// Actionable means the repo is currently non-compliant with the rule.
// ActionableSince is the "missing since 2026-06-14" a compliance report
// needs: implementations set it on the false→true transition, clear it
// on true→false, and preserve it across true→true. Callers do NOT
// supply it — passing a value is meaningless, because only the store
// can see the prior row needed to decide the transition.
type RuleState struct {
	InstallationID int64
	Owner          string
	Repo           string
	RuleName       string
	RuleKind       string
	Actionable     bool
	PolicyVersion  string
}

// Store is the persistence boundary for per-repo reconcile state.
// All methods are context-cancellable; implementations must respect
// ctx.Done() for in-flight queries.
//
// StaleRepos drives the sweep loop. A row is "stale" when either its
// LastCheckedAt is older than freshness OR its PolicyVersion differs
// from currentPolicyVersion (handles new-rule rollouts without per-rule
// freshness tracking — see DESIGN-0012 Q2 resolution).
//
// UpsertIfMissing is the discovery write-path (DESIGN-0017): it inserts
// a row with LastCheckStatus=StatusPending and LastCheckedAt=nil iff no
// row exists for the (installationID, owner, repo) tuple. It returns
// (true, nil) when a new row was inserted and (false, nil) when a row
// already existed — callers use the boolean to drive
// "discovered_repos_total" instrumentation and to decide whether to
// emit a slog.Info on first-sight. Implementations MUST execute this
// as a single atomic statement (no read-then-write race), and MUST NOT
// overwrite any existing fields on conflict.
//
// UpsertRuleStates is the posture write-path (DESIGN-0022). It replaces
// the complete set of rule verdicts for ONE repository, identified by
// the (InstallationID, Owner, Repo) shared by every element of states:
// rows in states are upserted, and any pre-existing row for that repo
// whose rule name is absent from states is deleted. That
// delete-not-in is what makes a renamed or removed rule stop counting
// against compliance on the very next check instead of lingering
// forever (DESIGN-0022 OQ3 → a).
//
// An empty (or nil) states slice is therefore meaningful, not a no-op:
// it clears every rule row for the repo. Callers that merely failed to
// learn anything must not call this at all.
//
// Implementations MUST apply the whole batch in a single transaction,
// so a concurrent posture query never observes a repo mid-rewrite with
// some rules deleted and others not yet written.
type Store interface {
	GetRepoState(ctx context.Context, installationID int64, owner, repo string) (*RepoState, error)
	UpdateRepoState(ctx context.Context, s *RepoState) error
	UpsertIfMissing(ctx context.Context, s *RepoState) (created bool, err error)
	UpsertRuleStates(ctx context.Context, installationID int64, owner, repo string, states []RuleState) error
	StaleRepos(ctx context.Context, freshness time.Duration, currentPolicyVersion string, limit int) ([]RepoState, error)
	Close() error
}

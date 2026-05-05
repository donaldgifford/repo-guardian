// Package memory provides an in-process map-backed implementation of
// store.Store. Suitable for unit tests and no-dep single-replica
// deployments. Restart loses all state — operators choosing this
// backend accept that tradeoff per DESIGN-0012.
package memory

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/store"
)

// Store is the in-memory implementation. The zero value is not usable;
// construct via New().
type Store struct {
	mu    sync.RWMutex
	repos map[key]store.RepoState
}

type key struct {
	installationID int64
	owner          string
	repo           string
}

// New returns an empty Store.
func New() *Store {
	return &Store{repos: make(map[key]store.RepoState)}
}

// GetRepoState returns the stored state for (installationID, owner, repo)
// or store.ErrNotFound if no record exists.
func (s *Store) GetRepoState(_ context.Context, installationID int64, owner, repo string) (*store.RepoState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rs, ok := s.repos[key{installationID, owner, repo}]
	if !ok {
		return nil, store.ErrNotFound
	}

	// Defensive copy: callers must not mutate stored state.
	out := rs

	if rs.LastCheckedAt != nil {
		t := *rs.LastCheckedAt
		out.LastCheckedAt = &t
	}

	return &out, nil
}

// UpdateRepoState upserts s into the store.
func (s *Store) UpdateRepoState(_ context.Context, rs *store.RepoState) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	stored := *rs
	if rs.LastCheckedAt != nil {
		t := *rs.LastCheckedAt
		stored.LastCheckedAt = &t
	}

	s.repos[key{rs.InstallationID, rs.Owner, rs.Repo}] = stored

	return nil
}

// StaleRepos returns up to limit repos whose LastCheckedAt is older
// than freshness OR whose PolicyVersion differs from
// currentPolicyVersion. Sort order: nil-LastCheckedAt first, then
// ascending by LastCheckedAt — matches the Postgres `NULLS FIRST` query.
func (s *Store) StaleRepos(_ context.Context, freshness time.Duration, currentPolicyVersion string, limit int) ([]store.RepoState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cutoff := time.Now().Add(-freshness)

	var stale []store.RepoState

	for k := range s.repos {
		rs := s.repos[k]
		switch {
		case rs.LastCheckedAt == nil:
			stale = append(stale, copyState(&rs))
		case rs.LastCheckedAt.Before(cutoff):
			stale = append(stale, copyState(&rs))
		case rs.PolicyVersion != currentPolicyVersion:
			stale = append(stale, copyState(&rs))
		}
	}

	sort.Slice(stale, func(i, j int) bool {
		return lessByCheckedAt(&stale[i], &stale[j])
	})

	if limit > 0 && len(stale) > limit {
		stale = stale[:limit]
	}

	return stale, nil
}

// Close is a no-op for the memory store.
func (*Store) Close() error { return nil }

func copyState(rs *store.RepoState) store.RepoState {
	out := *rs
	if rs.LastCheckedAt != nil {
		t := *rs.LastCheckedAt
		out.LastCheckedAt = &t
	}

	return out
}

// lessByCheckedAt orders nil LastCheckedAt first, then ascending by
// time. Mirrors the Postgres `ORDER BY last_checked_at NULLS FIRST`.
func lessByCheckedAt(a, b *store.RepoState) bool {
	switch {
	case a.LastCheckedAt == nil && b.LastCheckedAt == nil:
		return false
	case a.LastCheckedAt == nil:
		return true
	case b.LastCheckedAt == nil:
		return false
	default:
		return a.LastCheckedAt.Before(*b.LastCheckedAt)
	}
}

package memory_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/store/memory"
)

func TestGet_NotFound(t *testing.T) {
	t.Parallel()

	s := memory.New()

	_, err := s.GetRepoState(context.Background(), 42, "org", "repo")
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestUpdate_Get_RoundTrip(t *testing.T) {
	t.Parallel()

	s := memory.New()
	now := time.Now().UTC()
	want := &store.RepoState{
		InstallationID:  42,
		Owner:           "org",
		Repo:            "repo",
		LastCheckedAt:   &now,
		LastCheckStatus: store.StatusSuccess,
		PolicyVersion:   "v1",
	}

	if err := s.UpdateRepoState(context.Background(), want); err != nil {
		t.Fatalf("UpdateRepoState: %v", err)
	}

	got, err := s.GetRepoState(context.Background(), 42, "org", "repo")
	if err != nil {
		t.Fatalf("GetRepoState: %v", err)
	}

	if got.LastCheckStatus != store.StatusSuccess || got.PolicyVersion != "v1" {
		t.Errorf("round-trip mismatch: got %+v", got)
	}

	if got.LastCheckedAt == nil || !got.LastCheckedAt.Equal(now) {
		t.Errorf("LastCheckedAt: got %v, want %v", got.LastCheckedAt, now)
	}
}

func TestGet_DefensiveCopy(t *testing.T) {
	t.Parallel()

	s := memory.New()
	t1 := time.Now()

	if err := s.UpdateRepoState(context.Background(), &store.RepoState{
		InstallationID: 1,
		Owner:          "o",
		Repo:           "r",
		LastCheckedAt:  &t1,
		PolicyVersion:  "v1",
	}); err != nil {
		t.Fatalf("UpdateRepoState: %v", err)
	}

	got, err := s.GetRepoState(context.Background(), 1, "o", "r")
	if err != nil {
		t.Fatalf("GetRepoState: %v", err)
	}

	// Mutating the returned state must not affect stored state.
	got.PolicyVersion = "MUTATED"
	*got.LastCheckedAt = time.Time{}

	again, err := s.GetRepoState(context.Background(), 1, "o", "r")
	if err != nil {
		t.Fatalf("GetRepoState: %v", err)
	}

	if again.PolicyVersion != "v1" {
		t.Errorf("PolicyVersion mutated through returned pointer: %q", again.PolicyVersion)
	}

	if again.LastCheckedAt == nil || !again.LastCheckedAt.Equal(t1) {
		t.Errorf("LastCheckedAt mutated through returned pointer: %v", again.LastCheckedAt)
	}
}

func TestStaleRepos_Freshness(t *testing.T) {
	t.Parallel()

	s := memory.New()
	now := time.Now().UTC()
	old := now.Add(-24 * time.Hour)
	recent := now.Add(-1 * time.Minute)

	mustUpdate(t, s, &store.RepoState{InstallationID: 1, Owner: "o", Repo: "old", LastCheckedAt: &old, PolicyVersion: "v1"})
	mustUpdate(t, s, &store.RepoState{InstallationID: 1, Owner: "o", Repo: "recent", LastCheckedAt: &recent, PolicyVersion: "v1"})

	stale, err := s.StaleRepos(context.Background(), time.Hour, "v1", 10)
	if err != nil {
		t.Fatalf("StaleRepos: %v", err)
	}

	if len(stale) != 1 || stale[0].Repo != "old" {
		t.Errorf("expected only 'old' to be stale, got %+v", stale)
	}
}

func TestStaleRepos_PolicyVersionMismatch(t *testing.T) {
	t.Parallel()

	s := memory.New()
	now := time.Now().UTC()

	mustUpdate(t, s, &store.RepoState{InstallationID: 1, Owner: "o", Repo: "match", LastCheckedAt: &now, PolicyVersion: "v2"})
	mustUpdate(t, s, &store.RepoState{InstallationID: 1, Owner: "o", Repo: "mismatch", LastCheckedAt: &now, PolicyVersion: "v1"})

	stale, err := s.StaleRepos(context.Background(), time.Hour, "v2", 10)
	if err != nil {
		t.Fatalf("StaleRepos: %v", err)
	}

	if len(stale) != 1 || stale[0].Repo != "mismatch" {
		t.Errorf("expected only policy-mismatch repo, got %+v", stale)
	}
}

func TestStaleRepos_NilCheckedAtFirst(t *testing.T) {
	t.Parallel()

	s := memory.New()
	now := time.Now().UTC()
	old := now.Add(-24 * time.Hour)

	mustUpdate(t, s, &store.RepoState{InstallationID: 1, Owner: "o", Repo: "checked", LastCheckedAt: &old, PolicyVersion: "v1"})
	mustUpdate(t, s, &store.RepoState{InstallationID: 1, Owner: "o", Repo: "never", PolicyVersion: "v1"})

	stale, err := s.StaleRepos(context.Background(), time.Hour, "v1", 10)
	if err != nil {
		t.Fatalf("StaleRepos: %v", err)
	}

	if len(stale) != 2 {
		t.Fatalf("expected 2 stale, got %d", len(stale))
	}

	if stale[0].Repo != "never" {
		t.Errorf("nil-checked-at must come first: %+v", stale)
	}
}

func TestStaleRepos_LimitApplied(t *testing.T) {
	t.Parallel()

	s := memory.New()

	for i := range 5 {
		mustUpdate(t, s, &store.RepoState{
			InstallationID: 1, Owner: "o",
			Repo:          "r" + string(rune('0'+i)),
			PolicyVersion: "v1",
		})
	}

	stale, err := s.StaleRepos(context.Background(), time.Hour, "v1", 3)
	if err != nil {
		t.Fatalf("StaleRepos: %v", err)
	}

	if len(stale) != 3 {
		t.Errorf("expected limit=3, got %d", len(stale))
	}
}

func TestClose_NoOp(t *testing.T) {
	t.Parallel()

	if err := memory.New().Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func mustUpdate(t *testing.T, s *memory.Store, rs *store.RepoState) {
	t.Helper()

	if err := s.UpdateRepoState(context.Background(), rs); err != nil {
		t.Fatalf("UpdateRepoState: %v", err)
	}
}

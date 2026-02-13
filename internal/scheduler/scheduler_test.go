package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/checker"
	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
)

// mockClient implements ghclient.Client for scheduler tests.
type mockClient struct {
	installations []*ghclient.Installation
	installRepos  map[int64][]*ghclient.Repository

	listInstallErr error
	listReposErr   error
}

func newMockClient() *mockClient {
	return &mockClient{
		installRepos: make(map[int64][]*ghclient.Repository),
	}
}

func (*mockClient) GetContents(_ context.Context, _, _, _ string) (bool, error) {
	return false, fmt.Errorf("not implemented")
}

func (*mockClient) ListOpenPullRequests(_ context.Context, _, _ string) ([]*ghclient.PullRequest, error) {
	return nil, fmt.Errorf("not implemented")
}

func (*mockClient) GetRepository(_ context.Context, _, _ string) (*ghclient.Repository, error) {
	return nil, fmt.Errorf("not implemented")
}

func (*mockClient) GetBranchSHA(_ context.Context, _, _, _ string) (string, error) {
	return "", fmt.Errorf("not implemented")
}

func (*mockClient) CreateBranch(_ context.Context, _, _, _, _ string) error {
	return fmt.Errorf("not implemented")
}

func (*mockClient) DeleteBranch(_ context.Context, _, _, _ string) error {
	return fmt.Errorf("not implemented")
}

func (*mockClient) CreateOrUpdateFile(_ context.Context, _, _, _, _, _, _ string) error {
	return fmt.Errorf("not implemented")
}

func (*mockClient) CreatePullRequest(_ context.Context, _, _, _, _, _, _ string) (*ghclient.PullRequest, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *mockClient) ListInstallations(_ context.Context) ([]*ghclient.Installation, error) {
	if m.listInstallErr != nil {
		return nil, m.listInstallErr
	}

	return m.installations, nil
}

func (m *mockClient) ListInstallationRepos(_ context.Context, installationID int64) ([]*ghclient.Repository, error) {
	if m.listReposErr != nil {
		return nil, m.listReposErr
	}

	return m.installRepos[installationID], nil
}

func (*mockClient) CreateInstallationClient(_ context.Context, _ int64) (ghclient.Client, error) {
	return nil, fmt.Errorf("not implemented")
}

func (*mockClient) GetFileContent(_ context.Context, _, _, _ string) (string, error) {
	return "", fmt.Errorf("not implemented")
}

func (*mockClient) GetCustomPropertyValues(_ context.Context, _, _ string) ([]*ghclient.CustomPropertyValue, error) {
	return nil, fmt.Errorf("not implemented")
}

func (*mockClient) SetCustomPropertyValues(_ context.Context, _, _ string, _ []*ghclient.CustomPropertyValue) error {
	return fmt.Errorf("not implemented")
}

func TestReconcileAll(t *testing.T) {
	t.Parallel()

	client := newMockClient()
	client.installations = []*ghclient.Installation{
		{ID: 1, Account: "org1"},
		{ID: 2, Account: "org2"},
	}
	client.installRepos[1] = []*ghclient.Repository{
		{Owner: "org1", Name: "repo-a"},
		{Owner: "org1", Name: "repo-b"},
		{Owner: "org1", Name: "repo-c"},
	}
	client.installRepos[2] = []*ghclient.Repository{
		{Owner: "org2", Name: "repo-d"},
		{Owner: "org2", Name: "repo-e"},
		{Owner: "org2", Name: "repo-f"},
	}

	q := checker.NewQueue(100, slog.Default())

	s := NewScheduler(client, q, time.Hour, slog.Default(), true, true)
	s.reconcileAll(context.Background())

	if qLen := q.Len(); qLen != 6 {
		t.Errorf("expected 6 jobs enqueued, got %d", qLen)
	}
}

func TestReconcileAll_SkipsArchived(t *testing.T) {
	t.Parallel()

	client := newMockClient()
	client.installations = []*ghclient.Installation{
		{ID: 1, Account: "org1"},
	}
	client.installRepos[1] = []*ghclient.Repository{
		{Owner: "org1", Name: "active-repo"},
		{Owner: "org1", Name: "archived-repo", Archived: true},
		{Owner: "org1", Name: "forked-repo", Fork: true},
	}

	q := checker.NewQueue(100, slog.Default())

	s := NewScheduler(client, q, time.Hour, slog.Default(), true, true)
	s.reconcileAll(context.Background())

	if qLen := q.Len(); qLen != 1 {
		t.Errorf("expected 1 job enqueued (skipping archived+fork), got %d", qLen)
	}
}

func TestStart_RunsOnStartup(t *testing.T) {
	t.Parallel()

	client := newMockClient()
	client.installations = []*ghclient.Installation{
		{ID: 1, Account: "org1"},
	}
	client.installRepos[1] = []*ghclient.Repository{
		{Owner: "org1", Name: "repo-a"},
	}

	q := checker.NewQueue(100, slog.Default())

	s := NewScheduler(client, q, 24*time.Hour, slog.Default(), true, true)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		s.Start(ctx)
		close(done)
	}()

	// Wait for startup reconciliation.
	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return after context cancellation")
	}

	if qLen := q.Len(); qLen < 1 {
		t.Error("expected at least 1 job from startup reconciliation")
	}
}

func TestStart_RespectsContextCancellation(t *testing.T) {
	t.Parallel()

	client := newMockClient()
	q := checker.NewQueue(100, slog.Default())

	s := NewScheduler(client, q, time.Hour, slog.Default(), true, true)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		s.Start(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler did not stop after context cancellation")
	}
}

func TestReconcileAll_ListInstallationsError(t *testing.T) {
	t.Parallel()

	client := newMockClient()
	client.listInstallErr = fmt.Errorf("API error")

	q := checker.NewQueue(100, slog.Default())

	s := NewScheduler(client, q, time.Hour, slog.Default(), true, true)
	s.reconcileAll(context.Background())

	if qLen := q.Len(); qLen != 0 {
		t.Errorf("expected 0 jobs on error, got %d", qLen)
	}
}

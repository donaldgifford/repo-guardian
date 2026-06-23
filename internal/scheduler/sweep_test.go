package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/queue"
)

// recordingQueue is a test-local queue.Queue that records enqueued
// jobs in memory. Not a backend replacement — see DESIGN-0018 — just
// a recorder for sweep-test assertions about how many jobs the
// scheduler enqueued.
type recordingQueue struct {
	mu   sync.Mutex
	jobs []queue.Job
}

func newRecordingQueue() *recordingQueue { return &recordingQueue{} }

func (r *recordingQueue) Enqueue(_ context.Context, j queue.Job) error { //nolint:gocritic // interface contract
	r.mu.Lock()
	r.jobs = append(r.jobs, j)
	r.mu.Unlock()

	return nil
}

func (*recordingQueue) Subscribe(ctx context.Context, _ func(context.Context, queue.Job) error) error {
	<-ctx.Done()

	return ctx.Err()
}

func (*recordingQueue) Close() error { return nil }

func (r *recordingQueue) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return len(r.jobs)
}

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

func (*mockClient) AddLabelsToPR(_ context.Context, _, _ string, _ int, _ []string) error {
	return nil
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

func (*mockClient) GetVulnerabilityAlertsEnabled(_ context.Context, _, _ string) (bool, error) {
	return false, fmt.Errorf("not implemented")
}

func (*mockClient) EnableVulnerabilityAlerts(_ context.Context, _, _ string) error {
	return fmt.Errorf("not implemented")
}

func (*mockClient) DisableVulnerabilityAlerts(_ context.Context, _, _ string) error {
	return fmt.Errorf("not implemented")
}

func (*mockClient) GetRepoSettings(_ context.Context, _, _ string) (*ghclient.RepoSettings, error) {
	return nil, fmt.Errorf("not implemented")
}

func (*mockClient) UpdateRepository(_ context.Context, _, _ string, _ *ghclient.RepoUpdateOpts) error {
	return fmt.Errorf("not implemented")
}

func (*mockClient) ListRepositoryRulesets(_ context.Context, _, _ string) ([]*ghclient.Ruleset, error) {
	return nil, fmt.Errorf("not implemented")
}

func (*mockClient) GetRepositoryRuleset(_ context.Context, _, _ string, _ int64) (*ghclient.Ruleset, error) {
	return nil, fmt.Errorf("not implemented")
}

func (*mockClient) CreateRepositoryRuleset(_ context.Context, _, _ string, _ *ghclient.Ruleset) (*ghclient.Ruleset, error) {
	return nil, fmt.Errorf("not implemented")
}

func (*mockClient) UpdateRepositoryRuleset(_ context.Context, _, _ string, _ int64, _ *ghclient.Ruleset) (*ghclient.Ruleset, error) {
	return nil, fmt.Errorf("not implemented")
}

func (*mockClient) ListLabels(_ context.Context, _, _ string) ([]*ghclient.Label, error) {
	return nil, fmt.Errorf("not implemented")
}

func (*mockClient) CreateLabel(_ context.Context, _, _ string, _ *ghclient.Label) error {
	return fmt.Errorf("not implemented")
}

func (*mockClient) UpdateLabel(_ context.Context, _, _, _ string, _ *ghclient.Label) error {
	return fmt.Errorf("not implemented")
}

func (*mockClient) DeleteLabel(_ context.Context, _, _, _ string) error {
	return fmt.Errorf("not implemented")
}

func (*mockClient) RateLimitRemaining(_ context.Context, _ int64) (int, int, error) {
	return 5000, 5000, nil
}

func (*mockClient) GetContentsOnBranch(_ context.Context, _, _, _, _ string) (string, bool, error) {
	return "", false, nil
}

func (*mockClient) DeleteFile(_ context.Context, _, _, _, _, _, _ string) error {
	return nil
}

func (*mockClient) UpdatePullRequest(_ context.Context, _, _ string, _ int, _, _ string) error {
	return nil
}

func (*mockClient) ClosePullRequest(_ context.Context, _, _ string, _ int) error {
	return nil
}

func (*mockClient) ListPRComments(_ context.Context, _, _ string, _ int) ([]*ghclient.Comment, error) {
	return nil, nil
}

func (*mockClient) UpsertPRComment(_ context.Context, _, _ string, _ int, _, _ string) error {
	return nil
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

	q := newRecordingQueue()

	s := NewSweeper(client, q, time.Hour, slog.Default(), true, true)
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

	q := newRecordingQueue()

	s := NewSweeper(client, q, time.Hour, slog.Default(), true, true)
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

	q := newRecordingQueue()

	s := NewSweeper(client, q, 24*time.Hour, slog.Default(), true, true)

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
	q := newRecordingQueue()

	s := NewSweeper(client, q, time.Hour, slog.Default(), true, true)

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

	q := newRecordingQueue()

	s := NewSweeper(client, q, time.Hour, slog.Default(), true, true)
	s.reconcileAll(context.Background())

	if qLen := q.Len(); qLen != 0 {
		t.Errorf("expected 0 jobs on error, got %d", qLen)
	}
}

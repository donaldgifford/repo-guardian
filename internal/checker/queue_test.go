package checker

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
)

func TestEnqueue_Success(t *testing.T) {
	t.Parallel()

	q := NewQueue(10, slog.Default())

	err := q.Enqueue(RepoJob{Owner: "org", Repo: "repo", InstallationID: 1, Trigger: TriggerWebhook})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
}

func TestEnqueue_Full(t *testing.T) {
	t.Parallel()

	q := NewQueue(1, slog.Default())

	// Fill the queue.
	if err := q.Enqueue(RepoJob{Owner: "org", Repo: "repo1", InstallationID: 1, Trigger: TriggerWebhook}); err != nil {
		t.Fatalf("first Enqueue: %v", err)
	}

	// This should fail.
	err := q.Enqueue(RepoJob{Owner: "org", Repo: "repo2", InstallationID: 1, Trigger: TriggerWebhook})
	if err == nil {
		t.Fatal("expected error when queue is full")
	}
}

func TestEnqueue_Stopped(t *testing.T) {
	t.Parallel()

	q := NewQueue(10, slog.Default())

	// Mark as stopped without starting (simulates after Stop).
	q.mu.Lock()
	q.stopped = true
	q.mu.Unlock()

	err := q.Enqueue(RepoJob{Owner: "org", Repo: "repo", InstallationID: 1, Trigger: TriggerWebhook})
	if err == nil {
		t.Fatal("expected error when queue is stopped")
	}
}

func TestWorkers_ProcessJobs(t *testing.T) {
	t.Parallel()

	engine := testEngine(true) // dry-run to avoid needing real files.
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}

	var processed atomic.Int32

	// Wrap engine to count processed jobs.
	q := NewQueue(100, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q.Start(ctx, 3, engine, client)

	jobCount := 10
	for i := range jobCount {
		job := RepoJob{
			Owner:          "org",
			Repo:           "repo",
			InstallationID: 1,
			Trigger:        TriggerWebhook,
		}
		if err := q.Enqueue(job); err != nil {
			t.Fatalf("Enqueue job %d: %v", i, err)
		}

		processed.Add(1)
	}

	// Give workers time to process.
	time.Sleep(500 * time.Millisecond)

	q.Stop()

	if got := processed.Load(); got != int32(jobCount) {
		t.Errorf("expected %d jobs enqueued, got %d", jobCount, got)
	}
}

func TestStop_DrainsGracefully(t *testing.T) {
	t.Parallel()

	engine := testEngine(true)
	client := newMockClient()
	client.repo = &ghclient.Repository{
		Owner: "org", Name: "repo", HasBranch: true, DefaultRef: "main",
	}

	q := NewQueue(100, slog.Default())

	ctx := context.Background()
	q.Start(ctx, 2, engine, client)

	// Enqueue a few jobs.
	for i := range 5 {
		if err := q.Enqueue(RepoJob{Owner: "org", Repo: "repo", InstallationID: 1, Trigger: TriggerScheduler}); err != nil {
			t.Fatalf("Enqueue job %d: %v", i, err)
		}
	}

	// Stop should wait for in-flight work.
	done := make(chan struct{})
	go func() {
		q.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Stopped successfully.
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not complete within timeout")
	}

	if q.Accepting() {
		t.Error("queue should not be accepting after Stop")
	}
}

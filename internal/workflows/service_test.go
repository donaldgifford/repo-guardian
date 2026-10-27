package workflows

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.temporal.io/sdk/temporal"
)

// serviceFakes records the service activities. repos is what
// ListRepositories returns per installation; listErr fails it; pages is
// how many SignalRepositories pages each pass has.
type serviceFakes struct {
	mu sync.Mutex

	installations []InstallationRef
	repos         map[int64][]WebhookRepo
	listErr       map[int64]bool
	pages         int

	batches   []UpsertRepositoriesInput
	parked    []ParkMissingInput
	signals   []SignalRepositoriesInput
	signalAt  []time.Time
	snapshots []time.Time
	prunes    []time.Time
	completed []string
	cleared   int
	runs      []ServiceRun

	now func() time.Time
}

func (f *serviceFakes) ListInstallations(context.Context) ([]InstallationRef, error) {
	return f.installations, nil
}

func (f *serviceFakes) ListRepositories(_ context.Context, id int64) ([]WebhookRepo, error) {
	if f.listErr[id] {
		return nil, temporal.NewNonRetryableApplicationError("listing failed", "test", nil)
	}

	return f.repos[id], nil
}

func (f *serviceFakes) UpsertRepositories(_ context.Context, in *UpsertRepositoriesInput) (*UpsertRepositoriesResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.batches = append(f.batches, *in)

	res := &UpsertRepositoriesResult{}
	for _, r := range in.Repos {
		res.IDs = append(res.IDs, r.ID)
	}

	res.Started = len(in.Repos) / 2

	return res, nil
}

func (f *serviceFakes) ParkMissing(_ context.Context, in *ParkMissingInput) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.parked = append(f.parked, *in)

	return 0, nil
}

func (f *serviceFakes) SignalRepositories(_ context.Context, in *SignalRepositoriesInput) (*SignalRepositoriesResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.signals = append(f.signals, *in)
	f.signalAt = append(f.signalAt, f.now())

	// Each pass is f.pages pages of 10; the page number is AfterID/10.
	page := int(in.AfterID / 10)

	return &SignalRepositoriesResult{NextAfterID: in.AfterID + 10, Signalled: 10, Done: page+1 >= f.pages}, nil
}

func (f *serviceFakes) InsertComplianceSnapshot(_ context.Context, at time.Time) (int, error) {
	f.snapshots = append(f.snapshots, at)

	return 3, nil
}

func (f *serviceFakes) PruneChecks(_ context.Context, before time.Time) (int64, error) {
	f.prunes = append(f.prunes, before)

	return 7, nil
}

func (f *serviceFakes) CompletePolicyRollout(_ context.Context, v string) error {
	f.completed = append(f.completed, v)

	return nil
}

func (f *serviceFakes) ClearBootstrapPending(context.Context) error {
	f.cleared++

	return nil
}

func (f *serviceFakes) RecordServiceRun(_ context.Context, run *ServiceRun) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.runs = append(f.runs, *run)

	return nil
}

func repos(n int) []WebhookRepo {
	out := make([]WebhookRepo, n)
	for i := range out {
		out[i] = WebhookRepo{ID: int64(i + 1), Org: "acme", Name: "r"}
	}

	return out
}

func TestChunk(t *testing.T) {
	t.Parallel()

	var got []int
	for c := range chunk(make([]int, 7), 3) {
		got = append(got, len(c))
	}

	if len(got) != 3 || got[2] != 1 {
		t.Errorf("chunks = %v", got)
	}

	for range chunk([]int(nil), 3) {
		t.Error("empty input yielded a chunk")
	}
}

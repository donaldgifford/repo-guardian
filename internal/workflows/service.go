package workflows

import (
	"errors"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// Service workflow sizing (IMPL-0025 Phase 13).
const (
	// discoveryBatch is how many repositories one UpsertRepositories
	// activity upserts.
	discoveryBatch = 100
	// signalPage is how many repositories one SignalRepositories
	// activity signals.
	signalPage = 500

	serviceTimeout     = 5 * time.Minute
	listingMaxAttempts = 3
)

// serviceOptions runs a service activity. It retries without limit:
// every service activity is idempotent, and giving up would leave a
// rollout or a snapshot half done.
func serviceOptions(ctx workflow.Context, p Priority) workflow.Context {
	return workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: serviceTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    time.Minute,
		},
		Priority: TaskPriority(p, 0),
	})
}

// listingOptions runs a GitHub listing. It gives up after a few
// attempts: a failed listing is recorded as incomplete and parks
// nothing, and the next scheduled run tries again.
func listingOptions(ctx workflow.Context, installationID int64) workflow.Context {
	return workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: serviceTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    5 * time.Second,
			BackoffCoefficient: 2,
			MaximumAttempts:    listingMaxAttempts,
		},
		Priority: TaskPriority(PrioritySchedule, installationID),
	})
}

// DiscoveryWorkflow lists installations and their repositories
// (DESIGN-0026 § Discovery). Each repository is upserted, the only way
// to un-park one, and a new or reactivated repository gets its
// RepoWorkflow. Active repositories missing from a complete listing are
// parked removed; a failed listing parks nothing.
func DiscoveryWorkflow(ctx workflow.Context, in *DiscoveryInput) error {
	started := workflow.Now(ctx)
	detail := map[string]any{}

	installations := []InstallationRef{{ID: in.InstallationID}}

	if in.InstallationID == 0 {
		detail["scope"] = "all"

		if err := workflow.ExecuteActivity(listingOptions(ctx, 0), ListInstallationsActivity).Get(ctx, &installations); err != nil {
			detail["error"] = err.Error()

			return errors.Join(err, recordRun(ctx, ServiceDiscovery, false, started, detail))
		}
	} else {
		detail["installation_id"] = in.InstallationID
	}

	var discovered, activated int

	var incomplete []int64

	for _, inst := range installations {
		seen, n, err := discoverInstallation(ctx, inst.ID)
		if err != nil {
			workflow.GetLogger(ctx).Warn("discovery: installation listing incomplete", "installation_id", inst.ID, "error", err)

			incomplete = append(incomplete, inst.ID)

			continue
		}

		discovered += seen
		activated += n
	}

	detail["installations"] = len(installations)
	detail["repositories"] = discovered
	detail["started"] = activated

	if len(incomplete) > 0 {
		detail["incomplete"] = incomplete
	}

	return recordRun(ctx, ServiceDiscovery, len(incomplete) == 0, started, detail)
}

// discoverInstallation lists, upserts in batches and parks the missing.
// It returns the repositories seen and the workflows started. A listing
// error returns before anything is parked.
func discoverInstallation(ctx workflow.Context, installationID int64) (seen, started int, err error) {
	var repos []WebhookRepo
	if err := workflow.ExecuteActivity(listingOptions(ctx, installationID), ListRepositoriesActivity, installationID).
		Get(ctx, &repos); err != nil {
		return 0, 0, err
	}

	ids := make([]int64, 0, len(repos))
	actx := serviceOptions(ctx, PrioritySchedule)

	for batch := range chunk(repos, discoveryBatch) {
		var res UpsertRepositoriesResult
		if err := workflow.ExecuteActivity(actx, UpsertRepositoriesActivity,
			&UpsertRepositoriesInput{InstallationID: installationID, Repos: batch}).Get(ctx, &res); err != nil {
			return 0, 0, err
		}

		ids = append(ids, res.IDs...)
		started += res.Started
	}

	if err := workflow.ExecuteActivity(actx, ParkMissingActivity,
		&ParkMissingInput{InstallationID: installationID, Seen: ids}).Get(ctx, nil); err != nil {
		return 0, 0, err
	}

	return len(ids), started, nil
}

// chunk yields s in slices of at most n.
func chunk[T any](s []T, n int) func(yield func([]T) bool) {
	return func(yield func([]T) bool) {
		for len(s) > 0 {
			end := min(n, len(s))
			if !yield(s[:end]) {
				return
			}

			s = s[end:]
		}
	}
}

// SnapshotWorkflow writes the compliance snapshot, prunes old checks and
// records the run (DESIGN-0026 § Snapshots and retention).
func SnapshotWorkflow(ctx workflow.Context, in *SnapshotInput) error {
	started := workflow.Now(ctx)
	actx := serviceOptions(ctx, PrioritySchedule)

	var rows int
	if err := workflow.ExecuteActivity(actx, SnapshotActivity, started).Get(ctx, &rows); err != nil {
		return err
	}

	var pruned int64
	if err := workflow.ExecuteActivity(actx, PruneChecksActivity, started.Add(-in.Retention)).Get(ctx, &pruned); err != nil {
		return err
	}

	return recordRun(ctx, ServiceSnapshot, true, started, map[string]any{"rows": rows, "pruned_checks": pruned})
}

// PolicyRolloutWorkflow spreads a new policy version's re-checks across
// the window, re-signals the stragglers once at its end, and marks the
// rollout complete (DESIGN-0026 § Policy rollout).
func PolicyRolloutWorkflow(ctx workflow.Context, in *PolicyRolloutInput) error {
	started := workflow.Now(ctx)
	by := started.Add(in.Window)

	signalled, err := signalAll(ctx, SignalRepositoriesInput{Version: in.Version, By: by})
	if err != nil {
		return err
	}

	if err := workflow.Sleep(ctx, by.Sub(workflow.Now(ctx))); err != nil {
		return err
	}

	stragglers, err := signalAll(ctx, SignalRepositoriesInput{Version: in.Version, By: workflow.Now(ctx), DriftedOnly: true})
	if err != nil {
		return err
	}

	if err := workflow.ExecuteActivity(serviceOptions(ctx, PriorityRollout), CompletePolicyRolloutActivity, in.Version).
		Get(ctx, nil); err != nil {
		return err
	}

	return recordRun(ctx, ServicePolicyRollout, true, started,
		map[string]any{"version": in.Version, "signalled": signalled, "stragglers": stragglers})
}

// BootstrapWorkflow starts a RepoWorkflow for every active repository
// after a v1 backfill, each due at its backfilled next_due_at. Parked
// repositories are skipped: only discovery un-parks. It is idempotent: a
// running RepoWorkflow just takes the signal.
func BootstrapWorkflow(ctx workflow.Context) error {
	started := workflow.Now(ctx)

	signalled, err := signalAll(ctx, SignalRepositoriesInput{Bootstrap: true})
	if err != nil {
		return err
	}

	if err := workflow.ExecuteActivity(serviceOptions(ctx, PriorityRollout), ClearBootstrapActivity).Get(ctx, nil); err != nil {
		return err
	}

	return recordRun(ctx, ServiceBootstrap, true, started, map[string]any{"signalled": signalled})
}

// signalAll pages every active repository through SignalRepositories.
func signalAll(ctx workflow.Context, in SignalRepositoriesInput) (int, error) {
	actx := serviceOptions(ctx, PriorityRollout)
	in.Limit = signalPage
	total := 0

	for {
		var res SignalRepositoriesResult
		if err := workflow.ExecuteActivity(actx, SignalRepositoriesActivity, &in).Get(ctx, &res); err != nil {
			return total, err
		}

		total += res.Signalled

		if res.Done {
			return total, nil
		}

		in.AfterID = res.NextAfterID
	}
}

func recordRun(ctx workflow.Context, kind string, success bool, started time.Time, detail map[string]any) error {
	run := &ServiceRun{Kind: kind, Success: success, StartedAt: started, FinishedAt: workflow.Now(ctx), Detail: detail}

	return workflow.ExecuteActivity(serviceOptions(ctx, PrioritySchedule), RecordServiceRunActivity, run).Get(ctx, nil)
}

package workflows

import (
	"errors"

	"go.temporal.io/sdk/workflow"
)

// ControlsDiscoveryWorkflow is the evaluator's discovery (DESIGN-0032 §
// Evaluation): it lists the Evaluation App's installations and their
// repositories, refreshes the app = 'eval' installation and access rows,
// and upserts each repository, which resolves it inline and starts its
// EvaluationWorkflow. Active repositories missing from a complete listing
// are parked removed; a failed listing parks nothing.
func ControlsDiscoveryWorkflow(ctx workflow.Context, in *DiscoveryInput) error {
	started := workflow.Now(ctx)
	detail := map[string]any{}

	installations := []InstallationRef{{ID: in.InstallationID}}

	if in.InstallationID == 0 {
		detail["scope"] = "all"

		if err := workflow.ExecuteActivity(listingOptions(ctx, 0), ControlsListInstallationsActivity).Get(ctx, &installations); err != nil {
			detail["error"] = err.Error()

			return errors.Join(err, recordRun(ctx, ServiceDiscovery, false, started, detail))
		}
	} else {
		detail["installation_id"] = in.InstallationID
	}

	var discovered, activated int

	var incomplete []int64

	for _, inst := range installations {
		seen, n, err := discoverEvalInstallation(ctx, inst.ID)
		if err != nil {
			workflow.GetLogger(ctx).Warn("controls discovery: installation listing incomplete", "installation_id", inst.ID, "error", err)

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

// discoverEvalInstallation lists, upserts in batches and parks the
// missing. A listing error returns before anything is parked.
func discoverEvalInstallation(ctx workflow.Context, installationID int64) (seen, started int, err error) {
	var repos []DiscoveredRepo
	if err := workflow.ExecuteActivity(listingOptions(ctx, installationID), ControlsListRepositoriesActivity, installationID).
		Get(ctx, &repos); err != nil {
		return 0, 0, err
	}

	ids := make([]int64, 0, len(repos))
	actx := serviceOptions(ctx, PrioritySchedule)

	for batch := range chunk(repos, discoveryBatch) {
		var res UpsertRepositoriesResult
		if err := workflow.ExecuteActivity(actx, ControlsUpsertActivity,
			&ControlsUpsertInput{InstallationID: installationID, Repos: batch}).Get(ctx, &res); err != nil {
			return 0, 0, err
		}

		ids = append(ids, res.IDs...)
		started += res.Started
	}

	if err := workflow.ExecuteActivity(actx, ControlsParkMissingActivity,
		&ParkMissingInput{InstallationID: installationID, Seen: ids}).Get(ctx, nil); err != nil {
		return 0, 0, err
	}

	return len(ids), started, nil
}

// ControlsRolloutWorkflow rolls out one policy activation (DESIGN-0032 §
// Evaluation): it re-resolves every active repository, signals each
// managed one's EvaluationWorkflow with policy_changed spread across the
// window, waits out the window and marks the rollout complete. Its id
// carries the activation time, so a revert rolls out again.
func ControlsRolloutWorkflow(ctx workflow.Context, in *ControlsRolloutInput) error {
	started := workflow.Now(ctx)
	by := started.Add(in.Window)

	resolved, err := controlsPages(ctx, ControlsResolveAllActivity, ControlsPageInput{Version: in.Version})
	if err != nil {
		return err
	}

	signalled, err := controlsPages(ctx, ControlsSignalAllActivity, ControlsPageInput{Version: in.Version, By: by})
	if err != nil {
		return err
	}

	if err := workflow.Sleep(ctx, by.Sub(workflow.Now(ctx))); err != nil {
		return err
	}

	if err := workflow.ExecuteActivity(serviceOptions(ctx, PriorityRollout), ControlsCompleteRolloutActivity, in.Version).
		Get(ctx, nil); err != nil {
		return err
	}

	return recordRun(ctx, ServicePolicyRollout, true, started,
		map[string]any{"version": in.Version, "resolved": resolved, detailSignalled: signalled})
}

// ControlsSnapshotWorkflow writes the controls compliance snapshot from
// the live compliance query, prunes the checks that finished longer ago
// than in.Retention (CHECKS_RETENTION, INV-0023 F7.2) and records the
// run (IMPL-0029 task 5.8). A repository's last evaluation lives in
// repositories.last_checked_at, so a prune never makes one read as
// never evaluated.
func ControlsSnapshotWorkflow(ctx workflow.Context, in *SnapshotInput) error {
	started := workflow.Now(ctx)
	actx := serviceOptions(ctx, PrioritySchedule)

	var rows int
	if err := workflow.ExecuteActivity(actx, ControlsSnapshotActivity, started).Get(ctx, &rows); err != nil {
		return err
	}

	detail := map[string]any{"rows": rows}

	// Snapshot runs captured before the prune existed replay without it.
	if workflow.GetVersion(ctx, pruneChecksChange, workflow.DefaultVersion, 1) == 1 && in != nil && in.Retention > 0 {
		var pruned int64
		if err := workflow.ExecuteActivity(actx, ControlsPruneChecksActivity, started.Add(-in.Retention)).Get(ctx, &pruned); err != nil {
			return err
		}

		detail["pruned"] = pruned
	}

	return recordRun(ctx, ServiceSnapshot, true, started, detail)
}

// pruneChecksChange is the GetVersion change id of the snapshot's prune.
const pruneChecksChange = "prune-controls-checks"

// controlsPages runs a paged activity to its last page and sums the
// counts.
func controlsPages(ctx workflow.Context, activity string, in ControlsPageInput) (int, error) {
	actx := serviceOptions(ctx, PriorityRollout)
	in.Limit = signalPage
	total := 0

	for {
		var res ControlsPageResult
		if err := workflow.ExecuteActivity(actx, activity, &in).Get(ctx, &res); err != nil {
			return total, err
		}

		total += res.Count

		if res.Done {
			return total, nil
		}

		in.AfterID = res.NextAfterID
	}
}

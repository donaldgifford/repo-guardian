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
// are parked removed; a failed listing parks nothing. It has
// DiscoveryWorkflow's shape on the controls activities.
func ControlsDiscoveryWorkflow(ctx workflow.Context, in *DiscoveryInput) error {
	started := workflow.Now(ctx)
	detail := map[string]any{}

	installations := []InstallationRef{{ID: in.InstallationID}}

	if in.InstallationID == 0 {
		detail["scope"] = "all"

		if err := workflow.ExecuteActivity(listingOptions(ctx, 0), ControlsListInstallationsActivity).Get(ctx, &installations); err != nil {
			detail["error"] = err.Error()

			return errors.Join(err, recordRunWith(ctx, ControlsRecordServiceRunActivity, ServiceDiscovery, false, started, detail))
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

	return recordRunWith(ctx, ControlsRecordServiceRunActivity, ServiceDiscovery, len(incomplete) == 0, started, detail)
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

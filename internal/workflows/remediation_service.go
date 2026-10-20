package workflows

import (
	"errors"

	"go.temporal.io/sdk/workflow"
)

// RemediationSweepWorkflow is the remediator's hourly schedule
// (IMPL-0030 6.1, 6.2): lifecycle maintenance first, so a slot an
// expired reservation held is free for the sweep, then the sweep, which
// starts every due remediation an evaluation did not. Each half writes
// its own service_runs row.
func RemediationSweepWorkflow(ctx workflow.Context, in *RemediationSweepInput) error {
	started := workflow.Now(ctx)
	actx := serviceOptions(ctx, PriorityRollout)

	var maint MaintenanceResult

	mErr := workflow.ExecuteActivity(actx, RemediationMaintainActivity, in.MaintenanceBatch).Get(ctx, &maint)

	detail := map[string]any{
		"expired": maint.Expired, "superseded": maint.Superseded, "withdrawn": maint.Withdrawn,
		"closed": maint.Closed, "blocked": maint.Blocked, "skipped": maint.Skipped, "failed": maint.Failed,
		"throttled": maint.Throttled,
	}
	if mErr != nil {
		detail["error"] = mErr.Error()
	}

	if err := recordRun(ctx, ServiceMaintenance, mErr == nil && maint.Failed == 0, started, detail); err != nil {
		return errors.Join(mErr, err)
	}

	sweepStarted := workflow.Now(ctx)

	var sweep SweepResult

	sErr := workflow.ExecuteActivity(actx, RemediationSweepActivity, in.SweepBatch).Get(ctx, &sweep)

	detail = map[string]any{"due": sweep.Due, "started": sweep.Started}
	if sErr != nil {
		detail["error"] = sErr.Error()
	}

	return errors.Join(mErr, sErr, recordRun(ctx, ServiceSweep, sErr == nil, sweepStarted, detail))
}

// RemediationDiscoveryWorkflow is the controls-discovery-remediate
// schedule (IMPL-0030 6.4): it lists the Remediation App's installations
// and their repositories, refreshes the app = 'remediate' rows, and asks
// the evaluator to re-resolve every repository whose access changed.
// Installations a complete listing did not see are pruned; a failed
// listing prunes nothing.
func RemediationDiscoveryWorkflow(ctx workflow.Context) error {
	started := workflow.Now(ctx)
	detail := map[string]any{}

	var installations []RemediateInstallation
	if err := workflow.ExecuteActivity(listingOptions(ctx, 0), RemediationListInstallationsActivity).Get(ctx, &installations); err != nil {
		detail["error"] = err.Error()

		return errors.Join(err, recordRun(ctx, ServiceRemediationDiscovery, false, started, detail))
	}

	var (
		changed    int
		incomplete []int64
		seen       = make([]int64, 0, len(installations))
	)

	for _, inst := range installations {
		seen = append(seen, inst.ID)

		var repos []WebhookRepo
		if err := workflow.ExecuteActivity(listingOptions(ctx, inst.ID), RemediationRefreshAccessActivity, &inst).Get(ctx, &repos); err != nil {
			workflow.GetLogger(ctx).Warn("remediation discovery: installation listing incomplete", "installation_id", inst.ID, "error", err)

			incomplete = append(incomplete, inst.ID)

			continue
		}

		if err := resolveAccess(ctx, &ResolveAccessInput{InstallationID: inst.ID, AccountLogin: inst.Account, Repos: repos}); err != nil {
			return err
		}

		changed += len(repos)
	}

	if len(incomplete) == 0 {
		var pruned []WebhookRepo
		if err := workflow.ExecuteActivity(serviceOptions(ctx, PrioritySchedule), RemediationPruneInstallationsActivity, seen).
			Get(ctx, &pruned); err != nil {
			return err
		}

		if err := resolveAccess(ctx, &ResolveAccessInput{Repos: pruned}); err != nil {
			return err
		}

		changed += len(pruned)
	}

	detail["installations"] = len(installations)
	detail["changed"] = changed

	if len(incomplete) > 0 {
		detail["incomplete"] = incomplete
	}

	return recordRun(ctx, ServiceRemediationDiscovery, len(incomplete) == 0, started, detail)
}

// resolveAccess hands the changed repositories to the evaluator. The
// changes are in this workflow's history, so a failed hand-off retries
// with them even though the refresh that found them has committed.
func resolveAccess(ctx workflow.Context, in *ResolveAccessInput) error {
	if len(in.Repos) == 0 {
		return nil
	}

	return workflow.ExecuteActivity(serviceOptions(ctx, PrioritySchedule), RemediationResolveAccessActivity, in).Get(ctx, nil)
}

// Package workflows holds repo-guardian's Temporal workflow code
// (DESIGN-0026). Workflow code must be deterministic, so this package
// never imports the engine, the store or the GitHub client; a depguard
// rule enforces that. Everything with side effects runs in the
// activities package, which workflows reach by the names below.
package workflows

import (
	"strconv"
	"time"
)

// Workflow type names. They are part of every running execution's
// identity, so renaming one strands the executions already started
// under the old name.
const (
	InstallationWorkflowName = "InstallationWorkflow"
	WebhookWorkflowName      = "WebhookWorkflow"

	// RemediationWorkflowName is the per-(repository, control)
	// remediation run on the remediation queue (DESIGN-0032). IMPL-0030
	// implements it; the name is fixed now so StartRemediations can
	// target it.
	RemediationWorkflowName = "RemediationWorkflow"

	// The controls evaluation workflows (IMPL-0029 Phase 5), on the
	// evaluation queue.
	EvaluationWorkflowName        = "EvaluationWorkflow"
	ControlsDiscoveryWorkflowName = "ControlsDiscoveryWorkflow"
	ControlsRolloutWorkflowName   = "ControlsRolloutWorkflow"
	ControlsSnapshotWorkflowName  = "ControlsSnapshotWorkflow"
)

// Activity names. The activities package registers under these names
// and workflows execute by them, so neither side imports the other.
const (
	AcquireBudgetActivity = "AcquireBudget"
	RouteWebhookActivity  = "RouteWebhook"

	// StartRemediationsActivity signal-with-starts the due
	// RemediationWorkflows, one activity per evaluation (INV-0022 F4).
	StartRemediationsActivity = "StartRemediations"

	// The controls evaluation activities (IMPL-0029 Phase 5).
	EvaluateActivity                  = "Evaluate"
	RecordEvaluationActivity          = "RecordEvaluation"
	ParkRepositoryActivity            = "ParkRepository"
	ControlsListInstallationsActivity = "ControlsListInstallations"
	ControlsListRepositoriesActivity  = "ControlsListRepositories"
	ControlsUpsertActivity            = "ControlsUpsertRepositories"
	ControlsParkMissingActivity       = "ControlsParkMissing"
	ControlsResolveAllActivity        = "ControlsResolveAll"
	ControlsSignalAllActivity         = "ControlsSignalAll"
	ControlsSnapshotActivity          = "ControlsSnapshot"
	ControlsCompleteRolloutActivity   = "ControlsCompleteRollout"
	ControlsRecordServiceRunActivity  = "ControlsRecordServiceRun"
)

// RemediateSignal asks a RemediationWorkflow to run, or to loop once
// more when it already is (DESIGN-0032 D6).
const RemediateSignal = "remediate"

// RemediationWorkflowID is remediation/<repository id>/<control slug>:
// at most one remediation runs per control per repository.
func RemediationWorkflowID(repositoryID int64, control string) string {
	return "remediation/" + strconv.FormatInt(repositoryID, 10) + "/" + control
}

// Schedule IDs. Temporal suffixes each scheduled run's workflow ID with
// its schedule time.
const (
	// The evaluator's schedules (IMPL-0029 5.6): the only two its
	// bootstrap ensures.
	ControlsDiscoveryScheduleID = "controls-discovery"
	ControlsSnapshotScheduleID  = "controls-snapshot"
)

// Signal names accepted by EvaluationWorkflow.
const (
	RecheckSignal       = "recheck"
	PolicyChangedSignal = "policy_changed"
)

// InstallationWorkflow's handlers: acquire is an Update, so the caller
// gets a synchronous grant or wait; report is a Signal.
const (
	AcquireUpdate = "acquire"
	ReportSignal  = "report"
	SuspendSignal = "suspend"
)

// InstallationWorkflowID is the workflow ID for an installation's rate
// budget under app: installation/<app>/<id> for a controls App, whose
// installations each keep their own budget (IMPL-0028 task 4.6), and
// installation/<id> for the rc's single App (app ""), so running rc
// executions keep their id.
func InstallationWorkflowID(app string, installationID int64) string {
	if app == "" {
		return "installation/" + strconv.FormatInt(installationID, 10)
	}

	return "installation/" + app + "/" + strconv.FormatInt(installationID, 10)
}

// BudgetHolder is the AcquireRequest.Holder for workflowID acting as
// app: <app>/<workflow id> for a controls App, the bare workflow id for
// the rc's.
func BudgetHolder(app, workflowID string) string {
	if app == "" {
		return workflowID
	}

	return app + "/" + workflowID
}

// WebhookWorkflowID is the workflow ID for a GitHub delivery. It makes
// redeliveries idempotent.
func WebhookWorkflowID(deliveryID string) string {
	return "webhook/" + deliveryID
}

// EvaluationWorkflowID is evaluation/<repository id>: one evaluation
// loop per repository, kept across renames and transfers.
func EvaluationWorkflowID(repositoryID int64) string {
	return "evaluation/" + strconv.FormatInt(repositoryID, 10)
}

// ControlsDiscoveryWorkflowID is the workflow ID for single-installation
// controls discovery started by a webhook delivery.
func ControlsDiscoveryWorkflowID(installationID int64, deliveryID string) string {
	return "controls-discovery/installation/" + strconv.FormatInt(installationID, 10) + "/" + deliveryID
}

// ControlsRolloutWorkflowID is controls-rollout/<version>/<activated_at
// unix seconds>: one rollout per activation, so a revert or rollback to
// a version seen before rolls out again (DESIGN-0030 D14).
func ControlsRolloutWorkflowID(version string, activatedAt time.Time) string {
	return "controls-rollout/" + version + "/" + strconv.FormatInt(activatedAt.Unix(), 10)
}

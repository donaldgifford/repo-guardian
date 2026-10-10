// Package workflows holds repo-guardian's Temporal workflow code
// (DESIGN-0026). Workflow code must be deterministic, so this package
// never imports the engine, the store or the GitHub client; a depguard
// rule enforces that. Everything with side effects runs in the
// activities package, which workflows reach by the names below.
package workflows

import "strconv"

// Workflow type names. They are part of every running execution's
// identity, so renaming one strands the executions already started
// under the old name.
const (
	RepoWorkflowName         = "RepoWorkflow"
	InstallationWorkflowName = "InstallationWorkflow"
	WebhookWorkflowName      = "WebhookWorkflow"

	DiscoveryWorkflowName     = "DiscoveryWorkflow"
	SnapshotWorkflowName      = "SnapshotWorkflow"
	PolicyRolloutWorkflowName = "PolicyRolloutWorkflow"
	BootstrapWorkflowName     = "BootstrapWorkflow"

	// RemediationWorkflowName is the per-(repository, control)
	// remediation run on the remediation queue (DESIGN-0032). IMPL-0030
	// implements it; the name is fixed now so StartRemediations can
	// target it.
	RemediationWorkflowName = "RemediationWorkflow"
)

// Activity names. The activities package registers under these names
// and workflows execute by them, so neither side imports the other.
const (
	CheckRepoActivity        = "CheckRepo"
	RecordCheckActivity      = "RecordCheck"
	RecordCheckErrorActivity = "RecordCheckError"
	ParkActivity             = "Park"
	AcquireBudgetActivity    = "AcquireBudget"
	RouteWebhookActivity     = "RouteWebhook"

	ListInstallationsActivity     = "ListInstallations"
	ListRepositoriesActivity      = "ListRepositories"
	UpsertRepositoriesActivity    = "UpsertRepositories"
	ParkMissingActivity           = "ParkMissing"
	SignalRepositoriesActivity    = "SignalRepositories"
	SnapshotActivity              = "InsertComplianceSnapshot"
	PruneChecksActivity           = "PruneChecks"
	CompletePolicyRolloutActivity = "CompletePolicyRollout"
	ClearBootstrapActivity        = "ClearBootstrapPending"
	RecordServiceRunActivity      = "RecordServiceRun"

	// StartRemediationsActivity signal-with-starts the due
	// RemediationWorkflows, one activity per evaluation (INV-0022 F4).
	StartRemediationsActivity = "StartRemediations"
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
	DiscoveryScheduleID = "discovery"
	SnapshotScheduleID  = "snapshot"
)

// BootstrapWorkflowID is the one BootstrapWorkflow a cutover runs.
const BootstrapWorkflowID = "bootstrap/v1"

// Signal names accepted by RepoWorkflow.
const (
	RecheckSignal       = "recheck"
	PolicyChangedSignal = "policy_changed"
	ParkSignal          = "park"
)

// InstallationWorkflow's handlers: acquire is an Update, so the caller
// gets a synchronous grant or wait; report is a Signal.
const (
	AcquireUpdate = "acquire"
	ReportSignal  = "report"
	SuspendSignal = "suspend"
)

// RepoWorkflowID is the workflow ID for repositories.id. The ID is the
// per-repository lock: one RepoWorkflow per repository, kept across
// renames and transfers because the internal id never changes.
func RepoWorkflowID(repositoryID int64) string {
	return "repo/" + strconv.FormatInt(repositoryID, 10)
}

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

// DiscoveryWorkflowID is the workflow ID for single-installation
// discovery started by a webhook delivery.
func DiscoveryWorkflowID(installationID int64, deliveryID string) string {
	return "discovery/installation/" + strconv.FormatInt(installationID, 10) + "/" + deliveryID
}

// PolicyRolloutWorkflowID is the workflow ID for a policy version's
// rollout. Every worker tries to start it; Temporal rejects duplicates.
func PolicyRolloutWorkflowID(version string) string {
	return "policy-rollout/" + version
}

package workflows

import "time"

// Service run kinds, matching store.ServiceRunKind.
const (
	ServiceDiscovery     = "discovery"
	ServiceSnapshot      = "snapshot"
	ServicePolicyRollout = "policy_rollout"
	ServiceBootstrap     = "bootstrap"
)

// DiscoveryInput is DiscoveryWorkflow's input. A zero InstallationID
// discovers every installation; otherwise only that one.
type DiscoveryInput struct {
	InstallationID int64
}

// InstallationRef is one installation ListInstallations returned.
type InstallationRef struct {
	ID      int64
	Account string
}

// UpsertRepositoriesInput is one batch of an installation's listing.
type UpsertRepositoriesInput struct {
	InstallationID int64
	Repos          []WebhookRepo
}

// UpsertRepositoriesResult reports a batch: the ids of every repository
// in it and how many new or reactivated ones had a RepoWorkflow started.
type UpsertRepositoriesResult struct {
	IDs     []int64
	Started int
}

// ParkMissingInput names the active repositories a complete listing
// saw; the installation's other active repositories are parked.
type ParkMissingInput struct {
	InstallationID int64
	Seen           []int64
}

// SignalRepositoriesInput pages the active repositories after AfterID
// and SignalWithStarts each RepoWorkflow with policy_changed.
type SignalRepositoriesInput struct {
	AfterID int64
	Limit   int

	// Version and By are the policy_changed payload. With Bootstrap set,
	// each repository keeps its own policy version and By is its
	// next_due_at, so the v1 spread carries over.
	Version   string
	By        time.Time
	Bootstrap bool

	// DriftedOnly skips repositories whose policy version is Version:
	// the rollout's straggler pass.
	DriftedOnly bool
}

// SignalRepositoriesResult reports one page. Done is set when the page
// was the last.
type SignalRepositoriesResult struct {
	NextAfterID int64
	Signalled   int
	Done        bool
}

// ServiceRun is one service_runs row.
type ServiceRun struct {
	Kind       string
	Success    bool
	StartedAt  time.Time
	FinishedAt time.Time
	Detail     map[string]any
}

// SnapshotInput is SnapshotWorkflow's input.
type SnapshotInput struct {
	// Retention prunes checks that finished longer ago than this.
	Retention time.Duration
}

// PolicyRolloutInput is PolicyRolloutWorkflow's input.
type PolicyRolloutInput struct {
	Version string
	Window  time.Duration
}

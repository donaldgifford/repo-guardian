package temporal

import (
	"runtime/debug"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/contrib/sysinfo"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// The Temporal worker deployments (IMPL-0028 task 4.3, D28): the rc's,
// and one per controls role with no version suffix, so each half
// promotes and rolls back on its own and a remediator scaled to zero
// never blocks the evaluator's promotion.
const (
	DeploymentRC        = "repo-guardian"
	DeploymentEval      = "repo-guardian-eval"
	DeploymentRemediate = "repo-guardian-remediate"
)

// DeploymentName returns the worker deployment of the role named role:
// evaluator, remediator, or anything else for the rc's.
func DeploymentName(role string) string {
	switch role {
	case "evaluator":
		return DeploymentEval
	case "remediator":
		return DeploymentRemediate
	default:
		return DeploymentRC
	}
}

// DefaultActivityConcurrency is the activities one worker runs at once
// when its role's sizing (EVALUATOR_/REMEDIATOR_CONCURRENCY) is unset.
const DefaultActivityConcurrency = 10

// buildIDRevisionLen shortens a VCS revision used as a build ID.
const buildIDRevisionLen = 12

// devBuildID is the build ID of a binary with no version or revision.
const devBuildID = "dev"

// develVersion is the module version of an untagged build.
const develVersion = "(devel)"

// WorkerConfig configures a Temporal worker.
type WorkerConfig struct {
	TaskQueue string

	// Deployment is the worker deployment the worker joins; empty means
	// DeploymentRC.
	Deployment string

	// ActivityConcurrency caps concurrent activity executions.
	ActivityConcurrency int

	// BuildID is this binary's deployment version. Workflows auto-upgrade
	// to the current version at their next workflow task (DESIGN-0026 §
	// Versioning), so a deploy never strands running executions on old
	// workers.
	BuildID string
}

// WorkerConfigFromEnv reads TEMPORAL_BUILD_ID, which defaults to
// BuildID(). The caller sets the task queue, deployment and any
// per-role concurrency.
func WorkerConfigFromEnv(cfg *Config) WorkerConfig {
	return WorkerConfig{
		TaskQueue:           cfg.TaskQueue,
		ActivityConcurrency: DefaultActivityConcurrency,
		BuildID:             envOr("TEMPORAL_BUILD_ID", BuildID()),
	}
}

func (wc *WorkerConfig) deployment() string {
	if wc.Deployment == "" {
		return DeploymentRC
	}

	return wc.Deployment
}

// DevBuild reports whether the build ID fell through to "dev": no
// TEMPORAL_BUILD_ID, no module version, no VCS revision. Two different
// dev images then look like one version to Temporal.
func (wc *WorkerConfig) DevBuild() bool { return wc.BuildID == devBuildID }

// NewWorker returns a worker on wc's task queue with deployment
// versioning on: the build ID is the binary version and workflows
// default to AutoUpgrade. Callers register workflows and activities,
// then Run or Start it.
func NewWorker(c client.Client, wc *WorkerConfig) worker.Worker {
	return worker.New(c, wc.TaskQueue, workerOptions(wc))
}

func workerOptions(wc *WorkerConfig) worker.Options {
	return worker.Options{
		MaxConcurrentActivityExecutionSize: wc.ActivityConcurrency,
		DeploymentOptions: worker.DeploymentOptions{
			UseVersioning: true,
			Version: worker.WorkerDeploymentVersion{
				DeploymentName: wc.deployment(),
				BuildID:        wc.BuildID,
			},
			DefaultVersioningBehavior: workflow.VersioningBehaviorAutoUpgrade,
		},
		// Worker heartbeats (on by default since SDK 1.41) report 0 for
		// CPU and memory without a provider. This one reads the pod's
		// cgroup limits, so the numbers are the container's, not the
		// node's.
		SysInfoProvider: sysinfo.SysInfoProvider(),
	}
}

// BuildID returns the binary's version: the module version for a tagged
// build, otherwise the VCS revision, otherwise "dev".
func BuildID() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return devBuildID
	}

	return buildIDFrom(info)
}

func buildIDFrom(info *debug.BuildInfo) string {
	if v := info.Main.Version; v != "" && v != develVersion {
		return v
	}

	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && s.Value != "" {
			return s.Value[:min(len(s.Value), buildIDRevisionLen)]
		}
	}

	return devBuildID
}

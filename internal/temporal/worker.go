package temporal

import (
	"fmt"
	"os"
	"runtime/debug"
	"strconv"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// DeploymentName is the Temporal worker deployment every repo-guardian
// worker belongs to.
const DeploymentName = "repo-guardian"

// DefaultActivityConcurrency is WORKER_ACTIVITY_CONCURRENCY's default:
// the activities one worker runs at once.
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

	// ActivityConcurrency caps concurrent activity executions.
	ActivityConcurrency int

	// BuildID is this binary's deployment version. Workflows auto-upgrade
	// to the current version at their next workflow task (DESIGN-0026 §
	// Versioning), so a deploy never strands running executions on old
	// workers.
	BuildID string
}

// WorkerConfigFromEnv reads WORKER_ACTIVITY_CONCURRENCY and
// TEMPORAL_BUILD_ID. The build ID defaults to BuildID().
func WorkerConfigFromEnv(cfg *Config) (WorkerConfig, error) {
	wc := WorkerConfig{
		TaskQueue:           cfg.TaskQueue,
		ActivityConcurrency: DefaultActivityConcurrency,
		BuildID:             envOr("TEMPORAL_BUILD_ID", BuildID()),
	}

	if v := os.Getenv("WORKER_ACTIVITY_CONCURRENCY"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return WorkerConfig{}, fmt.Errorf("temporal: WORKER_ACTIVITY_CONCURRENCY=%q: want a positive integer", v)
		}

		wc.ActivityConcurrency = n
	}

	return wc, nil
}

// NewWorker returns a worker on wc's task queue with deployment
// versioning on: the build ID is the binary version and workflows
// default to AutoUpgrade. Callers register workflows and activities,
// then Run or Start it.
func NewWorker(c client.Client, wc *WorkerConfig) worker.Worker {
	return worker.New(c, wc.TaskQueue, worker.Options{
		MaxConcurrentActivityExecutionSize: wc.ActivityConcurrency,
		DeploymentOptions: worker.DeploymentOptions{
			UseVersioning: true,
			Version: worker.WorkerDeploymentVersion{
				DeploymentName: DeploymentName,
				BuildID:        wc.BuildID,
			},
			DefaultVersioningBehavior: workflow.VersioningBehaviorAutoUpgrade,
		},
	})
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

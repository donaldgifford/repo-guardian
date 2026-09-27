package temporal

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"golang.org/x/mod/semver"
)

// Promotion retry pacing. Temporal refuses SetCurrentVersion until the
// version has pollers, which takes a few seconds after Start.
const (
	promoteRetryInitial = 2 * time.Second
	promoteRetryMax     = 30 * time.Second
	// promoteWarnAfter is the attempt that turns quiet retries into a
	// WARN, roughly a minute in.
	promoteWarnAfter = 5
)

// ErrNotCurrent is returned by RequireCurrentVersion when the build is
// not the deployment's current version, so Temporal dispatches it no
// workflow tasks.
var ErrNotCurrent = errors.New("temporal: build is not the deployment's current version")

type promotion int

const (
	promoteNow promotion = iota
	alreadyCurrent
	superseded
)

// decide reports what a worker running buildID does about the
// deployment's current version. Newest wins: a build replaces the
// current one unless the current is newer by semver, so a crash-looping
// pod of the previous release cannot pull a rollout backwards. A build
// ID that is not a version (a dev build, a revision) never replaces one
// that is.
func decide(current *worker.WorkerDeploymentVersion, buildID string) promotion {
	if current == nil || current.BuildID == "" {
		return promoteNow
	}

	if current.BuildID == buildID {
		return alreadyCurrent
	}

	cur, ours := semverOf(current.BuildID), semverOf(buildID)

	switch {
	case cur != "" && ours == "":
		return superseded
	case cur != "" && semver.Compare(ours, cur) < 0:
		return superseded
	default:
		return promoteNow
	}
}

// semverOf returns id as a comparable semver ("2.0.0-rc.1" and
// "v2.0.0-rc.1" alike), or "" when it is not one.
func semverOf(id string) string {
	v := id
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}

	if semver.IsValid(v) {
		return v
	}

	return ""
}

// PromoteBuild makes buildID the current version of the repo-guardian
// worker deployment, which is what lets Temporal dispatch it tasks. It
// retries until the promotion lands, a newer build is found to be
// current, or ctx ends. Call it once the worker has started.
func PromoteBuild(ctx context.Context, c client.Client, buildID string, logger *slog.Logger) error {
	h := c.WorkerDeploymentClient().GetHandle(DeploymentName)
	delay := promoteRetryInitial

	for attempt := 1; ; attempt++ {
		done, err := promoteOnce(ctx, h, buildID, logger)
		if done {
			return nil
		}

		if attempt == promoteWarnAfter {
			logger.Warn("temporal: still promoting build to the deployment's current version; no workflow tasks are dispatched until it lands",
				"build_id", buildID, "attempt", attempt, "error", err)
		} else {
			logger.Debug("temporal: promotion not accepted yet", "build_id", buildID, "attempt", attempt, "error", err)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}

		delay = min(delay*2, promoteRetryMax)
	}
}

// promoteOnce makes one promotion attempt. done reports whether there
// is nothing left to do.
func promoteOnce(ctx context.Context, h client.WorkerDeploymentHandle, buildID string, logger *slog.Logger) (done bool, err error) {
	desc, err := h.Describe(ctx, client.WorkerDeploymentDescribeOptions{})
	if err != nil {
		// NotFound until this worker's first poll creates the deployment.
		return false, fmt.Errorf("describe deployment: %w", err)
	}

	current := desc.Info.RoutingConfig.CurrentVersion

	switch decide(current, buildID) {
	case alreadyCurrent:
		logger.Info("temporal: build is the deployment's current version", "build_id", buildID)

		return true, nil
	case superseded:
		logger.Warn("temporal: a newer build is the deployment's current version; this worker will not promote itself",
			"build_id", buildID, "current_build_id", current.BuildID,
			"promote_manually", "temporal worker deployment set-current-version --deployment-name "+DeploymentName+" --build-id "+buildID)

		return true, nil
	case promoteNow:
	}

	// The conflict token makes this a compare-and-set: two pods racing
	// cannot overwrite a version set since they described it.
	if _, err := h.SetCurrentVersion(ctx, client.WorkerDeploymentSetCurrentVersionOptions{
		BuildID:       buildID,
		ConflictToken: desc.ConflictToken,
	}); err != nil {
		return false, fmt.Errorf("set current version: %w", err)
	}

	previous := ""
	if current != nil {
		previous = current.BuildID
	}

	logger.Info("temporal: promoted build to the deployment's current version", "build_id", buildID, "previous_build_id", previous)

	return true, nil
}

// RequireCurrentVersion returns ErrNotCurrent unless buildID is the
// deployment's current version.
func RequireCurrentVersion(ctx context.Context, c client.Client, buildID string) error {
	desc, err := c.WorkerDeploymentClient().GetHandle(DeploymentName).Describe(ctx, client.WorkerDeploymentDescribeOptions{})
	if err != nil {
		return fmt.Errorf("temporal: describe deployment: %w", err)
	}

	current := desc.Info.RoutingConfig.CurrentVersion
	if current == nil || current.BuildID != buildID {
		have := "none"
		if current != nil {
			have = current.BuildID
		}

		return fmt.Errorf("%w: build %q, current %q", ErrNotCurrent, buildID, have)
	}

	return nil
}

package main

import (
	"context"
	"log/slog"
	"testing"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	temporalmocks "go.temporal.io/sdk/mocks"

	"github.com/donaldgifford/repo-guardian/internal/config"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// TestServiceStarter_SchedulesPerRole is IMPL-0028 task 4.4: each role
// ensures only its own Schedules.
func TestServiceStarter_SchedulesPerRole(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{DiscoveryEnabled: true, DiscoveryInterval: time.Hour, ComplianceSnapshotInterval: 24 * time.Hour}
	s := &serviceStarter{cfg: cfg, taskQueue: "repo-guardian"}

	ensure, remove := s.schedules(config.RoleWorker)
	if len(ensure) != 2 || ensure[0].ID != workflows.DiscoveryScheduleID || ensure[1].ID != workflows.SnapshotScheduleID || len(remove) != 0 {
		t.Errorf("rc worker schedules = %+v, remove %v; want discovery and snapshot", ensure, remove)
	}

	ensure, remove = s.schedules(config.RoleEvaluator)
	if len(ensure) != 2 || ensure[0].ID != workflows.ControlsDiscoveryScheduleID || ensure[1].ID != workflows.ControlsSnapshotScheduleID ||
		ensure[0].Workflow != workflows.ControlsDiscoveryWorkflowName || len(remove) != 0 {
		t.Errorf("evaluator schedules = %+v, remove %v; want controls-discovery and controls-snapshot only", ensure, remove)
	}

	for _, role := range []config.Role{config.RoleRemediator, config.RoleIngest} {
		if ensure, remove := s.schedules(role); len(ensure) != 0 || len(remove) != 0 {
			t.Errorf("schedules(%v) = %+v, remove %v; want none of the rc's", role, ensure, remove)
		}
	}

	cfg.DiscoveryEnabled = false

	if ensure, remove := s.schedules(config.RoleWorker); len(ensure) != 1 || len(remove) != 1 || remove[0] != workflows.DiscoveryScheduleID {
		t.Errorf("rc worker with discovery off = %+v, remove %v; want snapshot only and discovery removed", ensure, remove)
	}

	ensure, remove = s.schedules(config.RoleEvaluator)
	if len(ensure) != 1 || len(remove) != 1 || remove[0] != workflows.ControlsDiscoveryScheduleID {
		t.Errorf("evaluator with discovery off = %+v, remove %v; want controls-snapshot only and controls-discovery removed", ensure, remove)
	}
}

// fakeActivation answers CurrentActivation.
type fakeActivation struct {
	version string
	at      time.Time
}

func (f fakeActivation) CurrentActivation(context.Context) (string, time.Time, error) {
	return f.version, f.at, nil
}

// startRecorder records workflow starts; the embedded mock panics on
// anything else.
type startRecorder struct {
	*temporalmocks.Client

	starts []client.StartWorkflowOptions
	names  []string
	err    error
}

//nolint:gocritic // hugeParam: the signature is client.Client's.
func (r *startRecorder) ExecuteWorkflow(_ context.Context, o client.StartWorkflowOptions, wf any, _ ...any) (client.WorkflowRun, error) {
	r.starts, r.names = append(r.starts, o), append(r.names, wf.(string))

	return &temporalmocks.WorkflowRun{}, r.err
}

// TestServiceStarter_ControlsRollout is IMPL-0029 5.6: one rollout per
// activation, keyed by version and activation time, started only by a
// pod that loaded the activated policy; a duplicate start is success.
func TestServiceStarter_ControlsRollout(t *testing.T) {
	t.Parallel()

	at := time.Unix(1_790_000_000, 0)

	tests := []struct {
		name      string
		loaded    string
		err       error
		wantStart bool
	}{
		{name: "activated policy", loaded: "controls:abc", wantStart: true},
		{name: "already started", loaded: "controls:abc", err: serviceerror.NewWorkflowExecutionAlreadyStarted("dup", "", ""), wantStart: true},
		{name: "older pod", loaded: "controls:old"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := &startRecorder{Client: temporalmocks.NewClient(t), err: tt.err}
			s := &serviceStarter{
				cfg: &config.Config{PolicyRolloutWindow: time.Hour}, client: rec, taskQueue: "repo-guardian-eval",
				logger: slog.New(slog.DiscardHandler),
			}

			if err := s.startControlsRollout(t.Context(), fakeActivation{version: "controls:abc", at: at}, tt.loaded); err != nil {
				t.Fatalf("startControlsRollout() = %v", err)
			}

			if !tt.wantStart {
				if len(rec.starts) != 0 {
					t.Errorf("starts = %+v, want none from a pod without the activated policy", rec.starts)
				}

				return
			}

			if len(rec.starts) != 1 || rec.starts[0].ID != "controls-rollout/controls:abc/1790000000" ||
				rec.starts[0].WorkflowIDReusePolicy != enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE ||
				rec.names[0] != workflows.ControlsRolloutWorkflowName || rec.starts[0].TaskQueue != "repo-guardian-eval" {
				t.Errorf("starts = %+v %v, want one REJECT_DUPLICATE ControlsRolloutWorkflow keyed by version and activation", rec.starts, rec.names)
			}
		})
	}
}

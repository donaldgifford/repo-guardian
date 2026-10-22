package main

import (
	"context"
	"log/slog"
	"slices"
	"testing"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	temporalmocks "go.temporal.io/sdk/mocks"

	"github.com/donaldgifford/repo-guardian/internal/config"
	"github.com/donaldgifford/repo-guardian/internal/temporal"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// TestServiceStarter_SchedulesPerRole is IMPL-0028 task 4.4 and
// IMPL-0030 6.5: each role ensures only its own Schedules. An evaluator
// never ensures remediation-sweep or controls-discovery-remediate, a
// remediator never the evaluation schedules, and DISCOVERY_ENABLED=false
// removes each role's own discovery only.
func TestServiceStarter_SchedulesPerRole(t *testing.T) {
	t.Parallel()

	evaluation := []string{workflows.ControlsDiscoveryScheduleID, workflows.ControlsSnapshotScheduleID}
	remediation := []string{workflows.RemediationDiscoveryScheduleID, workflows.RemediationSweepScheduleID}
	legacy := []string{"discovery", "snapshot"}

	ids := func(ss []*temporal.Schedule) []string {
		out := make([]string, 0, len(ss))
		for _, sc := range ss {
			out = append(out, sc.ID)
		}

		return out
	}

	for _, tt := range []struct {
		role      config.Role
		discovery bool
		ensure    []string
		remove    []string
	}{
		{role: config.RoleEvaluator, discovery: true, ensure: evaluation, remove: legacy},
		{role: config.RoleEvaluator, ensure: evaluation[1:], remove: append(slices.Clone(legacy), evaluation[0])},
		{role: config.RoleRemediator, discovery: true, ensure: remediation},
		{role: config.RoleRemediator, ensure: remediation[1:], remove: remediation[:1]},
		{role: config.RoleIngest, discovery: true},
		{role: config.RoleAPI, discovery: true},
	} {
		cfg := &config.Config{
			DiscoveryEnabled: tt.discovery, DiscoveryInterval: time.Hour, ComplianceSnapshotInterval: 24 * time.Hour,
			Remediation: config.RemediationConfig{SweepBatch: 50, MaintenanceBatch: 20},
		}
		s := &serviceStarter{cfg: cfg, taskQueue: "q"}

		ensure, remove := s.schedules(tt.role)
		if got := ids(ensure); !slices.Equal(got, tt.ensure) || !slices.Equal(remove, tt.remove) {
			t.Errorf("schedules(%v, discovery %v) = %v, remove %v; want %v, remove %v", tt.role, tt.discovery, got, remove, tt.ensure, tt.remove)
		}

		for _, sc := range ensure {
			if sc.TaskQueue != "q" || sc.Every <= 0 {
				t.Errorf("schedule %s = %+v, want the role's queue and a cadence", sc.ID, sc)
			}

			if sc.ID == workflows.RemediationSweepScheduleID {
				in, ok := sc.Args[0].(*workflows.RemediationSweepInput)
				if !ok || *in != (workflows.RemediationSweepInput{SweepBatch: 50, MaintenanceBatch: 20}) || sc.Every != time.Hour {
					t.Errorf("remediation-sweep = %+v, args %+v; want hourly with the configured batches", sc, sc.Args)
				}
			}
		}
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

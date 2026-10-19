package main

import (
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/config"
	"github.com/donaldgifford/repo-guardian/internal/temporal"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// TestControlsHalves is IMPL-0028 task 4.2: each controls role runs one
// worker on its own queue, and all runs one per configured App.
func TestControlsHalves(t *testing.T) {
	t.Parallel()

	tcfg := &temporal.Config{EvalTaskQueue: temporal.TaskQueueEval, RemediateTaskQueue: temporal.TaskQueueRemediate}
	both := &config.Config{EvalApp: config.AppCredentials{AppID: 1}, RemediateApp: config.AppCredentials{AppID: 2}}
	evalOnly := &config.Config{EvalApp: config.AppCredentials{AppID: 1}}

	tests := []struct {
		name  string
		cfg   *config.Config
		roles config.Role
		want  []string
	}{
		{"evaluator alone", &config.Config{}, config.RoleEvaluator, []string{temporal.TaskQueueEval}},
		{"remediator alone", &config.Config{}, config.RoleRemediator, []string{temporal.TaskQueueRemediate}},
		{"all, both Apps", both, config.RoleAll, []string{temporal.TaskQueueEval, temporal.TaskQueueRemediate}},
		{"all, Evaluation App only", evalOnly, config.RoleAll, []string{temporal.TaskQueueEval}},
		{"all, no App", &config.Config{}, config.RoleAll, nil},
		{"ingest", both, config.RoleIngest, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			halves := controlsHalves(tt.cfg, tt.roles, tcfg)

			var got []string
			for _, h := range halves {
				got = append(got, h.taskQueue)
			}

			if len(got) != len(tt.want) {
				t.Fatalf("controlsHalves queues = %v, want %v", got, tt.want)
			}

			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("controlsHalves queues = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

// TestControlsHalves_Sizing pins that each half carries its own role's
// sizing, never the other's.
func TestControlsHalves_Sizing(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		EvalApp:      config.AppCredentials{AppID: 1},
		RemediateApp: config.AppCredentials{AppID: 2},
		Evaluator:    config.WorkerSizing{Concurrency: 20, DBPoolSize: 12},
		Remediator:   config.WorkerSizing{Concurrency: 3},
	}

	halves := controlsHalves(cfg, config.RoleAll, &temporal.Config{})
	if len(halves) != 2 {
		t.Fatalf("controlsHalves = %d halves, want 2", len(halves))
	}

	if halves[0].sizing != cfg.Evaluator || halves[1].sizing != cfg.Remediator {
		t.Errorf("sizing = %+v, %+v, want %+v, %+v", halves[0].sizing, halves[1].sizing, cfg.Evaluator, cfg.Remediator)
	}
}

// TestHalfSizing is IMPL-0030 1.9: the remediator defaults its pool to
// concurrency + 4 and refuses one smaller than its concurrency; the
// evaluator keeps its configured size or pgxpool's default.
func TestHalfSizing(t *testing.T) {
	eval := controlsHalf{name: cmdEvaluator, app: workflows.AppEval}
	rem := func(pool int32) controlsHalf {
		return controlsHalf{name: cmdRemediator, app: workflows.AppRemediate, sizing: config.WorkerSizing{Concurrency: 10, DBPoolSize: pool}}
	}

	tests := []struct {
		name    string
		half    controlsHalf
		want    int32
		wantErr bool
	}{
		{name: "evaluator default", half: eval, want: 0},
		{name: "remediator default", half: rem(0), want: 14},
		{name: "remediator equal", half: rem(10), want: 10},
		{name: "remediator too small", half: rem(9), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, got, err := halfSizing(tt.half)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Errorf("halfSizing(%s) pool = %d, %v; want %d, error %v", tt.half.name, got, err, tt.want, tt.wantErr)
			}
		})
	}
}

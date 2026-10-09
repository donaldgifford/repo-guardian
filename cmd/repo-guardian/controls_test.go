package main

import (
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/config"
	"github.com/donaldgifford/repo-guardian/internal/temporal"
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
		{"all, rc single App", &config.Config{}, config.RoleAll, nil},
		{"rc worker", both, config.RoleWorker, nil},
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

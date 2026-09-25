package workflows

import (
	"path/filepath"
	"testing"

	"go.temporal.io/sdk/worker"
)

// The replay suite (IMPL-0025 OQ13) replays captured histories against
// the current workflow code. RepoWorkflow runs for months, so a change
// that alters the commands an old history recorded breaks every running
// execution at its next workflow task. This catches that in CI instead:
// a non-deterministic edit fails here. Guard real changes with
// workflow.GetVersion.
//
// Histories live in testdata/histories. Capture from the integration
// suite with:
//
//	go test -tags integration -run TestIntegration_OneCheck ./internal/activities -update-histories
//
// and add homelab histories with `temporal workflow show --output json`.
func TestReplay_CapturedHistories(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob(filepath.Join("testdata", "histories", "*.json"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	if len(files) == 0 {
		t.Fatal("no captured histories; the replay suite would be vacuous")
	}

	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			t.Parallel()

			replayer := worker.NewWorkflowReplayer()
			Register(replayer)

			if err := replayer.ReplayWorkflowHistoryFromJSONFile(nil, file); err != nil {
				t.Errorf("replay %s: %v", file, err)
			}
		})
	}
}

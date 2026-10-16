package examples_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/controls/builtin"
	"github.com/donaldgifford/repo-guardian/internal/policy"
)

var update = flag.Bool("update", false, "rewrite the policy example goldens")

// policyExamples are the controls-model example policy roots under
// examples/policy/. A new example MUST be added here so it is loaded
// through the strict loader and its summary pinned (DESIGN-0033
// AR-0033-01: an example cannot diverge from its schema).
var policyExamples = []string{"minimal", "full", "multi-org", "renovate", "enterprise"}

// TestPolicyExamples_Summary loads each example with every built-in
// control type and compares its Summarize() with the committed golden
// at examples/policy/testdata/<name>.summary.json. Rewrite with
// go test ./examples -run TestPolicyExamples_Summary -update.
func TestPolicyExamples_Summary(t *testing.T) {
	for _, name := range policyExamples {
		t.Run(name, func(t *testing.T) {
			var warnings bytes.Buffer

			logger := slog.New(slog.NewTextHandler(&warnings, &slog.HandlerOptions{Level: slog.LevelWarn}))

			snap, err := policy.Load(filepath.Join(examplesDir(), "policy", name), builtin.Registry(), logger)
			if err != nil {
				t.Fatalf("Load(%s): %v", name, err)
			}

			if warnings.Len() > 0 {
				t.Errorf("Load(%s) warned; an example must load clean:\n%s", name, warnings.String())
			}

			got, err := json.MarshalIndent(snap.Summarize(), "", "  ")
			if err != nil {
				t.Fatal(err)
			}

			got = append(got, '\n')
			golden := filepath.Join(examplesDir(), "policy", "testdata", name+".summary.json")

			if *update {
				if err := os.WriteFile(golden, got, 0o600); err != nil {
					t.Fatal(err)
				}

				return
			}

			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("reading golden (run with -update): %v", err)
			}

			if string(got) != string(want) {
				t.Errorf("Summarize(%s) differs from %s; rerun with -update if the change is intended\ngot:\n%s", name, golden, got)
			}
		})
	}
}

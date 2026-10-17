package examples_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/donaldgifford/repo-guardian/internal/controls/builtin"
	"github.com/donaldgifford/repo-guardian/internal/policy"
)

var update = flag.Bool("update", false, "rewrite the policy example goldens")

func examplesDir() string {
	_, file, _, _ := runtime.Caller(0)

	return filepath.Dir(file)
}

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

// valuesExamples maps each example values file to the policy root its
// policy.files map mirrors.
var valuesExamples = map[string]string{
	"values-with-policy.yaml": "full",
	"values-multi-org.yaml":   "multi-org",
}

// TestValuesExamples_MatchPolicyRoots pins that each values file's
// nested policy.files map is the example policy root byte for byte: a
// string is a file, a map is a directory (IMPL-0029 7.9, OQ11).
func TestValuesExamples_MatchPolicyRoots(t *testing.T) {
	for file, root := range valuesExamples {
		t.Run(file, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(examplesDir(), file))
			if err != nil {
				t.Fatal(err)
			}

			var values struct {
				Policy struct {
					Files map[string]any `yaml:"files"`
				} `yaml:"policy"`
			}

			if err := yaml.Unmarshal(raw, &values); err != nil {
				t.Fatalf("parse %s: %v", file, err)
			}

			got := make(map[string]string)
			flattenPolicyFiles(t, "", values.Policy.Files, got)

			want := make(map[string]string)
			dir := filepath.Join(examplesDir(), "policy", root)

			err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return err
				}

				content, err := os.ReadFile(path)
				if err != nil {
					return err
				}

				rel, err := filepath.Rel(dir, path)
				if err != nil {
					return err
				}

				want[filepath.ToSlash(rel)] = string(content)

				return nil
			})
			if err != nil {
				t.Fatal(err)
			}

			if !maps.Equal(got, want) {
				t.Errorf("%s policy.files differs from examples/policy/%s:\n got paths %v\nwant paths %v",
					file, root, slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want)))
			}
		})
	}
}

func flattenPolicyFiles(t *testing.T, prefix string, node map[string]any, out map[string]string) {
	t.Helper()

	for name, v := range node {
		path := prefix + name

		switch v := v.(type) {
		case string:
			out[path] = v
		case map[string]any:
			flattenPolicyFiles(t, path+"/", v, out)
		default:
			t.Fatalf("policy.files %s is %T, want a string or a map", path, v)
		}
	}
}

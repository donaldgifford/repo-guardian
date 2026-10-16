package policy

import (
	"bytes"
	"encoding/json"
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the resolution goldens")

// resolutionCase is a golden case's repo.json.
type resolutionCase struct {
	Repository Repository `json:"repository"`
	Access     AppAccess  `json:"access"`
}

// TestResolve_Goldens runs testdata/resolution/<case>: the policy
// directory plus the test catalogue, resolved for repo.json, against
// expected.json (D18). Regenerate with -update, only on purpose.
func TestResolve_Goldens(t *testing.T) {
	t.Parallel()

	dirs, err := filepath.Glob("testdata/resolution/*")
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no resolution goldens: %v", err)
	}

	for _, dir := range dirs {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			t.Parallel()

			snap := mustLoad(t, readPolicyDir(t, filepath.Join(dir, "policy")))

			var in resolutionCase

			raw, err := os.ReadFile(filepath.Join(dir, "repo.json"))
			if err != nil {
				t.Fatal(err)
			}

			if err := json.Unmarshal(raw, &in); err != nil {
				t.Fatalf("repo.json: %v", err)
			}

			got, err := json.MarshalIndent(snap.Resolve(in.Repository, in.Access), "", "  ")
			if err != nil {
				t.Fatal(err)
			}

			got = append(got, '\n')
			path := filepath.Join(dir, "expected.json")

			if *update {
				if err := os.WriteFile(path, got, 0o600); err != nil {
					t.Fatal(err)
				}

				return
			}

			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (run with -update to create it)", err)
			}

			if !bytes.Equal(got, want) {
				t.Errorf("Resolve() for %s =\n%s\nwant\n%s", dir, got, want)
			}
		})
	}
}

// readPolicyDir reads a golden's policy directory as root-relative files.
func readPolicyDir(t *testing.T, root string) map[string]string {
	t.Helper()

	files := make(map[string]string)

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(root, p)
		files[filepath.ToSlash(rel)] = string(b)

		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	return files
}

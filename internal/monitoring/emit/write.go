package emit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// File modes.
//
// Group-readable, never world-readable: a generated dashboard names
// every org and rule in the fleet, which is the same class of
// information internal/report withholds from world access. Note this
// governs the operator's --out directory only — the static tier
// committed to contrib/ is a public artifact and git does not carry
// these bits across a checkout anyway.
const (
	dirPerm  = 0o750
	filePerm = 0o640
)

// Write writes the artifacts under dir and returns the paths written.
func Write(dir string, artifacts []Artifact) ([]string, error) {
	paths := make([]string, 0, len(artifacts))

	for i := range artifacts {
		a := &artifacts[i]

		if err := checkPath(a.Path); err != nil {
			return nil, err
		}

		full := filepath.Join(dir, filepath.FromSlash(a.Path))

		if err := os.MkdirAll(filepath.Dir(full), dirPerm); err != nil {
			return nil, fmt.Errorf("emit: create %s: %w", filepath.Dir(full), err)
		}

		if err := os.WriteFile(full, a.Content, filePerm); err != nil {
			return nil, fmt.Errorf("emit: write %s: %w", full, err)
		}

		paths = append(paths, full)
	}

	return paths, nil
}

// checkPath refuses an artifact path that could escape the output
// directory.
//
// Every path in this package is built from a slug ValidateSuite has
// already constrained, so this is belt and braces — but it is cheap,
// and it is the difference between a future slug source (a config
// value, an org name) being a bug and being a directory traversal.
func checkPath(p string) error {
	switch {
	case p == "":
		return fmt.Errorf("emit: empty artifact path")
	case filepath.IsAbs(p) || strings.HasPrefix(p, "/"):
		return fmt.Errorf("emit: refusing absolute artifact path %q", p)
	case strings.Contains(p, ".."):
		return fmt.Errorf("emit: refusing artifact path %q: contains a parent-directory reference", p)
	}

	return nil
}

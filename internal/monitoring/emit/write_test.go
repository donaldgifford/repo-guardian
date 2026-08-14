package emit_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/monitoring/emit"
)

// TestWrite_CreatesTheTreeAndReturnsPaths pins the writer.
func TestWrite_CreatesTheTreeAndReturnsPaths(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	got, err := emit.Write(dir, []emit.Artifact{
		{Path: "dashboards/rg-first.json", Content: []byte("{}\n")},
		{Path: "alerts/rules.yaml", Content: []byte("---\ngroups: []\n")},
	})
	if err != nil {
		t.Fatalf("Write() = %v, want nil", err)
	}

	want := []string{
		filepath.Join(dir, "dashboards", "rg-first.json"),
		filepath.Join(dir, "alerts", "rules.yaml"),
	}

	if !slices.Equal(got, want) {
		t.Errorf("Write() = %v, want %v", got, want)
	}

	for _, p := range want {
		body, err := os.ReadFile(p)
		if err != nil {
			t.Errorf("ReadFile(%s) = %v, want nil", p, err)

			continue
		}

		if len(body) == 0 {
			t.Errorf("%s is empty", p)
		}
	}
}

// TestWrite_FilesAreNotWorldReadable pins the mode.
//
// A generated dashboard names every org and rule in the fleet, which is
// the class of information internal/report already withholds from world
// access. This governs the operator's --out directory; the static tier
// committed to contrib/ is a public artifact and git does not carry
// these bits across a checkout.
func TestWrite_FilesAreNotWorldReadable(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	got, err := emit.Write(dir, []emit.Artifact{{Path: "alerts/rules.yaml", Content: []byte("x")}})
	if err != nil {
		t.Fatalf("Write() = %v, want nil", err)
	}

	info, err := os.Stat(got[0])
	if err != nil {
		t.Fatalf("Stat() = %v, want nil", err)
	}

	if mode := info.Mode().Perm(); mode&0o007 != 0 {
		t.Errorf("mode = %o, want no world access", mode)
	}
}

// TestWrite_RefusesEscapingPaths pins the traversal guard.
//
// Every path this package builds comes from a slug ValidateSuite has
// already constrained, so this is belt and braces — but it is the
// difference between a future slug source (a config value, an org name)
// being a bug and being a directory traversal.
func TestWrite_RefusesEscapingPaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
	}{
		{name: "parent reference", path: "../escaped.yaml"},
		{name: "parent reference mid-path", path: "dashboards/../../escaped.yaml"},
		{name: "absolute", path: "/etc/escaped.yaml"},
		{name: "empty", path: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()

			_, err := emit.Write(dir, []emit.Artifact{{Path: tt.path, Content: []byte("x")}})
			if err == nil {
				t.Fatalf("Write(%q) = nil, want an error", tt.path)
			}

			if !strings.Contains(err.Error(), "emit:") {
				t.Errorf("Write(%q) = %v, want an emit-prefixed error", tt.path, err)
			}
		})
	}
}

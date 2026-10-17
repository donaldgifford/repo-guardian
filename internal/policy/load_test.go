package policy

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/control"
)

// writeRoot writes files (slash paths) under a fresh directory and
// returns it.
func writeRoot(t *testing.T, files map[string]string) string {
	t.Helper()

	dir := t.TempDir()

	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	return dir
}

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

const minimalEnterprise = `enterprise {
  orgs     = ["acme"]
  controls = []
}
`

func TestLoad_Layout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"no enterprise", map[string]string{"catalogue/a.hcl": ""}, "policy root has no enterprise.hcl"},
		{"stray hcl", map[string]string{"enterprise.hcl": minimalEnterprise, "extra.hcl": ""}, "extra.hcl: unexpected HCL file"},
		{
			"nested catalogue",
			map[string]string{"enterprise.hcl": minimalEnterprise, "catalogue/x/a.hcl": ""},
			"catalogue/x/a.hcl: unexpected HCL file",
		},
		{"syntax error", map[string]string{"enterprise.hcl": "enterprise {"}, "enterprise.hcl:1"},
		{"variable", map[string]string{"enterprise.hcl": "enterprise {\n  mode = var.mode\n}\n"}, "enterprise.hcl:2"},
		{"other function", map[string]string{"enterprise.hcl": "enterprise {\n  mode = upper(\"x\")\n}\n"}, "Call to unknown function"},
		{"file escapes", map[string]string{"enterprise.hcl": "enterprise {\n  mode = file(\"../x\")\n}\n"}, "stay inside it"},
		{"file missing", map[string]string{"enterprise.hcl": "enterprise {\n  mode = file(\"nope\")\n}\n"}, "no such file"},
		{
			"too large",
			map[string]string{"enterprise.hcl": minimalEnterprise, "templates/big.tmpl": strings.Repeat("x", MaxFileBytes+1)},
			"templates/big.tmpl: 1048577 bytes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := Load(writeRoot(t, tt.files), control.NewRegistry(), discard())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Load() err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestLoad_FileFunctionReadsUnderRoot(t *testing.T) {
	t.Parallel()

	root := writeRoot(t, map[string]string{
		"enterprise.hcl": "enterprise {\n  orgs = [\"acme\"]\n  mode = file(\"data/mode.txt\")\n}\n",
		"data/mode.txt":  "evaluate",
	})

	if _, err := Load(root, control.NewRegistry(), discard()); err != nil {
		t.Fatalf("Load() err = %v", err)
	}
}

func TestLoad_SymlinkEscapeIsRefused(t *testing.T) {
	t.Parallel()

	outside := writeRoot(t, map[string]string{"secret.tmpl": "x"})
	root := writeRoot(t, map[string]string{"enterprise.hcl": minimalEnterprise})

	if err := os.MkdirAll(filepath.Join(root, "templates"), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(filepath.Join(outside, "secret.tmpl"), filepath.Join(root, "templates", "secret.tmpl")); err != nil {
		t.Fatal(err)
	}

	_, err := Load(root, control.NewRegistry(), discard())
	if err == nil || !strings.Contains(err.Error(), "templates/secret.tmpl: resolves outside the policy root") {
		t.Errorf("Load() err = %v, want the escaping symlink refused", err)
	}
}

// TestLoad_KubeletProjection lays the root out the way a ConfigMap
// volume does: data under ..<timestamp>/, ..data pointing at it, and
// each top-level name a symlink through ..data.
func TestLoad_KubeletProjection(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	stamp := filepath.Join(root, "..2026_10_16_12_00_00.000000001")

	for name, body := range map[string]string{"enterprise.hcl": minimalEnterprise, "templates/codeowners.tmpl": "* @acme/a\n"} {
		p := filepath.Join(stamp, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	links := map[string]string{
		"..data":         filepath.Base(stamp),
		"enterprise.hcl": filepath.Join("..data", "enterprise.hcl"),
		"templates":      filepath.Join("..data", "templates"),
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}

	snap, err := Load(root, control.NewRegistry(), discard())
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	if _, ok := snap.files["templates/codeowners.tmpl"]; !ok || len(snap.files) != 2 {
		t.Errorf("files = %v, want enterprise.hcl and templates/codeowners.tmpl keyed relative to ..data", keys(snap.files))
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	return out
}

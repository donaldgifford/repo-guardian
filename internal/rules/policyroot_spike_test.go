package rules

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// NOTE: IMPL-0028 task 0.12 (INV-0022 spike 7). Prototypes reading a
// policy root that a ConfigMap volume projects with nested paths
// (policy/templates/...), and hashing it with the embedded fallback.
// IMPL-0029 builds the real policy-root reader; this test records what
// that reader must handle.

// atomicWriterLayout lays files out the way kubelet's atomic writer
// projects a ConfigMap volume with items[].path: the data lives in a
// timestamped directory, ..data points at it, and each top-level path
// component is a symlink through ..data.
func atomicWriterLayout(t *testing.T, files map[string]string) string {
	t.Helper()

	root := t.TempDir()
	ts := filepath.Join(root, "..2026_10_07_06_00_00.000000001")

	top := map[string]bool{}

	for p, content := range files {
		full := filepath.Join(ts, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}

		top[strings.SplitN(p, "/", 2)[0]] = true
	}

	if err := os.Symlink(filepath.Base(ts), filepath.Join(root, "..data")); err != nil {
		t.Fatal(err)
	}

	for name := range top {
		if err := os.Symlink(filepath.Join("..data", name), filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}

	return root
}

// naiveWalk is filepath.WalkDir over the root: it does not follow
// symlinks, so a projected subdirectory is invisible, and it would descend
// into the timestamped directory.
func naiveWalk(t *testing.T, root string) []string {
	t.Helper()

	var out []string

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !d.IsDir() && d.Type()&fs.ModeSymlink == 0 {
			rel, _ := filepath.Rel(root, p)
			out = append(out, rel)
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	slices.Sort(out)

	return out
}

// readRoot is the prototype: walk ..data when it exists (a projected
// volume, one consistent snapshot), else the root itself (a plain
// directory, as in local runs and tests). Names starting with ".." are
// kubelet's and never policy files.
func readRoot(root string) (map[string][]byte, error) {
	base := root
	if fi, err := os.Lstat(filepath.Join(root, "..data")); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
		base = filepath.Join(root, "..data")
	}

	out := map[string][]byte{}

	start := base + string(filepath.Separator)

	err := filepath.WalkDir(start, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if p != start && strings.HasPrefix(d.Name(), "..") {
			if d.IsDir() {
				return filepath.SkipDir
			}

			return nil
		}

		if d.IsDir() {
			return nil
		}

		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(base, p)
		if err != nil {
			return err
		}

		out[filepath.ToSlash(rel)] = b

		return nil
	})

	return out, err
}

// hashRoot hashes the root's files plus every embedded template the root
// does not shadow, in path order, length-prefixed so no two inputs
// collide by concatenation.
func hashRoot(files map[string][]byte, embedded map[string]string) string {
	h := sha256.New()

	write := func(kind, name string, b []byte) {
		h.Write([]byte(kind + "\x00" + name + "\x00"))
		h.Write([]byte{byte(len(b) >> 24), byte(len(b) >> 16), byte(len(b) >> 8), byte(len(b))})
		h.Write(b)
	}

	for _, p := range slices.Sorted(maps.Keys(files)) {
		write("root", p, files[p])
	}

	for _, name := range slices.Sorted(maps.Keys(embedded)) {
		if _, shadowed := files["templates/"+name+".tmpl"]; shadowed {
			continue
		}

		write("embedded", name, []byte(embedded[name]))
	}

	return hex.EncodeToString(h.Sum(nil))
}

func TestSpike_PolicyRoot(t *testing.T) {
	files := map[string]string{
		"main.hcl":                  `enterprise { orgs = ["acme"] }`,
		"orgs/acme.hcl":             `org "acme" {}`,
		"templates/codeowners.tmpl": "* @acme/platform\n",
	}

	root := atomicWriterLayout(t, files)

	t.Logf("naive WalkDir sees %v", naiveWalk(t, root))

	got, err := readRoot(root)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != len(files) {
		t.Fatalf("readRoot read %d files, want %d: %v", len(got), len(files), got)
	}

	for p, want := range files {
		if string(got[p]) != want {
			t.Errorf("readRoot[%q] = %q, want %q", p, got[p], want)
		}
	}

	ts := NewTemplateStore()
	if err := ts.Load(""); err != nil {
		t.Fatal(err)
	}

	embedded := ts.AsMap()
	base := hashRoot(got, embedded)

	// A template edit, an embedded-default change and a shadowing template
	// each move the hash; re-reading the same projection does not.
	again, _ := readRoot(root)
	if hashRoot(again, embedded) != base {
		t.Error("hash is not stable across reads")
	}

	edited := maps.Clone(got)
	edited["templates/codeowners.tmpl"] = []byte("* @acme/security\n")

	if hashRoot(edited, embedded) == base {
		t.Error("a template edit did not move the hash")
	}

	upgraded := maps.Clone(embedded)

	for k := range upgraded {
		if k != "codeowners" {
			upgraded[k] += "\n# changed by a binary upgrade"
			break
		}
	}

	if hashRoot(got, upgraded) == base {
		t.Error("an embedded-default change did not move the hash")
	}

	// The root's codeowners shadows the embedded one, so changing the
	// embedded codeowners must NOT move the hash.
	shadowedOnly := maps.Clone(embedded)

	shadowedOnly["codeowners"] += "\n# shadowed"

	if hashRoot(got, shadowedOnly) != base {
		t.Error("a change to a shadowed embedded template moved the hash")
	}

	t.Logf("embedded templates: %d, hash %s", len(embedded), base[:12])
}

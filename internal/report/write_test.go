package report

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/store"
)

// TestWriteAll_OneFilePerOrg pins the file layout and its permissions.
//
// One file per org is an access-control decision, not a formatting one:
// a combined document invites sending the whole fleet's weaknesses to a
// team that should only see their own. The 0640 mode is the same
// reasoning applied to the filesystem.
func TestWriteAll_OneFilePerOrg(t *testing.T) {
	t.Parallel()

	data := fullData()
	data.Current = append(data.Current, store.SnapshotRow{
		Org: "globex", RuleName: "dependabot", ActionableCount: 1, TrackedCount: 3,
	})

	r := newRenderer(t, nil)

	dir := filepath.Join(t.TempDir(), "reports")

	paths, err := r.WriteAll(dir, r.Build(data))
	if err != nil {
		t.Fatalf("WriteAll() = %v, want nil", err)
	}

	want := []string{filepath.Join(dir, "acme.md"), filepath.Join(dir, "globex.md")}
	if len(paths) != len(want) {
		t.Fatalf("WriteAll() = %v, want %v", paths, want)
	}

	for i, p := range paths {
		if p != want[i] {
			t.Errorf("WriteAll()[%d] = %q, want %q", i, p, want[i])
		}

		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("Stat(%s) = %v, want nil", p, err)
		}

		if perm := info.Mode().Perm(); perm != fs.FileMode(filePerm) {
			t.Errorf("%s mode = %v, want %v; reports name internal repositories and their weaknesses", p, perm, fs.FileMode(filePerm))
		}
	}

	body, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatalf("ReadFile(%s) = %v, want nil", paths[0], err)
	}

	if !strings.Contains(string(body), "Compliance report: acme") {
		t.Errorf("acme.md does not contain the acme report:\n%s", body)
	}

	if strings.Contains(string(body), "globex") {
		t.Errorf("acme.md leaked another org's data:\n%s", body)
	}
}

// TestWriteAll_CreatesTheDirectory pins that --out need not exist.
func TestWriteAll_CreatesTheDirectory(t *testing.T) {
	t.Parallel()

	r := newRenderer(t, nil)
	dir := filepath.Join(t.TempDir(), "nested", "reports")

	if _, err := r.WriteAll(dir, r.Build(fullData())); err != nil {
		t.Fatalf("WriteAll() = %v, want nil", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Stat(%s) = %v, want nil", dir, err)
	}

	if perm := info.Mode().Perm(); perm != fs.FileMode(dirPerm) {
		t.Errorf("%s mode = %v, want %v", dir, perm, fs.FileMode(dirPerm))
	}
}

// TestFilename_RejectsRatherThanSanitizes pins the deliberate choice.
//
// Sanitizing is the tempting option and the wrong one: any rewriting
// rule can map two distinct orgs onto one filename, so one report would
// silently overwrite another's. GitHub org names cannot contain these
// characters, so every case here is unreachable in practice and exists
// to stay that way.
func TestFilename_RejectsRatherThanSanitizes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		org     string
		want    string
		wantErr bool
	}{
		{name: "ordinary", org: "acme", want: "acme.md"},
		{name: "hyphenated", org: "acme-corp", want: "acme-corp.md"},
		{name: "empty", org: "", wantErr: true},
		{name: "dot", org: ".", wantErr: true},
		{name: "dotdot", org: "..", wantErr: true},
		{name: "forward slash", org: "acme/evil", wantErr: true},
		{name: "backslash", org: `acme\evil`, wantErr: true},
		{name: "traversal", org: "..%2fetc", wantErr: true},
		{name: "embedded traversal", org: "a..b", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := filename(tt.org)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("filename(%q) = %q, nil; want an error rather than a rewritten name", tt.org, got)
				}

				return
			}

			if err != nil {
				t.Fatalf("filename(%q) = %v, want nil", tt.org, err)
			}

			if got != tt.want {
				t.Errorf("filename(%q) = %q, want %q", tt.org, got, tt.want)
			}
		})
	}
}

// TestWriteAll_RefusesABadOrgName pins that the rejection reaches the
// caller instead of writing a file somewhere unexpected.
func TestWriteAll_RefusesABadOrgName(t *testing.T) {
	t.Parallel()

	r := newRenderer(t, nil)
	dir := t.TempDir()

	_, err := r.WriteAll(dir, []Org{{Name: "../escape", GeneratedAt: fixedNow}})
	if err == nil {
		t.Fatal("WriteAll() = nil, want an error for an org name containing a separator")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%s) = %v, want nil", dir, err)
	}

	if len(entries) != 0 {
		t.Errorf("WriteAll() wrote %v despite rejecting the org name", entries)
	}
}

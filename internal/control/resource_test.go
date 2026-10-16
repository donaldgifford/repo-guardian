package control

import (
	"strings"
	"testing"
)

func TestCanonical(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		kind    string
		key     string
		want    string
		wantErr bool
	}{
		{name: "file", kind: KindFile, key: ".github/CODEOWNERS", want: ".github/CODEOWNERS"},
		{name: "file dot segment", kind: KindFile, key: ".github/./CODEOWNERS", want: ".github/CODEOWNERS"},
		{name: "file case kept", kind: KindFile, key: "Docs/CODEOWNERS", want: "Docs/CODEOWNERS"},
		{name: "file parent", kind: KindFile, key: "../x", wantErr: true},
		{name: "file inner parent", kind: KindFile, key: "a/../b", wantErr: true},
		{name: "file absolute", kind: KindFile, key: "/x", wantErr: true},
		{name: "file empty segment", kind: KindFile, key: "a//b", wantErr: true},
		{name: "file trailing slash", kind: KindFile, key: "a/", wantErr: true},
		{name: "file leading dot slash", kind: KindFile, key: "./x", wantErr: true},
		{name: "file root", kind: KindFile, key: ".", wantErr: true},
		{name: "file empty", kind: KindFile, key: "", wantErr: true},
		{name: "label lower-cased", kind: KindLabel, key: "Bug", want: "bug"},
		{name: "setting exact", kind: KindSetting, key: "delete_branch_on_merge", want: "delete_branch_on_merge"},
		{name: "ruleset exact", kind: KindRuleset, key: "Main Protection", want: "Main Protection"},
		{name: "property exact", kind: KindProperty, key: "Owner", want: "Owner"},
		{name: "tree exact", kind: KindTree, key: ".github/workflows", want: ".github/workflows"},
		{name: "unknown kind", kind: "branch", key: "main", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := Canonical(tt.kind, tt.key)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Canonical(%q, %q) error = %v, wantErr %v", tt.kind, tt.key, err, tt.wantErr)
			}

			if !tt.wantErr && got != (Resource{Kind: tt.kind, Key: tt.want}) {
				t.Errorf("Canonical(%q, %q) = %v, want %s:%s", tt.kind, tt.key, got, tt.kind, tt.want)
			}
		})
	}
}

func TestCanonical_SameFileSpellingsCollide(t *testing.T) {
	t.Parallel()

	a := MustCanonical(KindFile, ".github/./CODEOWNERS")
	b := MustCanonical(KindFile, ".github/CODEOWNERS")

	if a != b {
		t.Errorf("%v and %v name one file but differ", a, b)
	}

	if MustCanonical(KindLabel, "Bug") != MustCanonical(KindLabel, "bug") {
		t.Error("labels Bug and bug differ")
	}
}

func FuzzCanonicalFile(f *testing.F) {
	seeds := []string{".github/CODEOWNERS", ".github/./CODEOWNERS", "../x", "/x", "a//b", "./x", "a/./././b", "...", "a/.../b"}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, key string) {
		r, err := Canonical(KindFile, key)
		if err != nil {
			return
		}

		again, err := Canonical(KindFile, r.Key)
		if err != nil || again != r {
			t.Fatalf("Canonical(%q) = %q is not a fixed point: %q, %v", key, r.Key, again.Key, err)
		}

		if escapesRoot(r.Key) {
			t.Fatalf("Canonical(%q) = %q escapes the repository root", key, r.Key)
		}
	})
}

func escapesRoot(key string) bool {
	return strings.HasPrefix(key, "/") || key == ".." || strings.HasPrefix(key, "../") ||
		strings.Contains(key, "/../") || strings.HasSuffix(key, "/..")
}

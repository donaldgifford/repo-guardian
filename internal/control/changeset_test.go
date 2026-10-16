package control

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateChangeSet(t *testing.T) {
	t.Parallel()

	codeowners := MustCanonical(KindFile, ".github/CODEOWNERS")
	setting := MustCanonical(KindSetting, "has_wiki")

	c := newStub("exists", "wiki", "other")
	c.owns = []Resource{codeowners, setting}

	failing := Evaluation{Results: []RuleResult{result("exists", Fail), result("wiki", Fail), result("other", Pass)}}
	file := func(p string) FileChange { return FileChange{Path: p, Content: []byte("x")} }

	tests := []struct {
		name string
		ev   Evaluation
		cs   ChangeSet
		want string // empty: valid
	}{
		{
			name: "valid",
			ev:   failing,
			cs: ChangeSet{
				Files: []FileChange{file(".github/CODEOWNERS")},
				API:   []APIChange{{Resource: setting, Op: "set", Value: false}},
				Fixed: []RuleID{"exists", "wiki"},
			},
		},
		{
			name: "valid recommendation",
			ev:   failing,
			cs:   ChangeSet{Manual: []RuleID{"exists"}, Blocked: []RuleID{"wiki"}, Notes: []string{"add it", "cannot read"}},
		},
		{
			name: "duplicate path",
			ev:   failing,
			cs:   ChangeSet{Files: []FileChange{file(".github/CODEOWNERS"), file(".github/./CODEOWNERS")}, Fixed: []RuleID{"exists", "wiki"}},
			want: "changed twice",
		},
		{name: "bad path", ev: failing, cs: ChangeSet{Files: []FileChange{file("../x")}, Fixed: []RuleID{"exists", "wiki"}}, want: `".." segment`},
		{
			name: "file not owned",
			ev:   failing,
			cs:   ChangeSet{Files: []FileChange{file("CODEOWNERS")}, Fixed: []RuleID{"exists", "wiki"}},
			want: "not owned",
		},
		{
			name: "api not owned",
			ev:   failing,
			cs:   ChangeSet{API: []APIChange{{Resource: MustCanonical(KindSetting, "has_issues"), Op: "set"}}, Fixed: []RuleID{"exists", "wiki"}},
			want: "not owned",
		},
		{
			name: "too many files",
			ev:   failing,
			cs:   ChangeSet{Files: make([]FileChange, MaxChangeSetFiles+1), Fixed: []RuleID{"exists", "wiki"}},
			want: "files, over",
		},
		{
			name: "file too big",
			ev:   failing,
			cs: ChangeSet{
				Files: []FileChange{{Path: ".github/CODEOWNERS", Content: make([]byte, MaxFileBytes+1)}},
				Fixed: []RuleID{"exists", "wiki"},
			},
			want: "bytes, over",
		},
		{
			name: "symlink written",
			ev: Evaluation{
				Results: failing.Results,
				Reads:   []ResourceRead{{Resource: codeowners, State: ReadError, Err: NotRegularFile}},
			},
			cs:   ChangeSet{Files: []FileChange{file(".github/CODEOWNERS")}, Fixed: []RuleID{"exists", "wiki"}},
			want: "symlink or submodule",
		},
		{name: "failing rule unaccounted", ev: failing, cs: ChangeSet{Fixed: []RuleID{"exists"}}, want: `failing rule "wiki"`},
		{
			name: "rule in two lists",
			ev:   failing,
			cs:   ChangeSet{Fixed: []RuleID{"exists", "wiki"}, Manual: []RuleID{"wiki"}, Notes: []string{"n"}},
			want: "in both",
		},
		{
			name: "undeclared rule",
			ev:   failing,
			cs:   ChangeSet{Fixed: []RuleID{"exists", "wiki"}, Manual: []RuleID{"nope"}, Notes: []string{"n"}},
			want: "undeclared",
		},
		{name: "fixed but passing", ev: failing, cs: ChangeSet{Fixed: []RuleID{"exists", "wiki", "other"}}, want: "in Fixed but not failing"},
		{name: "manual without note", ev: failing, cs: ChangeSet{Fixed: []RuleID{"exists"}, Manual: []RuleID{"wiki"}}, want: "notes"},
		{name: "blocked without note", ev: failing, cs: ChangeSet{Blocked: []RuleID{"exists", "wiki"}, Notes: []string{"one"}}, want: "notes"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := ValidateChangeSet(c, tt.ev, tt.cs)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("ValidateChangeSet() = %v, want nil", err)
				}

				return
			}

			if !errors.Is(err, ErrInvalidChangeSet) || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("ValidateChangeSet() = %v, want ErrInvalidChangeSet mentioning %q", err, tt.want)
			}
		})
	}
}

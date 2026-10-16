package control

import "testing"

func TestParseID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in      string
		want    ID
		wantErr bool
	}{
		{in: "codeowners@2", want: ID{Slug: "codeowners", Version: 2}},
		{in: "repo_settings@1", want: ID{Slug: "repo_settings", Version: 1}},
		{in: "branch-ruleset@10", want: ID{Slug: "branch-ruleset", Version: 10}},
		{in: "codeowners", wantErr: true},
		{in: "codeowners@0", wantErr: true},
		{in: "codeowners@-1", wantErr: true},
		{in: "codeowners@01", wantErr: true},
		{in: "codeowners@x", wantErr: true},
		{in: "Codeowners@1", wantErr: true},
		{in: "1codeowners@1", wantErr: true},
		{in: "code__owners@1", wantErr: true},
		{in: "codeowners-@1", wantErr: true},
		{in: "@1", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()

			got, err := ParseID(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseID(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			}

			if got != tt.want {
				t.Errorf("ParseID(%q) = %+v, want %+v", tt.in, got, tt.want)
			}

			if !tt.wantErr && got.String() != tt.in {
				t.Errorf("ParseID(%q).String() = %q, want the input back", tt.in, got.String())
			}
		})
	}
}

func TestResourceString(t *testing.T) {
	t.Parallel()

	if got := (Resource{Kind: KindFile, Key: ".github/CODEOWNERS"}).String(); got != "file:.github/CODEOWNERS" {
		t.Errorf("Resource.String() = %q", got)
	}
}

func TestChangeSetIsRecommendation(t *testing.T) {
	t.Parallel()

	notes := ChangeSet{Manual: []RuleID{"x"}, Notes: []string{"do it by hand"}}
	if !notes.IsRecommendation() {
		t.Error("a notes-only change set is a recommendation")
	}

	writes := ChangeSet{Files: []FileChange{{Path: "a"}}}
	if writes.IsRecommendation() {
		t.Error("a change set with files is not a recommendation")
	}
}

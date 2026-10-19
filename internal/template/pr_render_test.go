package template

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func prVars() *PRVars {
	return &PRVars{
		Common:  Common{Owner: "acme", Repo: "svc", DefaultBranch: "main"},
		Control: ControlRef{ID: "codeowners@1", Slug: "codeowners", Title: "Code owners"},
		Fixed:   []RuleRef{{ID: "present", Title: "CODEOWNERS exists"}},
		Manual:  []RuleRef{{ID: "teams"}},
		Notes:   []string{"codeowners/teams: add the owning team by hand"},
	}
}

// TestRenderPR_Defaults is IMPL-0030 4.13 / OQ10: the built-in title and
// body name the control, what it fixes, what needs a human and the notes.
func TestRenderPR_Defaults(t *testing.T) {
	t.Parallel()

	got, err := RenderPR(nil, prVars())
	if err != nil {
		t.Fatal(err)
	}

	if got.Title != "chore: Code owners" {
		t.Errorf("title = %q", got.Title)
	}

	for _, want := range []string{"`codeowners@1`", "## What this fixes", "`present`: CODEOWNERS exists", "## What needs a human", "`teams`", "add the owning team by hand"} {
		if !strings.Contains(got.Body, want) {
			t.Errorf("body lacks %q:\n%s", want, got.Body)
		}
	}

	if strings.Contains(got.Body, "controls:") {
		t.Errorf("body links a controls page with no URL:\n%s", got.Body)
	}
}

// TestRenderPR_OverridesFieldByField: a pr {} title replaces the default
// title only; its labels are carried.
func TestRenderPR_OverridesFieldByField(t *testing.T) {
	t.Parallel()

	title, err := NewRenderer().Parse("pr", "fix({{ .Control.Slug }}): {{ .Repo }}")
	if err != nil {
		t.Fatal(err)
	}

	got, err := RenderPR(&PRConfig{CompiledTitle: title, Labels: []string{"compliance"}}, prVars())
	if err != nil {
		t.Fatal(err)
	}

	if got.Title != "fix(codeowners): svc" || !strings.Contains(got.Body, "## What this fixes") || len(got.Labels) != 1 {
		t.Errorf("RenderPR = %+v, want the custom title over the default body", got)
	}
}

// TestRenderPR_TruncatesLongBodies: a body past MaxPRBodyChars is cut to
// exactly the limit and ends in the marker, never mid-rune.
func TestRenderPR_TruncatesLongBodies(t *testing.T) {
	t.Parallel()

	v := prVars()
	v.Notes = []string{strings.Repeat("é", MaxPRBodyChars)}

	got, err := RenderPR(nil, v)
	if err != nil {
		t.Fatal(err)
	}

	if n := utf8.RuneCountInString(got.Body); n != MaxPRBodyChars || !strings.HasSuffix(got.Body, TruncatedMarker) || !utf8.ValidString(got.Body) {
		t.Errorf("truncated body: %d runes, marker %v, valid %v", n, strings.HasSuffix(got.Body, TruncatedMarker), utf8.ValidString(got.Body))
	}
}

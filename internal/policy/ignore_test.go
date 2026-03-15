package policy

import "testing"

func TestIgnoreConfig_Matches_ExactMatch(t *testing.T) {
	t.Parallel()

	ic := &IgnoreConfig{Repos: []string{"myorg/my-repo"}}

	if !ic.Matches("myorg/my-repo") {
		t.Error("expected exact match")
	}
}

func TestIgnoreConfig_Matches_GlobWildcard(t *testing.T) {
	t.Parallel()

	ic := &IgnoreConfig{Repos: []string{"myorg/terraform-*"}}

	if !ic.Matches("myorg/terraform-vpc") {
		t.Error("expected glob wildcard to match")
	}
}

func TestIgnoreConfig_Matches_NoMatch(t *testing.T) {
	t.Parallel()

	ic := &IgnoreConfig{Repos: []string{"myorg/other-*"}}

	if ic.Matches("myorg/my-repo") {
		t.Error("expected no match")
	}
}

func TestIgnoreConfig_Matches_CharacterSet(t *testing.T) {
	t.Parallel()

	ic := &IgnoreConfig{Repos: []string{"myorg/repo-[abc]"}}

	if !ic.Matches("myorg/repo-a") {
		t.Error("expected character set to match 'a'")
	}

	if !ic.Matches("myorg/repo-b") {
		t.Error("expected character set to match 'b'")
	}

	if ic.Matches("myorg/repo-d") {
		t.Error("expected character set NOT to match 'd'")
	}
}

func TestIgnoreConfig_Matches_CaseInsensitive(t *testing.T) {
	t.Parallel()

	ic := &IgnoreConfig{Repos: []string{"MyOrg/My-Repo"}}

	if !ic.Matches("myorg/my-repo") {
		t.Error("expected case-insensitive match (lowercase input)")
	}

	if !ic.Matches("MYORG/MY-REPO") {
		t.Error("expected case-insensitive match (uppercase input)")
	}
}

func TestIgnoreConfig_Matches_EmptyRepos(t *testing.T) {
	t.Parallel()

	ic := &IgnoreConfig{}

	if ic.Matches("myorg/my-repo") {
		t.Error("expected no match with empty repos list")
	}
}

func TestIgnoreConfig_Matches_NilConfig(t *testing.T) {
	t.Parallel()

	var ic *IgnoreConfig

	if ic.Matches("myorg/my-repo") {
		t.Error("expected no match with nil config")
	}
}

func TestIgnoreConfig_Matches_MultiplePatterns(t *testing.T) {
	t.Parallel()

	ic := &IgnoreConfig{Repos: []string{
		"myorg/legacy-*",
		"myorg/special-case",
		"myorg/terraform-*",
	}}

	if !ic.Matches("myorg/legacy-monolith") {
		t.Error("expected first pattern to match")
	}

	if !ic.Matches("myorg/special-case") {
		t.Error("expected second pattern to match")
	}

	if !ic.Matches("myorg/terraform-vpc") {
		t.Error("expected third pattern to match")
	}

	if ic.Matches("myorg/normal-repo") {
		t.Error("expected no match for unmatched repo")
	}
}

func TestIgnoreConfig_Matches_InvalidPattern(t *testing.T) {
	t.Parallel()

	ic := &IgnoreConfig{Repos: []string{"myorg/[invalid"}}

	// Invalid patterns should be skipped, not cause a panic.
	if ic.Matches("myorg/anything") {
		t.Error("expected invalid pattern to be skipped")
	}
}

func TestIgnoreConfig_Matches_QuestionMark(t *testing.T) {
	t.Parallel()

	ic := &IgnoreConfig{Repos: []string{"myorg/repo-?"}}

	if !ic.Matches("myorg/repo-a") {
		t.Error("expected ? to match single character")
	}

	if ic.Matches("myorg/repo-ab") {
		t.Error("expected ? NOT to match multiple characters")
	}
}

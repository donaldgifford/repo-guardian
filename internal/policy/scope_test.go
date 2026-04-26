package policy

import "testing"

func TestScopeConfig_Matches_ExactMatch(t *testing.T) {
	t.Parallel()

	sc := &ScopeConfig{Orgs: []string{"myorg"}}

	if !sc.Matches("myorg") {
		t.Error("expected exact match")
	}
}

func TestScopeConfig_Matches_GlobWildcard(t *testing.T) {
	t.Parallel()

	sc := &ScopeConfig{Orgs: []string{"myorg-*"}}

	if !sc.Matches("myorg-prod") {
		t.Error("expected glob wildcard to match")
	}
}

func TestScopeConfig_Matches_NoMatch(t *testing.T) {
	t.Parallel()

	sc := &ScopeConfig{Orgs: []string{"myorg-*"}}

	if sc.Matches("otherorg") {
		t.Error("expected no match")
	}
}

func TestScopeConfig_Matches_CharacterSet(t *testing.T) {
	t.Parallel()

	sc := &ScopeConfig{Orgs: []string{"myorg-[abc]"}}

	if !sc.Matches("myorg-a") {
		t.Error("expected character set to match 'a'")
	}

	if !sc.Matches("myorg-b") {
		t.Error("expected character set to match 'b'")
	}

	if sc.Matches("myorg-d") {
		t.Error("expected character set NOT to match 'd'")
	}
}

func TestScopeConfig_Matches_CaseInsensitive(t *testing.T) {
	t.Parallel()

	sc := &ScopeConfig{Orgs: []string{"MyOrg"}}

	if !sc.Matches("myorg") {
		t.Error("expected case-insensitive match (lowercase input)")
	}

	if !sc.Matches("MYORG") {
		t.Error("expected case-insensitive match (uppercase input)")
	}
}

func TestScopeConfig_Matches_EmptyOrgs(t *testing.T) {
	t.Parallel()

	sc := &ScopeConfig{}

	if sc.Matches("myorg") {
		t.Error("expected no match with empty orgs list")
	}
}

func TestScopeConfig_Matches_NilConfig(t *testing.T) {
	t.Parallel()

	var sc *ScopeConfig

	if sc.Matches("myorg") {
		t.Error("expected no match with nil config")
	}
}

func TestScopeConfig_Matches_MultiplePatterns(t *testing.T) {
	t.Parallel()

	sc := &ScopeConfig{Orgs: []string{
		"myorg-prod",
		"myorg-staging",
		"partner-*",
	}}

	if !sc.Matches("myorg-prod") {
		t.Error("expected first pattern to match")
	}

	if !sc.Matches("myorg-staging") {
		t.Error("expected second pattern to match")
	}

	if !sc.Matches("partner-acme") {
		t.Error("expected third pattern to match")
	}

	if sc.Matches("unrelated") {
		t.Error("expected no match for unmatched org")
	}
}

func TestScopeConfig_Matches_InvalidPattern(t *testing.T) {
	t.Parallel()

	sc := &ScopeConfig{Orgs: []string{"[invalid"}}

	// Invalid patterns should be skipped, not cause a panic.
	if sc.Matches("anything") {
		t.Error("expected invalid pattern to be skipped")
	}
}

func TestScopeConfig_Matches_QuestionMark(t *testing.T) {
	t.Parallel()

	sc := &ScopeConfig{Orgs: []string{"myorg-?"}}

	if !sc.Matches("myorg-a") {
		t.Error("expected ? to match single character")
	}

	if sc.Matches("myorg-ab") {
		t.Error("expected ? NOT to match multiple characters")
	}
}

func TestScopeConfig_Matches_UniversalLiteralMatchesAnyOrg(t *testing.T) {
	t.Parallel()

	// path.Match("*", anything) returns true for any single-segment string.
	// At runtime the rule-level gate uses HasUniversal() to short-circuit
	// before calling Matches(), but the matcher itself must still treat "*"
	// as a glob that matches any non-empty single segment.
	sc := &ScopeConfig{Orgs: []string{"*"}}

	if !sc.Matches("anyorg") {
		t.Error("expected '*' glob to match any single-segment org")
	}
}

func TestScopeConfig_HasUniversal_True(t *testing.T) {
	t.Parallel()

	sc := &ScopeConfig{Orgs: []string{"myorg-prod", "*"}}

	if !sc.HasUniversal() {
		t.Error("expected HasUniversal to be true when '*' is present")
	}
}

func TestScopeConfig_HasUniversal_False(t *testing.T) {
	t.Parallel()

	sc := &ScopeConfig{Orgs: []string{"myorg-prod", "myorg-staging"}}

	if sc.HasUniversal() {
		t.Error("expected HasUniversal to be false when '*' is absent")
	}
}

func TestScopeConfig_HasUniversal_NilReceiver(t *testing.T) {
	t.Parallel()

	var sc *ScopeConfig

	if sc.HasUniversal() {
		t.Error("expected HasUniversal to be false on nil receiver")
	}
}

func TestScopeConfig_HasUniversal_EmptyOrgs(t *testing.T) {
	t.Parallel()

	sc := &ScopeConfig{}

	if sc.HasUniversal() {
		t.Error("expected HasUniversal to be false on empty Orgs")
	}
}

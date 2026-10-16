package policy

import (
	"path"
	"slices"
	"strings"
)

// universalOrg is the literal pattern that, when present in a rule-level
// ScopeConfig.Orgs, signals "this rule applies to every org declared in
// the top-level scope." See DESIGN-0010.
const universalOrg = "*"

// Matches reports whether owner matches any pattern in Orgs. Patterns use
// path.Match for glob matching (*, ?, [abc]). Input is lowercased since
// GitHub org names are case-insensitive. Returns false for nil or empty
// Orgs.
//
// Note the inverted polarity from IgnoreConfig.Matches: this returns true
// when the rule applies, whereas IgnoreConfig.Matches returns true to skip.
func (sc *ScopeConfig) Matches(owner string) bool {
	if sc == nil || len(sc.Orgs) == 0 {
		return false
	}

	normalized := strings.ToLower(owner)

	for _, pattern := range sc.Orgs {
		matched, err := path.Match(strings.ToLower(pattern), normalized)
		if err != nil {
			// Invalid pattern — skip it silently.
			continue
		}

		if matched {
			return true
		}
	}

	return false
}

// HasUniversal reports whether Orgs contains the literal "*". Used at the
// rule level as the explicit "applies to all top-level scope orgs" idiom.
func (sc *ScopeConfig) HasUniversal() bool {
	if sc == nil {
		return false
	}

	return slices.Contains(sc.Orgs, universalOrg)
}

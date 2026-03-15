package policy

import (
	"path"
	"strings"
)

// Matches returns true if the given "owner/repo" matches any pattern in the
// ignore list. Patterns use path.Match for glob matching (*, ?, [abc]).
// Input is normalized to lowercase since GitHub repo names are case-insensitive.
func (ic *IgnoreConfig) Matches(ownerRepo string) bool {
	if ic == nil || len(ic.Repos) == 0 {
		return false
	}

	normalized := strings.ToLower(ownerRepo)

	for _, pattern := range ic.Repos {
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

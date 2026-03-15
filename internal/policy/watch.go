package policy

// ExtractWatchedPaths returns the set of file paths that should trigger
// a re-check on push events. A path is watched when its file rule has
// at least one reconciler with watch = true.
func ExtractWatchedPaths(config *PolicyConfig) map[string]bool {
	watched := map[string]bool{}

	for i := range config.FileRules {
		if !hasWatchedReconciler(&config.FileRules[i]) {
			continue
		}

		for _, path := range config.FileRules[i].Paths {
			watched[path] = true
		}
	}

	return watched
}

func hasWatchedReconciler(rule *FileRuleConfig) bool {
	for _, rec := range rule.Reconcilers {
		if rec.Watch {
			return true
		}
	}

	return false
}

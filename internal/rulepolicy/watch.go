package policy

// ExtractWatchedPaths returns the set of file paths that should trigger
// a re-check on push events. A path is watched when it comes from any of
// three sources (IMPL-0019 / DESIGN-0020 Decision 4):
//
//  1. a file rule with at least one reconciler declaring watch = true;
//  2. the paths of a when-gate's referee — merging the referee's add PR
//     flips the gate, so the gated rule must re-check on that push;
//  3. a gated rule's own paths — re-adding a removed forbidden file must
//     re-open the removal PR on the push path rather than waiting for the
//     next sweep.
func ExtractWatchedPaths(config *PolicyConfig) map[string]bool {
	watched := map[string]bool{}

	ruleByName := make(map[string]*FileRuleConfig, len(config.FileRules))
	for i := range config.FileRules {
		ruleByName[config.FileRules[i].Name] = &config.FileRules[i]
	}

	for i := range config.FileRules {
		rule := &config.FileRules[i]

		if hasWatchedReconciler(rule) {
			addPaths(watched, rule.Paths)
		}

		if rule.When == nil {
			continue
		}

		// Source 3: the gated rule's own paths.
		addPaths(watched, rule.Paths)

		// Source 2: the referee's paths (skip silently if the reference
		// can't be resolved — load validation already guarantees it).
		if referee, ok := ruleByName[rule.When.RuleSatisfied]; ok {
			addPaths(watched, referee.Paths)
		}
	}

	return watched
}

func addPaths(watched map[string]bool, paths []string) {
	for _, path := range paths {
		watched[path] = true
	}
}

func hasWatchedReconciler(rule *FileRuleConfig) bool {
	for _, rec := range rule.Reconcilers {
		if rec.Watch {
			return true
		}
	}

	return false
}

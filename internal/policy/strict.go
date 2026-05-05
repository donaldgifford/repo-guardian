package policy

import (
	"errors"
	"fmt"
	"strings"

	tmpl "github.com/donaldgifford/repo-guardian/internal/template"
)

// ValidatePRTemplates runs ValidateZero[tmpl.PRVars] on every compiled
// PR template referenced by cfg (defaults.pr, every rule.pr, every
// reconcile.pr). Each failing template is collected and reported in a
// single error so operators see all problems at once instead of having
// to fix them one at a time.
//
// PR templates must render cleanly against an empty PRVars{} because
// the engine's bundle path may produce contexts where individual
// fields (Rule, Rules, Files, Reconciler) are zero-valued, and
// strict mode catches templates that would silently dereference nil
// pointer fields under missingkey=error.
//
// CompiledTitle and CompiledBody must already be populated by
// compilePolicyTemplates; ValidatePRTemplates is intended to run
// after Load() succeeds.
func ValidatePRTemplates(cfg *PolicyConfig) error {
	var failures []string

	if cfg.Defaults != nil {
		failures = append(failures, validatePRBlock("defaults", cfg.Defaults.PR)...)
	}

	for i := range cfg.FileRules {
		rule := &cfg.FileRules[i]
		failures = append(failures, validatePRBlock(fmt.Sprintf("rule %q", rule.Name), rule.PR)...)

		for j := range rule.Reconcilers {
			rec := &rule.Reconcilers[j]
			loc := fmt.Sprintf("rule %q.reconcile %q", rule.Name, rec.Type)
			failures = append(failures, validatePRBlock(loc, rec.PR)...)
		}
	}

	if len(failures) == 0 {
		return nil
	}

	return errors.New("strict template validation failed:\n  " + strings.Join(failures, "\n  "))
}

// validatePRBlock returns a slice of error strings — one per
// failing template field — found inside pr. Callers prefix the
// location with their scope so failures are addressable. An
// empty slice means the block validated cleanly.
func validatePRBlock(location string, pr *PRConfig) []string {
	if pr == nil {
		return nil
	}

	var failures []string

	if pr.CompiledTitle != nil {
		if err := tmpl.ValidateZero[tmpl.PRVars](pr.CompiledTitle); err != nil {
			failures = append(failures, fmt.Sprintf("%s.pr.title: %v", location, err))
		}
	}

	if pr.CompiledBody != nil {
		if err := tmpl.ValidateZero[tmpl.PRVars](pr.CompiledBody); err != nil {
			failures = append(failures, fmt.Sprintf("%s.pr.body: %v", location, err))
		}
	}

	return failures
}

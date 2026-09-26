package policy

import (
	"fmt"
	"strings"

	"github.com/donaldgifford/repo-guardian/internal/findings"
	"github.com/donaldgifford/repo-guardian/internal/store"
)

// Summarize describes the enforced policy for policy_versions.summary:
// one entry per enabled rule with its kind, name, a one-line
// description, check mode, scope and ignore patterns. It never carries
// template bodies, PR text or assertion patterns; the summary is shown
// through the API.
//
// A rule's scope is its own scope block, else the top-level scope; nil
// means every org.
func Summarize(cfg *PolicyConfig) store.PolicySummary {
	sum := store.PolicySummary{Rules: []store.PolicyRule{}}
	if cfg == nil {
		return sum
	}

	scope := func(s *ScopeConfig) []string {
		switch {
		case s != nil:
			return s.Orgs
		case cfg.Scope != nil:
			return cfg.Scope.Orgs
		default:
			return nil
		}
	}

	for i := range cfg.FileRules {
		r := &cfg.FileRules[i]
		if !r.IsEnabled() {
			continue
		}

		mode := r.Check
		if mode == "" {
			mode = string(CheckExists)
		}

		sum.Rules = append(sum.Rules, store.PolicyRule{
			Kind:        findings.RuleKindFile,
			Name:        r.Name,
			Description: fileDescription(CheckMode(mode), r.Paths),
			Scope:       scope(r.Scope),
			Ignore:      ignoreOf(r.Ignore),
			CheckMode:   mode,
		})
	}

	for i := range cfg.SettingRules {
		r := &cfg.SettingRules[i]
		if !r.IsEnabled() {
			continue
		}

		sum.Rules = append(sum.Rules, store.PolicyRule{
			Kind:        findings.RuleKindSetting,
			Name:        r.Name,
			Description: fmt.Sprintf("%s is %v", r.Property, r.Expected),
			Scope:       scope(r.Scope),
			Ignore:      ignoreOf(r.Ignore),
		})
	}

	for i := range cfg.BranchProtectionRules {
		r := &cfg.BranchProtectionRules[i]
		if !r.IsEnabled() {
			continue
		}

		sum.Rules = append(sum.Rules, store.PolicyRule{
			Kind:        findings.RuleKindBranchProtection,
			Name:        r.Name,
			Description: "ruleset protects " + r.Branch,
			Scope:       scope(r.Scope),
			Ignore:      ignoreOf(r.Ignore),
		})
	}

	return sum
}

func fileDescription(mode CheckMode, paths []string) string {
	list := strings.Join(paths, ", ")

	switch mode {
	case CheckAbsent:
		return "none of " + list + " exists"
	case CheckContains:
		return "one of " + list + " exists and matches its assertions"
	case CheckExact:
		return "one of " + list + " exists with the expected content"
	default:
		return "one of " + list + " exists"
	}
}

package policy

import (
	"fmt"

	tmpl "github.com/donaldgifford/repo-guardian/internal/template"
)

// policyRenderer is the package-level *template.Renderer used to compile
// every PR Title/Body string declared in HCL into a *template.Compiled.
// One instance per process is sufficient because Renderer is documented
// as concurrency-safe and immutable after construction.
var policyRenderer = tmpl.NewRenderer()

// compilePolicyTemplates walks every PRConfig in cfg (defaults, file
// rules, and reconcilers) and parses Title/Body strings into
// *template.Compiled fields. Parse errors are returned with a
// location-prefixed message (e.g. "defaults.pr.title: ...",
// "rule \"codeowners\".pr.body: ...",
// "rule \"catalog_info\".reconcile \"custom_properties\".pr.title: ...")
// so operators can locate the broken HCL block.
//
// The resulting compiled forms are stored on PRConfig.CompiledTitle and
// PRConfig.CompiledBody. Engine hot-path code retrieves them through the
// resolution helpers in this file (ResolveRulePR / ResolveReconcilerPR)
// so render-time callers never need to re-parse.
func compilePolicyTemplates(cfg *PolicyConfig) error {
	if cfg.Defaults != nil && cfg.Defaults.PR != nil {
		if err := compilePR(cfg.Defaults.PR, "defaults"); err != nil {
			return err
		}
	}

	for i := range cfg.FileRules {
		rule := &cfg.FileRules[i]

		if rule.PR != nil {
			loc := fmt.Sprintf("rule %q", rule.Name)
			if err := compilePR(rule.PR, loc); err != nil {
				return err
			}
		}

		for j := range rule.Reconcilers {
			rec := &rule.Reconcilers[j]
			if rec.PR == nil {
				continue
			}

			loc := fmt.Sprintf("rule %q.reconcile %q", rule.Name, rec.Type)
			if err := compilePR(rec.PR, loc); err != nil {
				return err
			}
		}
	}

	return nil
}

// compilePR parses pr.Title and pr.Body strings (when set) into
// *template.Compiled values stored back on pr. The location string is
// prefixed onto any parse error.
func compilePR(pr *PRConfig, location string) error {
	if pr.Title != nil {
		c, err := policyRenderer.Parse(location+".pr.title", *pr.Title)
		if err != nil {
			return fmt.Errorf("%s.pr.title: %w", location, err)
		}

		pr.CompiledTitle = c
	}

	if pr.Body != nil {
		c, err := policyRenderer.Parse(location+".pr.body", *pr.Body)
		if err != nil {
			return fmt.Errorf("%s.pr.body: %w", location, err)
		}

		pr.CompiledBody = c
	}

	return nil
}

// asTemplate converts a PRConfig (HCL-decoded form) into a PRTemplate
// (resolved form) by lifting the compiled templates and resolving the
// nullable Inherits flag. Returns nil when pr is nil so callers can
// pass through "this scope declared no pr block" as a nil PRTemplate.
//
// Inherits semantics: nil → true (default), explicit true → true,
// explicit false → false. The default-true matches the rule that
// silence at a scope means "let the parent scope's value flow
// through"; an operator who wants to STOP propagation must set
// `inherits = false` explicitly.
func asTemplate(pr *PRConfig) *PRTemplate {
	if pr == nil {
		return nil
	}

	inherits := true
	if pr.Inherits != nil {
		inherits = *pr.Inherits
	}

	return &PRTemplate{
		Title:     pr.CompiledTitle,
		Body:      pr.CompiledBody,
		Labels:    pr.Labels,
		LabelsSet: pr.LabelsSet,
		Inherits:  inherits,
	}
}

// ResolveRulePR returns the merged PRTemplate for a rule's own PR. The
// resolution chain runs from most specific to least specific: rule.pr
// → defaults.pr. Each of Title, Body, and Labels independently
// inherits from the parent when the child does not set the field and
// the child's Inherits flag is true.
//
// Inherits=false at the rule scope short-circuits the chain: only
// fields the rule itself set are honored; unset fields fall through
// directly to engine built-ins.
func ResolveRulePR(rule, defaults *PRTemplate) *PRTemplate {
	return mergePR(rule, defaults)
}

// ResolveReconcilerPR returns the merged PRTemplate for a reconciler's
// own PR. Reconciler PRs deliberately skip the rule.pr block (Open Q4
// resolution): the rule's PR text describes the rule's file change,
// not the reconciler's side-channel PR. The chain therefore runs
// reconciler.pr → defaults.pr.
func ResolveReconcilerPR(reconciler, defaults *PRTemplate) *PRTemplate {
	return mergePR(reconciler, defaults)
}

// mergePR is the field-by-field inherit merge used by both Resolve
// entry points. The child scope wins on every field it explicitly
// set; unset fields inherit from the parent only when the child's
// Inherits flag is true. When the child is nil, the parent is
// returned verbatim. When the parent is nil, the child is returned
// verbatim.
//
// LabelsSet semantics: Labels inherit from the parent only when the
// child did NOT set the labels attribute (LabelsSet=false). An
// explicit `labels = []` at the child (LabelsSet=true, Labels=[])
// is an empty-list override and blocks parent inheritance.
func mergePR(child, parent *PRTemplate) *PRTemplate {
	switch {
	case child == nil && parent == nil:
		return nil
	case child == nil:
		return parent
	case parent == nil:
		return child
	}

	if !child.Inherits {
		return child
	}

	merged := &PRTemplate{
		Title:     child.Title,
		Body:      child.Body,
		Labels:    child.Labels,
		LabelsSet: child.LabelsSet,
		Inherits:  child.Inherits,
	}

	if merged.Title == nil {
		merged.Title = parent.Title
	}

	if merged.Body == nil {
		merged.Body = parent.Body
	}

	if !merged.LabelsSet {
		merged.Labels = parent.Labels
		merged.LabelsSet = parent.LabelsSet
	}

	return merged
}

// DefaultsPR returns the resolved PRTemplate for the top-level
// `defaults { pr { } }` block, or nil if the policy declares no
// defaults. Engine code that needs only the defaults (e.g. for the
// fallback path on bundled-PR title conflicts) should call this.
func (c *PolicyConfig) DefaultsPR() *PRTemplate {
	if c.Defaults == nil {
		return nil
	}

	return asTemplate(c.Defaults.PR)
}

// RulePR returns the resolved PRTemplate for the named file rule's
// own PR, with defaults inheritance applied. Returns nil when the
// rule is unknown or declares no pr {} block AND no defaults are set.
func (c *PolicyConfig) RulePR(ruleName string) *PRTemplate {
	defaults := c.DefaultsPR()

	for i := range c.FileRules {
		if c.FileRules[i].Name != ruleName {
			continue
		}

		return ResolveRulePR(asTemplate(c.FileRules[i].PR), defaults)
	}

	return defaults
}

// ReconcilerPR returns the resolved PRTemplate for the reconciler
// of the given type attached to the named file rule, with defaults
// inheritance applied (rule.pr is deliberately skipped per the
// ResolveReconcilerPR contract). Returns nil when the rule or
// reconciler is unknown AND no defaults are set.
func (c *PolicyConfig) ReconcilerPR(ruleName, reconcilerType string) *PRTemplate {
	defaults := c.DefaultsPR()

	for i := range c.FileRules {
		if c.FileRules[i].Name != ruleName {
			continue
		}

		for j := range c.FileRules[i].Reconcilers {
			rec := &c.FileRules[i].Reconcilers[j]
			if rec.Type != reconcilerType {
				continue
			}

			return ResolveReconcilerPR(asTemplate(rec.PR), defaults)
		}
	}

	return defaults
}

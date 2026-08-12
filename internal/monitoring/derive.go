package monitoring

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/donaldgifford/repo-guardian/internal/policy"
)

// envInfluencers are the environment variables that can change the
// derived model behind the config file's back.
//
// policy.BuiltinDefaults reads CUSTOM_PROPERTIES_MODE and appends a
// catalog_info rule with a custom_properties reconciler; applyEnvOverrides
// runs last over the rest. Every one of them is either a mechanism or an
// alert-window input, so a stray variable in somebody's shell produces a
// generated artifact that differs from CI's for reasons the diff does not
// show. Recording them is the cheapest way to make that self-identifying.
var envInfluencers = []string{
	"AUTO_CLOSE_PR",
	"CUSTOM_PROPERTIES_MODE",
	"DRY_RUN",
	"ORPHAN_CLEANUP",
	"SCHEDULE_INTERVAL",
	"SKIP_ARCHIVED",
	"SKIP_FORKS",
}

// globMeta are the path.Match metacharacters. An org name containing
// one cannot be rendered as a fixed dashboard row.
const globMeta = `*?[`

// Options are the non-policy inputs to Derive.
type Options struct {
	// ConfigPath is recorded as provenance. Derive does not read it —
	// the caller has already loaded the policy.
	ConfigPath string

	// ExtraOrgs are orgs named on the command line.
	//
	// The escape hatch for legacy-mode configs, which carry no org list
	// anywhere: without it a legacy operator cannot get the silent-org
	// signal at all, and forcing them into strict mode to obtain it
	// would mean adding a scope block to every rule.
	ExtraOrgs []string
}

// Derive projects a loaded policy onto the generation model.
//
// Everything derived from a map or from multiple slices is sorted here,
// at construction, so no emit path can introduce non-determinism into
// artifacts the CI drift gate compares byte for byte.
func Derive(cfg *policy.PolicyConfig, opts Options) (*Model, error) {
	if cfg == nil {
		return nil, fmt.Errorf("monitoring: nil policy config")
	}

	strict := policy.IsStrictScope(cfg)

	m := &Model{
		Strict:        strict,
		Orgs:          deriveOrgs(cfg, opts.ExtraOrgs),
		Mechanisms:    make(Mechanisms),
		SweepInterval: cfg.Guardian.ParsedScheduleInterval,
		Source: Source{
			ConfigPath:   opts.ConfigPath,
			EnvInfluence: presentEnvInfluencers(),
		},
	}

	m.Rules = deriveRules(cfg, m.OrgNames(), strict)

	if err := rejectDuplicateNames(m.Rules); err != nil {
		return nil, err
	}

	deriveMechanisms(cfg, m)

	return m, nil
}

// deriveOrgs merges the top-level scope orgs with any named on the
// command line.
func deriveOrgs(cfg *policy.PolicyConfig, extra []string) []Org {
	names := make(map[string]struct{})

	if cfg.Scope != nil {
		for _, o := range cfg.Scope.Orgs {
			names[o] = struct{}{}
		}
	}

	for _, o := range extra {
		if o != "" {
			names[o] = struct{}{}
		}
	}

	out := make([]Org, 0, len(names))
	for name := range names {
		out = append(out, Org{Name: name, Pattern: strings.ContainsAny(name, globMeta)})
	}

	slices.SortFunc(out, func(a, b Org) int { return strings.Compare(a.Name, b.Name) })

	return out
}

// deriveRules flattens the three policy rule slices into one sorted,
// kind-tagged list, resolving each rule's org set through both scope
// gates.
func deriveRules(cfg *policy.PolicyConfig, orgs []string, strict bool) []Rule {
	out := make([]Rule, 0, len(cfg.FileRules)+len(cfg.SettingRules)+len(cfg.BranchProtectionRules))

	for i := range cfg.FileRules {
		r := &cfg.FileRules[i]
		if !r.IsEnabled() {
			continue
		}

		out = append(out, Rule{
			Name:        r.Name,
			Kind:        RuleKindFile,
			CheckMode:   string(r.CheckMode()),
			Gated:       r.When != nil,
			Reconcilers: reconcilerNames(r.Reconcilers),
			Orgs:        rulesOrgs(r.Scope, orgs, strict),
		})
	}

	for i := range cfg.SettingRules {
		r := &cfg.SettingRules[i]
		if !r.IsEnabled() {
			continue
		}

		out = append(out, Rule{
			Name:       r.Name,
			Kind:       RuleKindSetting,
			Remediates: r.Remediate,
			Orgs:       rulesOrgs(r.Scope, orgs, strict),
		})
	}

	for i := range cfg.BranchProtectionRules {
		r := &cfg.BranchProtectionRules[i]
		if !r.IsEnabled() {
			continue
		}

		out = append(out, Rule{
			Name:       r.Name,
			Kind:       RuleKindBranchProtection,
			Remediates: r.Remediate,
			Orgs:       rulesOrgs(r.Scope, orgs, strict),
		})
	}

	slices.SortFunc(out, func(a, b Rule) int {
		if c := strings.Compare(string(a.Kind), string(b.Kind)); c != 0 {
			return c
		}

		return strings.Compare(a.Name, b.Name)
	})

	return out
}

// rulesOrgs resolves which of the configured orgs a rule applies to.
//
// Returns nil in legacy mode — "every org", including ones nobody has
// named — which is a different statement from the empty slice a
// strict-mode rule scoped to nothing produces. Collapsing the two would
// turn a misconfigured rule into one that appears to apply everywhere.
//
// The gate itself is policy.RuleScopeAllowsOrg, the same predicate the
// engine calls. A local reimplementation would not fail loudly; it
// would render a plausible, wrong dashboard row.
func rulesOrgs(scope *policy.ScopeConfig, orgs []string, strict bool) []string {
	if !strict {
		return nil
	}

	out := make([]string, 0, len(orgs))

	for _, org := range orgs {
		if policy.RuleScopeAllowsOrg(scope, org, strict) {
			out = append(out, org)
		}
	}

	return out
}

// reconcilerNames returns the attached reconciler type names, sorted
// and deduplicated.
func reconcilerNames(rs []policy.ReconcilerConfig) []string {
	if len(rs) == 0 {
		return nil
	}

	out := make([]string, 0, len(rs))
	for i := range rs {
		out = append(out, rs[i].Type)
	}

	slices.Sort(out)

	return slices.Compact(out)
}

// rejectDuplicateNames refuses a config whose rule names collide across
// kinds.
//
// Uniqueness is validated WITHIN each kind but not across them, so a
// `rule "file" "x"` and a `rule "setting" "x"` are both legal and both
// emit rule_name="x". Every posture series is keyed on rule_name with
// no kind label, so a kind-scoped panel would silently merge the two
// and chart a number that is the sum of two unrelated things. Refusing
// at generation time is the honest response: the generator cannot emit
// a correct artifact for this config, and emitting a wrong one quietly
// is how a compliance dashboard starts lying.
func rejectDuplicateNames(rules []Rule) error {
	seen := make(map[string]RuleKind, len(rules))

	for i := range rules {
		if kind, dup := seen[rules[i].Name]; dup {
			return fmt.Errorf(
				"monitoring: rule name %q is used by both a %s rule and a %s rule; "+
					"every posture series is keyed on rule_name with no kind label, "+
					"so the two would merge into one misleading number — rename one",
				rules[i].Name, kind, rules[i].Kind)
		}

		seen[rules[i].Name] = rules[i].Kind
	}

	return nil
}

// deriveMechanisms folds the config into the set of features that
// produce series.
func deriveMechanisms(cfg *policy.PolicyConfig, m *Model) {
	for i := range cfg.FileRules {
		r := &cfg.FileRules[i]
		if !r.IsEnabled() {
			continue
		}

		m.Mechanisms.add(MechanismFileRules)

		if r.CheckMode() == policy.CheckAbsent {
			m.Mechanisms.add(MechanismAbsentRules)
		}

		if r.When != nil {
			m.Mechanisms.add(MechanismWhenGates)
		}

		addReconcilerMechanisms(r.Reconcilers, m)
		addIgnoreMechanism(r.Ignore, m)
	}

	for i := range cfg.SettingRules {
		r := &cfg.SettingRules[i]
		if !r.IsEnabled() {
			continue
		}

		m.Mechanisms.add(MechanismSettingRules)

		if r.Remediate {
			m.Mechanisms.add(MechanismSettingRemediation)
		}

		addIgnoreMechanism(r.Ignore, m)
	}

	for i := range cfg.BranchProtectionRules {
		r := &cfg.BranchProtectionRules[i]
		if !r.IsEnabled() {
			continue
		}

		m.Mechanisms.add(MechanismBranchProtectionRules)

		if r.Remediate {
			m.Mechanisms.add(MechanismBranchProtectionRemediation)
		}

		addIgnoreMechanism(r.Ignore, m)
	}

	if cfg.Scope != nil {
		m.Mechanisms.add(MechanismStrictScope)
	}

	// Value, not pointer, at the top level — the global ignore block is
	// always present as a zero struct.
	addIgnoreMechanism(&cfg.IgnoreList, m)
	addGuardianMechanisms(&cfg.Guardian, m)
}

// addReconcilerMechanisms records the reconcilers that instrument
// anything.
//
// label_sync, workflow_sync and the branch_protection reconciler are
// deliberately absent: they emit no metrics today, so they gate nothing
// and a panel for them would be empty by construction.
func addReconcilerMechanisms(rs []policy.ReconcilerConfig, m *Model) {
	for i := range rs {
		if rs[i].Type != policy.ReconcilerCustomProperties {
			continue
		}

		m.Mechanisms.add(MechanismCustomProperties)

		// The schema preflight runs in both modes, so the mode gates
		// only the PR counter, never the preflight alert.
		if rs[i].Mode == customPropertiesGHAMode {
			m.Mechanisms.add(MechanismCustomPropertiesGHA)
		}
	}
}

// customPropertiesGHAMode is the mode string that produces
// properties_prs_created_total.
const customPropertiesGHAMode = "github-action"

func addIgnoreMechanism(ic *policy.IgnoreConfig, m *Model) {
	if ic != nil && len(ic.Repos) > 0 {
		m.Mechanisms.add(MechanismIgnoreLists)
	}
}

func addGuardianMechanisms(g *policy.GuardianConfig, m *Model) {
	if g.AutoClosePREnabled() {
		m.Mechanisms.add(MechanismAutoClosePR)
	}

	if g.OrphanCleanupEnabled() {
		m.Mechanisms.add(MechanismOrphanCleanup)
	}

	if g.SkipForks || g.SkipArchived {
		m.Mechanisms.add(MechanismRepoParking)
	}

	if g.DryRun {
		m.Mechanisms.add(MechanismDryRun)
	}
}

// presentEnvInfluencers returns the influencing variables that are
// actually set, sorted.
func presentEnvInfluencers() []string {
	var out []string

	for _, name := range envInfluencers {
		if _, ok := os.LookupEnv(name); ok {
			out = append(out, name)
		}
	}

	return out
}

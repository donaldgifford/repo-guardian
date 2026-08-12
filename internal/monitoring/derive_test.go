package monitoring_test

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/checker"
	"github.com/donaldgifford/repo-guardian/internal/monitoring"
	"github.com/donaldgifford/repo-guardian/internal/policy"
)

// TestRuleKinds_MatchTheEngine pins the redeclaration.
//
// internal/monitoring cannot import internal/checker — the engine drags
// the reconcilers and their promauto registrations into a read-only CLI
// — so the three kind strings are declared twice. A test may import
// both, and this is the only thing standing between the two copies and
// a silent divergence that would render every kind-scoped panel empty.
func TestRuleKinds_MatchTheEngine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		monitoring monitoring.RuleKind
		engine     checker.RuleKind
	}{
		{monitoring.RuleKindFile, checker.RuleKindFile},
		{monitoring.RuleKindSetting, checker.RuleKindSetting},
		{monitoring.RuleKindBranchProtection, checker.RuleKindBranchProtection},
	}

	for _, tt := range tests {
		if string(tt.monitoring) != string(tt.engine) {
			t.Errorf("monitoring kind %q != engine kind %q; kind-scoped panels would chart nothing",
				tt.monitoring, tt.engine)
		}
	}
}

// writeConfig writes an HCL config to a temp file.
func writeConfig(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "guardian.hcl")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile() = %v, want nil", err)
	}

	return path
}

// derive loads an HCL body and derives the model.
func derive(t *testing.T, body string, opts monitoring.Options) *monitoring.Model {
	t.Helper()

	cfg, err := policy.Load(writeConfig(t, body))
	if err != nil {
		t.Fatalf("policy.Load() = %v, want nil", err)
	}

	m, err := monitoring.Derive(cfg, opts)
	if err != nil {
		t.Fatalf("Derive() = %v, want nil", err)
	}

	return m
}

// strictConfig is a mechanism-rich strict-mode policy: two orgs, a rule
// scoped to one of them, an absent rule, a gated rule, a remediating
// setting rule, and a custom_properties reconciler.
const strictConfig = `
scope {
  orgs = ["acme", "globex"]
}

rule "file" "codeowners" {
  paths = ["CODEOWNERS", ".github/CODEOWNERS"]
  target = ".github/CODEOWNERS"
  template = "CODEOWNERS.tmpl"
  scope { orgs = ["*"] }

  reconcile "custom_properties" {
    mode = "api"
  }
}

rule "file" "renovate" {
  paths = ["renovate.json"]
  target = "renovate.json"
  template = "renovate.json.tmpl"
  scope { orgs = ["*"] }
}

rule "file" "no-dependabot" {
  check = "absent"
  paths = [".github/dependabot.yml"]
  scope { orgs = ["*"] }

  when {
    rule_satisfied = "renovate"
  }
}

rule "file" "platform-only" {
  paths = ["PLATFORM.md"]
  target = "PLATFORM.md"
  template = "CODEOWNERS.tmpl"
  scope { orgs = ["acme"] }
}

rule "setting" "issues" {
  property = "has_issues"
  expected = true
  remediate = true
  scope { orgs = ["*"] }
}
`

// TestDerive_StrictScope covers the shape the design assumes:
// declared orgs, per-rule scope resolution, and the mechanism fold.
func TestDerive_StrictScope(t *testing.T) {
	t.Parallel()

	m := derive(t, strictConfig, monitoring.Options{ConfigPath: "guardian.hcl"})

	if !m.Strict {
		t.Error("Derive().Strict = false for a config with a top-level scope block, want true")
	}

	if got := m.OrgNames(); !reflect.DeepEqual(got, []string{"acme", "globex"}) {
		t.Errorf("Derive().OrgNames() = %v, want [acme globex]", got)
	}

	// Sorted by (kind, name): file rules first, then setting.
	wantRules := []string{"codeowners", "no-dependabot", "platform-only", "renovate", "issues"}

	got := make([]string, 0, len(m.Rules))
	for _, r := range m.Rules {
		got = append(got, r.Name)
	}

	if !reflect.DeepEqual(got, wantRules) {
		t.Errorf("Derive().Rules = %v, want %v (sorted by kind then name)", got, wantRules)
	}

	wantMechanisms := []monitoring.Mechanism{
		monitoring.MechanismAbsentRules,
		monitoring.MechanismAutoClosePR,
		monitoring.MechanismCustomProperties,
		monitoring.MechanismFileRules,
		monitoring.MechanismOrphanCleanup,
		// From the guardian DEFAULTS, not from anything in the config
		// above: skip_forks and skip_archived both default to true, so
		// the archived/fork park reasons always have a producer unless
		// an operator turns them off.
		monitoring.MechanismRepoParking,
		monitoring.MechanismSettingRemediation,
		monitoring.MechanismSettingRules,
		monitoring.MechanismStrictScope,
		monitoring.MechanismWhenGates,
	}

	if diff := mechanismDiff(m.Mechanisms.Sorted(), wantMechanisms); diff != "" {
		t.Errorf("Derive().Mechanisms mismatch: %s", diff)
	}

	// mode = "api", so the GHA-only PR counter's mechanism must NOT be
	// set. Getting this backwards would emit PropertiesPRBurst against a
	// series api mode never produces.
	if m.Mechanisms.Has(monitoring.MechanismCustomPropertiesGHA) {
		t.Error("api-mode custom_properties set the github-action mechanism")
	}
}

// TestDerive_RuleScopeResolution pins that a rule scoped to one org
// appears on that org's row only.
//
// This is the assertion that a local reimplementation of the scope gate
// would break — quietly, by rendering a plausible extra row rather than
// by failing.
func TestDerive_RuleScopeResolution(t *testing.T) {
	t.Parallel()

	m := derive(t, strictConfig, monitoring.Options{})

	acme := ruleNames(m.RulesFor("acme"))
	globex := ruleNames(m.RulesFor("globex"))

	if !slices.Contains(acme, "platform-only") {
		t.Errorf("RulesFor(acme) = %v, want it to include platform-only", acme)
	}

	if slices.Contains(globex, "platform-only") {
		t.Errorf("RulesFor(globex) = %v, want platform-only excluded; it is scoped to acme", globex)
	}

	// Universal rules land on both.
	for _, name := range []string{"codeowners", "renovate"} {
		if !slices.Contains(acme, name) || !slices.Contains(globex, name) {
			t.Errorf("universal rule %q missing: acme=%v globex=%v", name, acme, globex)
		}
	}
}

// TestDerive_LegacyModeHasNoOrgs pins the distinction between "every
// org" and "no org".
//
// A legacy config carries no org list anywhere, so an unscoped rule's
// Orgs must be nil (applies everywhere) and never an empty slice, which
// is what a strict-mode rule scoped to nothing produces. Collapsing the
// two turns a misconfigured rule into one that looks universal.
func TestDerive_LegacyModeHasNoOrgs(t *testing.T) {
	t.Parallel()

	m := derive(t, `
rule "file" "codeowners" {
  paths = ["CODEOWNERS"]
  target = ".github/CODEOWNERS"
  template = "CODEOWNERS.tmpl"
}
`, monitoring.Options{})

	if m.Strict {
		t.Error("Derive().Strict = true without a top-level scope block")
	}

	if len(m.Orgs) != 0 {
		t.Errorf("Derive().Orgs = %v, want empty in legacy mode", m.Orgs)
	}

	if m.Mechanisms.Has(monitoring.MechanismStrictScope) {
		t.Error("legacy config set the strict_scope mechanism; out_of_scope_total has no producer")
	}

	for _, r := range m.Rules {
		if !r.AppliesEverywhere() {
			t.Errorf("legacy rule %q has Orgs = %v, want nil (every org)", r.Name, r.Orgs)
		}
	}

	// Every rule applies, whatever org is asked about.
	if got := len(m.RulesFor("any-org-at-all")); got != len(m.Rules) {
		t.Errorf("RulesFor() returned %d of %d rules in legacy mode, want all", got, len(m.Rules))
	}
}

// TestDerive_ExtraOrgs covers the --org escape hatch.
//
// A legacy-mode operator cannot get the silent-org signal from the
// config, and forcing them into strict mode to obtain it would mean
// adding a scope block to every rule.
func TestDerive_ExtraOrgs(t *testing.T) {
	t.Parallel()

	m := derive(t, `
rule "file" "codeowners" {
  paths = ["CODEOWNERS"]
  target = ".github/CODEOWNERS"
  template = "CODEOWNERS.tmpl"
}
`, monitoring.Options{ExtraOrgs: []string{"globex", "acme", "acme"}})

	if got := m.OrgNames(); !reflect.DeepEqual(got, []string{"acme", "globex"}) {
		t.Errorf("OrgNames() = %v, want [acme globex] — sorted and deduplicated", got)
	}

	// --org names rows to render; it does not retroactively scope the
	// rules, which stay universal.
	if m.Strict {
		t.Error("--org flipped the model into strict mode; scope semantics must come from the policy only")
	}
}

// TestDerive_DisabledRulesAreExcluded pins that a disabled rule
// contributes neither a row nor a mechanism.
func TestDerive_DisabledRulesAreExcluded(t *testing.T) {
	t.Parallel()

	m := derive(t, `
rule "file" "codeowners" {
  paths = ["CODEOWNERS"]
  target = ".github/CODEOWNERS"
  template = "CODEOWNERS.tmpl"
}

rule "file" "no-dependabot" {
  enabled = false
  check = "absent"
  paths = [".github/dependabot.yml"]
}
`, monitoring.Options{})

	if names := ruleNames(m.Rules); slices.Contains(names, "no-dependabot") {
		t.Errorf("Derive().Rules = %v, want the disabled rule excluded", names)
	}

	if m.Mechanisms.Has(monitoring.MechanismAbsentRules) {
		t.Error("a disabled absent rule set the absent_rules mechanism; " +
			"files_forbidden_present_total has no producer, so the panel would be empty by construction")
	}
}

// TestDerive_RejectsCrossKindDuplicateNames pins the refusal.
//
// Rule-name uniqueness is validated within each kind but not across
// them, and every posture series is keyed on rule_name with no kind
// label. A kind-scoped panel would silently merge the two into one
// number that is the sum of two unrelated things — so the generator
// refuses rather than emitting a wrong artifact quietly.
func TestDerive_RejectsCrossKindDuplicateNames(t *testing.T) {
	t.Parallel()

	cfg, err := policy.Load(writeConfig(t, `
rule "file" "issues" {
  paths = ["ISSUES.md"]
  target = "ISSUES.md"
  template = "CODEOWNERS.tmpl"
}

rule "setting" "issues" {
  property = "has_issues"
  expected = true
}
`))
	if err != nil {
		t.Fatalf("policy.Load() = %v, want nil; the collision must be legal at load for this test to mean anything", err)
	}

	if _, err := monitoring.Derive(cfg, monitoring.Options{}); err == nil {
		t.Fatal("Derive() = nil error for a cross-kind duplicate rule name, want a refusal")
	}
}

// TestDerive_IsDeterministic pins that two derivations of one config
// are byte-identical.
//
// Mechanisms is a map and the org set is map-derived, so an unsorted
// range would make the task 5.5 drift gate fail on nothing at all —
// which trains everyone to regenerate without reading the diff.
func TestDerive_IsDeterministic(t *testing.T) {
	t.Parallel()

	path := writeConfig(t, strictConfig)

	const runs = 8

	models := make([]*monitoring.Model, 0, runs)

	for range runs {
		cfg, err := policy.Load(path)
		if err != nil {
			t.Fatalf("policy.Load() = %v, want nil", err)
		}

		m, err := monitoring.Derive(cfg, monitoring.Options{ConfigPath: path})
		if err != nil {
			t.Fatalf("Derive() = %v, want nil", err)
		}

		models = append(models, m)
	}

	for i := 1; i < len(models); i++ {
		if !reflect.DeepEqual(models[0], models[i]) {
			t.Fatalf("derivation %d differs from the first:\n%+v\n%+v", i, models[0], models[i])
		}
	}
}

// TestDerive_OrgPatternsAreFlagged pins that a glob org is marked.
//
// Scope orgs are matched with path.Match, so `orgs = ["acme-*"]` is
// legal and "one row per configured org" is undefined for it. Rendering
// it as a literal row would produce a panel that is permanently empty
// and looks exactly like a silent org.
func TestDerive_OrgPatternsAreFlagged(t *testing.T) {
	t.Parallel()

	m := derive(t, `
scope {
  orgs = ["acme", "myent-*"]
}

rule "file" "codeowners" {
  paths = ["CODEOWNERS"]
  target = ".github/CODEOWNERS"
  template = "CODEOWNERS.tmpl"
  scope { orgs = ["*"] }
}
`, monitoring.Options{})

	want := map[string]bool{"acme": false, "myent-*": true}

	for _, o := range m.Orgs {
		wantPattern, known := want[o.Name]
		if !known {
			t.Errorf("unexpected org %q", o.Name)

			continue
		}

		if o.Pattern != wantPattern {
			t.Errorf("Org(%q).Pattern = %t, want %t", o.Name, o.Pattern, wantPattern)
		}
	}
}

// TestDerive_EnvInfluenceIsRecorded pins the provenance that makes an
// unexplainable drift diff explain itself.
func TestDerive_EnvInfluenceIsRecorded(t *testing.T) {
	t.Setenv("DRY_RUN", "true")

	cfg, err := policy.Load("")
	if err != nil {
		t.Fatalf("policy.Load() = %v, want nil", err)
	}

	m, err := monitoring.Derive(cfg, monitoring.Options{})
	if err != nil {
		t.Fatalf("Derive() = %v, want nil", err)
	}

	if !slices.Contains(m.Source.EnvInfluence, "DRY_RUN") {
		t.Errorf("Source.EnvInfluence = %v, want it to name DRY_RUN", m.Source.EnvInfluence)
	}

	// DRY_RUN is the inverted mechanism: it suppresses prs_created_total
	// rather than producing anything. A generator that only adds alerts
	// would page a dry-run deployment forever on "no PRs created".
	if !m.Mechanisms.Has(monitoring.MechanismDryRun) {
		t.Error("DRY_RUN=true did not set the dry_run mechanism")
	}
}

// TestRuleNames covers the omit-the-panel signal.
func TestRuleNames(t *testing.T) {
	t.Parallel()

	m := derive(t, strictConfig, monitoring.Options{})

	tests := []struct {
		name    string
		kinds   []monitoring.RuleKind
		want    []string
		wantAny bool
	}{
		{
			name:    "file rules",
			kinds:   []monitoring.RuleKind{monitoring.RuleKindFile},
			want:    []string{"codeowners", "no-dependabot", "platform-only", "renovate"},
			wantAny: true,
		},
		{
			name:    "setting rules",
			kinds:   []monitoring.RuleKind{monitoring.RuleKindSetting},
			want:    []string{"issues"},
			wantAny: true,
		},
		{
			// No branch-protection rule is configured, so the caller must
			// omit the panel entirely. An empty selector would match
			// every rule and chart the wrong thing.
			name:    "branch protection rules",
			kinds:   []monitoring.RuleKind{monitoring.RuleKindBranchProtection},
			want:    []string{},
			wantAny: false,
		},
		{
			name:    "no kinds means all",
			kinds:   nil,
			want:    []string{"codeowners", "issues", "no-dependabot", "platform-only", "renovate"},
			wantAny: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, found := m.RuleNames(tt.kinds...)
			if found != tt.wantAny {
				t.Fatalf("RuleNames(%v) found = %t, want %t", tt.kinds, found, tt.wantAny)
			}

			if !found {
				return
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("RuleNames(%v) = %v, want %v (sorted by name across kinds)", tt.kinds, got, tt.want)
			}
		})
	}
}

// TestMechanisms_HasAnyWithNoArguments pins that an artifact requiring
// no mechanism is unconditional.
//
// The common case. An empty-slice check that returned false here would
// filter out every unconditional alert.
func TestMechanisms_HasAnyWithNoArguments(t *testing.T) {
	t.Parallel()

	var ms monitoring.Mechanisms = map[monitoring.Mechanism]struct{}{}

	if !ms.HasAny() {
		t.Error("HasAny() with no arguments = false, want true; unconditional artifacts would be dropped")
	}

	if ms.HasAny(monitoring.MechanismAbsentRules) {
		t.Error("HasAny(absent_rules) = true on an empty set")
	}
}

// TestDerive_NilConfig pins that a nil policy is an error, not a panic.
func TestDerive_NilConfig(t *testing.T) {
	t.Parallel()

	if _, err := monitoring.Derive(nil, monitoring.Options{}); err == nil {
		t.Error("Derive(nil) = nil error, want a refusal")
	}
}

// ruleNames extracts the names from a rule slice.
func ruleNames(rules []monitoring.Rule) []string {
	out := make([]string, 0, len(rules))
	for i := range rules {
		out = append(out, rules[i].Name)
	}

	return out
}

// mechanismDiff describes the difference between two mechanism sets.
func mechanismDiff(got, want []monitoring.Mechanism) string {
	if reflect.DeepEqual(got, want) {
		return ""
	}

	return "got " + join(got) + ", want " + join(want)
}

func join(ms []monitoring.Mechanism) string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, string(m))
	}

	return "[" + strings.Join(out, " ") + "]"
}

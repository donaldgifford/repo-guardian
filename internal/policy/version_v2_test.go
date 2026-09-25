package policy

import (
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// goldenVersionV2 pins the v2 version of testdata/version_v2. Changing
// it re-checks every repository in every fleet: update it only for a
// deliberate change to the hash input.
const goldenVersionV2 = "v2:14ae2e64b32cfd8e5f43f42b5d8d8f1b9f9a82563e6b07b30d10e47d87af97d4"

// The fixture's template names, spelled as the fixture spells them.
const (
	fixtureCodeowners = "codeowners"
	fixtureRenovate   = "renovate"
)

var versionV2Templates = map[string]string{
	fixtureCodeowners: "* @acme/platform\n",
	fixtureRenovate:   "{\"extends\": [\"config:recommended\"]}\n",
}

func loadVersionFixture(t *testing.T, name string) *PolicyConfig {
	t.Helper()

	cfg, err := Load(filepath.Join("testdata", "version_v2", name))
	if err != nil {
		t.Fatalf("Load(%s): %v", name, err)
	}

	return cfg
}

func mustVersionV2(t *testing.T, cfg *PolicyConfig, templates map[string]string) string {
	t.Helper()

	v, err := VersionV2(cfg, templates)
	if err != nil {
		t.Fatalf("VersionV2: %v", err)
	}

	return v
}

func TestVersionV2_Golden(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"guardian.hcl", "guardian_reordered.hcl"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := mustVersionV2(t, loadVersionFixture(t, name), versionV2Templates)
			if got != goldenVersionV2 {
				t.Errorf("VersionV2 = %s, want golden %s", got, goldenVersionV2)
			}
		})
	}
}

func TestVersionV2_NeverEqualsV1(t *testing.T) {
	t.Parallel()

	cfg := loadVersionFixture(t, "guardian.hcl")

	v1, err := Version(cfg, versionV2Templates)
	if err != nil {
		t.Fatalf("Version: %v", err)
	}

	if v2 := mustVersionV2(t, cfg, versionV2Templates); v1 == v2 || !strings.HasPrefix(v2, VersionV2Prefix) {
		t.Errorf("v1 %s, v2 %s: want distinct, v2-prefixed", v1, v2)
	}
}

func TestVersionV2_NilConfig(t *testing.T) {
	t.Parallel()

	if _, err := VersionV2(nil, nil); err == nil {
		t.Error("VersionV2(nil) succeeded")
	}
}

// versionV2Hashed lists every policy field that feeds VersionV2.
var versionV2Hashed = []string{
	"PolicyConfig.IgnoreList", "PolicyConfig.Scope", "PolicyConfig.Defaults",
	"PolicyConfig.FileRules", "PolicyConfig.SettingRules", "PolicyConfig.BranchProtectionRules",
	"GuardianConfig.DryRun", "GuardianConfig.SkipForks", "GuardianConfig.SkipArchived",
	"GuardianConfig.AutoClosePR", "GuardianConfig.OrphanCleanup",
	"DefaultsConfig.PR",
	"FileRuleConfig.Type", "FileRuleConfig.Name", "FileRuleConfig.Enabled", "FileRuleConfig.Check",
	"FileRuleConfig.Paths", "FileRuleConfig.Target", "FileRuleConfig.Template", "FileRuleConfig.PR",
	"FileRuleConfig.Assertions", "FileRuleConfig.Ignore", "FileRuleConfig.Scope",
	"FileRuleConfig.Reconcilers", "FileRuleConfig.When",
	"WhenConfig.RuleSatisfied",
	"PRConfig.SearchTerms", "PRConfig.Title", "PRConfig.Body", "PRConfig.Labels",
	"PRConfig.Inherits", "PRConfig.LabelsSet",
	"AssertionConfig.Pattern", "AssertionConfig.NotPattern", "AssertionConfig.YAMLPath",
	"AssertionConfig.Contains", "AssertionConfig.Equals", "AssertionConfig.NonEmpty", "AssertionConfig.Message",
	"IgnoreConfig.Repos",
	"ScopeConfig.Orgs",
	"SettingRuleConfig.Name", "SettingRuleConfig.Enabled", "SettingRuleConfig.Property",
	"SettingRuleConfig.Expected", "SettingRuleConfig.Remediate", "SettingRuleConfig.Ignore", "SettingRuleConfig.Scope",
	"BranchProtectionRuleConfig.Name", "BranchProtectionRuleConfig.Enabled", "BranchProtectionRuleConfig.Branch",
	"BranchProtectionRuleConfig.RequirePR", "BranchProtectionRuleConfig.RequiredApprovals",
	"BranchProtectionRuleConfig.DismissStaleReviews", "BranchProtectionRuleConfig.RequireStatusChecks",
	"BranchProtectionRuleConfig.EnforceAdmins", "BranchProtectionRuleConfig.RequireLinearHistory",
	"BranchProtectionRuleConfig.Remediate", "BranchProtectionRuleConfig.Ignore", "BranchProtectionRuleConfig.Scope",
	"ReconcilerConfig.Type", "ReconcilerConfig.Watch", "ReconcilerConfig.Mode", "ReconcilerConfig.DeleteExtra",
	"ReconcilerConfig.PR", "ReconcilerConfig.AnnotationProperties",
}

// versionV2NotHashed lists every policy field deliberately left out of
// VersionV2: operational knobs, and values derived from hashed ones.
var versionV2NotHashed = []string{
	"PolicyConfig.Guardian", // container; its hashed fields are listed individually
	"GuardianConfig.ScheduleInterval", "GuardianConfig.ParsedScheduleInterval",
	"GuardianConfig.WorkerCount", "GuardianConfig.QueueSize",
	"GuardianConfig.LogLevel", "GuardianConfig.RateLimitThreshold",
	"PRConfig.CompiledTitle", "PRConfig.CompiledBody", // compiled from Title/Body
}

// TestVersionV2_EveryFieldClassified walks every struct reachable from
// PolicyConfig in this package and fails on an exported field that is
// neither hashed nor deliberately not hashed. Adding a policy field
// must be a decision about whether it re-checks the fleet.
func TestVersionV2_EveryFieldClassified(t *testing.T) {
	t.Parallel()

	classified := make(map[string]bool)
	for _, f := range append(slices.Clone(versionV2Hashed), versionV2NotHashed...) {
		if classified[f] {
			t.Errorf("%s is classified twice", f)
		}

		classified[f] = true
	}

	seen := make(map[string]bool)
	pkg := reflect.TypeFor[PolicyConfig]().PkgPath()

	var walk func(reflect.Type)

	walk = func(typ reflect.Type) {
		for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Map {
			typ = typ.Elem()
		}

		if typ.Kind() != reflect.Struct || typ.PkgPath() != pkg || seen[typ.Name()] {
			return
		}

		seen[typ.Name()] = true

		for f := range typ.Fields() {
			if !f.IsExported() {
				continue
			}

			key := typ.Name() + "." + f.Name
			if !classified[key] {
				t.Errorf("policy field %s is unclassified: add it to versionV2Hashed (and VersionV2's input) or versionV2NotHashed", key)
			}

			delete(classified, key)
			walk(f.Type)
		}
	}

	walk(reflect.TypeFor[PolicyConfig]())

	for f := range classified {
		t.Errorf("classified field %s does not exist", f)
	}
}

func TestVersionV2_WhatChangesTheVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(cfg *PolicyConfig, templates map[string]string)
		changes bool
	}{
		{"guardian.log_level", func(c *PolicyConfig, _ map[string]string) { c.Guardian.LogLevel = "debug" }, false},
		{"guardian.rate_limit_threshold", func(c *PolicyConfig, _ map[string]string) { c.Guardian.RateLimitThreshold = 0.5 }, false},
		{"guardian.schedule_interval", func(c *PolicyConfig, _ map[string]string) { c.Guardian.ScheduleInterval = "1h" }, false},
		{"guardian.worker_count", func(c *PolicyConfig, _ map[string]string) { c.Guardian.WorkerCount = 99 }, false},
		{"guardian.queue_size", func(c *PolicyConfig, _ map[string]string) { c.Guardian.QueueSize = 99 }, false},
		{"guardian.dry_run", func(c *PolicyConfig, _ map[string]string) { c.Guardian.DryRun = true }, true},
		{"rule paths", func(c *PolicyConfig, _ map[string]string) {
			c.FileRules[0].Paths = append(c.FileRules[0].Paths, "docs/CODEOWNERS")
		}, true},
		{"template content", func(_ *PolicyConfig, tpl map[string]string) { tpl[fixtureCodeowners] = "* @acme/other\n" }, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := loadVersionFixture(t, "guardian.hcl")
			templates := maps.Clone(versionV2Templates)
			tt.mutate(cfg, templates)

			if changed := mustVersionV2(t, cfg, templates) != goldenVersionV2; changed != tt.changes {
				t.Errorf("version changed = %v, want %v", changed, tt.changes)
			}
		})
	}
}

package alert_test

import (
	"bytes"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/donaldgifford/repo-guardian/internal/monitoring"
	"github.com/donaldgifford/repo-guardian/internal/monitoring/alert"
)

// modelWith builds a model carrying exactly the given mechanisms.
func modelWith(ms ...monitoring.Mechanism) *monitoring.Model {
	set := make(monitoring.Mechanisms, len(ms))
	for _, m := range ms {
		set[m] = struct{}{}
	}

	return &monitoring.Model{Mechanisms: set}
}

func specByName(t *testing.T, specs []alert.Spec, name string) alert.Spec {
	t.Helper()

	for i := range specs {
		if specs[i].Name == name {
			return specs[i]
		}
	}

	t.Fatalf("no alert named %q; got %v", name, names(specs))

	return alert.Spec{}
}

func names(specs []alert.Spec) []string {
	out := make([]string, 0, len(specs))
	for i := range specs {
		out = append(out, specs[i].Name)
	}

	return out
}

// TestCatalogue_WindowIsAtLeastFor pins INV-0012 finding E.
//
// An alert whose range-vector window is shorter than its pending period
// can, for a sparse metric, never hold its condition true long enough to
// fire — the window empties out between samples. That is how the
// IMPL-0021 A7 alert was dead on arrival: a 15m window over a metric
// observed roughly once per 24h, with a 30m `for`.
//
// The finding says to judge each alert against its own metric's cadence
// rather than rewriting all of them, and that is right — but window >=
// for is the conservative direction. It can only smooth an alert, never
// make a correct one stop firing, so it is enforced uniformly and two
// existing alerts were widened to satisfy it.
func TestCatalogue_WindowIsAtLeastFor(t *testing.T) {
	t.Parallel()

	for _, s := range alert.Catalogue() {
		// A zero window means the expression is over an instant vector,
		// which is always current and has no emptying problem.
		if s.Window == 0 {
			continue
		}

		if s.Window < s.For {
			t.Errorf("%s: window %s is shorter than for %s; a sparse source can never hold this true",
				s.Name, s.Window, s.For)
		}
	}
}

// windowPattern finds range-vector selectors in a PromQL expression.
var windowPattern = regexp.MustCompile(`\[(\d+[smhd])\]`)

// TestCatalogue_DeclaredWindowMatchesTheExpression pins that the Window
// field is not a lie.
//
// Window exists so the invariant above is checkable without a PromQL
// parser, which makes it a hand-maintained copy of something already in
// the expression — precisely the shape that drifts. This compares the
// declaration against every literal window in the expression.
func TestCatalogue_DeclaredWindowMatchesTheExpression(t *testing.T) {
	t.Parallel()

	for _, s := range alert.Catalogue() {
		found := windowPattern.FindAllStringSubmatch(s.Expr, -1)

		if len(found) == 0 {
			if s.Window != 0 {
				t.Errorf("%s: declares window %s but the expression has no range selector", s.Name, s.Window)
			}

			continue
		}

		if s.Window == 0 {
			t.Errorf("%s: expression uses %s but Window is unset", s.Name, found[0][1])

			continue
		}

		want := durationToken(t, s.Window)

		for _, m := range found {
			if m[1] != want {
				t.Errorf("%s: expression window [%s] does not match declared Window %s (%s)",
					s.Name, m[1], s.Window, want)
			}
		}
	}
}

func durationToken(t *testing.T, d time.Duration) string {
	t.Helper()

	switch {
	case d%time.Hour == 0:
		return strconv.Itoa(int(d/time.Hour)) + "h"
	case d%time.Minute == 0:
		return strconv.Itoa(int(d/time.Minute)) + "m"
	default:
		t.Fatalf("duration %s is not a whole number of minutes", d)

		return ""
	}
}

// TestCatalogue_EveryAlertIsDescribed pins that an alert cannot ship
// without saying what to do about it.
func TestCatalogue_EveryAlertIsDescribed(t *testing.T) {
	t.Parallel()

	seen := make(map[string]bool)

	for _, s := range alert.Catalogue() {
		switch {
		case s.Name == "":
			t.Error("an alert has no name")
		case s.Group == "":
			t.Errorf("%s: no group", s.Name)
		case s.Expr == "":
			t.Errorf("%s: no expression", s.Name)
		case s.Severity == "":
			t.Errorf("%s: no severity", s.Name)
		case s.Summary == "":
			t.Errorf("%s: no summary; a page with no summary is a page nobody can triage", s.Name)
		}

		if seen[s.Name] {
			t.Errorf("%s: duplicate alert name", s.Name)
		}

		seen[s.Name] = true
	}
}

// TestGenerate_MechanismScoping is the core of task 5.3.
func TestGenerate_MechanismScoping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		mechanisms []monitoring.Mechanism
		wantKept   []string
		wantGone   []string
	}{
		{
			// Nothing configured: only the unconditional service-health
			// and infrastructure alerts survive.
			name:       "no mechanisms",
			mechanisms: nil,
			wantKept: []string{
				"RepoGuardianNoRepoChecks",
				"RepoGuardianPostureExportStalled",
				"RepoGuardianNoSchedulerLeader",
				// Unconditional on purpose: an installation can lose
				// read access at any time regardless of config.
				"RepoGuardianRepoAccessDenied",
			},
			wantGone: []string{
				"RepoGuardianPropertySchemaMissing",
				"RepoGuardianCatalogParseFailures",
				"RepoGuardianPropertiesPRBurst",
				"RepoGuardianRuleNeverApplies",
				"RepoGuardianSettingRemediationChurn",
				"RepoGuardianBranchProtectionChurn",
				"RepoGuardianPRBurst",
				"RepoGuardianPRDrift",
			},
		},
		{
			// The IMPL's own example: no PropertySchemaMissing without a
			// custom_properties reconciler.
			name:       "custom properties in api mode",
			mechanisms: []monitoring.Mechanism{monitoring.MechanismCustomProperties},
			wantKept: []string{
				"RepoGuardianPropertySchemaMissing",
				"RepoGuardianCatalogParseFailures",
			},
			// The preflight runs in both modes, so the two above ship —
			// but the PR counter is github-action only.
			wantGone: []string{"RepoGuardianPropertiesPRBurst"},
		},
		{
			name: "custom properties in github-action mode",
			mechanisms: []monitoring.Mechanism{
				monitoring.MechanismCustomProperties,
				monitoring.MechanismCustomPropertiesGHA,
			},
			wantKept: []string{
				"RepoGuardianPropertySchemaMissing",
				"RepoGuardianPropertiesPRBurst",
			},
		},
		{
			// out_of_scope_total has no producer in legacy mode.
			name:       "strict scope",
			mechanisms: []monitoring.Mechanism{monitoring.MechanismStrictScope},
			wantKept:   []string{"RepoGuardianRuleNeverApplies"},
		},
		{
			name:       "setting remediation",
			mechanisms: []monitoring.Mechanism{monitoring.MechanismSettingRemediation},
			wantKept:   []string{"RepoGuardianSettingRemediationChurn"},
			wantGone:   []string{"RepoGuardianBranchProtectionChurn"},
		},
		{
			// With auto-close OFF, an open PR whose rules are satisfied
			// is the DESIGNED behaviour, so PRDrift would fire
			// permanently on a correctly-configured deployment.
			name:       "file rules without auto-close",
			mechanisms: []monitoring.Mechanism{monitoring.MechanismFileRules},
			wantKept:   []string{"RepoGuardianPRBurst", "RepoGuardianStaleOpenPRs"},
			wantGone:   []string{"RepoGuardianPRDrift"},
		},
		{
			// dry_run suppresses prs_created_total, so every PR-shaped
			// alert is empty by construction.
			name: "dry run suppresses PR alerts",
			mechanisms: []monitoring.Mechanism{
				monitoring.MechanismFileRules,
				monitoring.MechanismAutoClosePR,
				monitoring.MechanismDryRun,
			},
			wantGone: []string{
				"RepoGuardianPRBurst",
				"RepoGuardianPRDrift",
				"RepoGuardianStaleOpenPRs",
			},
			// Service health is unaffected by dry run.
			wantKept: []string{"RepoGuardianNoRepoChecks", "RepoGuardianPostureExportStalled"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			kept, skipped := alert.Generate(modelWith(tt.mechanisms...))
			got := names(kept)

			for _, want := range tt.wantKept {
				if !slices.Contains(got, want) {
					t.Errorf("%s was dropped; kept %v", want, got)
				}
			}

			for _, gone := range tt.wantGone {
				if slices.Contains(got, gone) {
					t.Errorf("%s was emitted with no producer for its series", gone)
				}

				if !slices.ContainsFunc(skipped, func(s alert.Skip) bool { return s.Alert == gone }) {
					t.Errorf("%s was dropped without a recorded reason", gone)
				}
			}
		})
	}
}

// TestGenerate_RepoAccessDeniedKeepsItsSelector pins the load-bearing
// label matcher.
//
// repos_parked_total also counts routine archived and fork parks, which
// happen on every normal onboarding sweep. The same expression without
// {reason="access_denied"} pages on healthy behaviour, which is worse
// than no alert: it trains the operator to ignore the one signal that
// says the App lost access to a repository.
func TestGenerate_RepoAccessDeniedKeepsItsSelector(t *testing.T) {
	t.Parallel()

	kept, _ := alert.Generate(modelWith())
	spec := specByName(t, kept, "RepoGuardianRepoAccessDenied")

	if !strings.Contains(spec.Expr, `reason="access_denied"`) {
		t.Errorf("expression has no reason selector, so it fires on every archived and forked repo:\n%s", spec.Expr)
	}
}

// TestGenerate_SkipsAreExplained pins that every omission is reportable.
func TestGenerate_SkipsAreExplained(t *testing.T) {
	t.Parallel()

	_, skipped := alert.Generate(modelWith())

	if len(skipped) == 0 {
		t.Fatal("no alerts were skipped for an empty mechanism set")
	}

	for _, s := range skipped {
		if s.Reason == "" {
			t.Errorf("%s was skipped with no reason", s.Alert)
		}
	}
}

// TestGenerate_NilModel pins that a nil model yields nothing rather
// than panicking or emitting everything.
func TestGenerate_NilModel(t *testing.T) {
	t.Parallel()

	kept, skipped := alert.Generate(nil)
	if len(kept) != 0 || len(skipped) != 0 {
		t.Errorf("Generate(nil) = %d kept, %d skipped; want nothing", len(kept), len(skipped))
	}
}

// TestRenderGroups_ParsesAsPrometheusRuleSpec pins the emitted shape.
func TestRenderGroups_ParsesAsPrometheusRuleSpec(t *testing.T) {
	t.Parallel()

	kept, _ := alert.Generate(modelWith(monitoring.MechanismCustomProperties))

	raw, err := alert.RenderGroups(alert.Groups(kept))
	if err != nil {
		t.Fatalf("RenderGroups() = %v, want nil", err)
	}

	var decoded struct {
		Groups []struct {
			Name  string `yaml:"name"`
			Rules []struct {
				Alert       string            `yaml:"alert"`
				Expr        string            `yaml:"expr"`
				For         string            `yaml:"for"`
				Labels      map[string]string `yaml:"labels"`
				Annotations map[string]string `yaml:"annotations"`
			} `yaml:"rules"`
		} `yaml:"groups"`
	}

	if err := yaml.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("generated YAML does not parse: %v\n%s", err, raw)
	}

	if len(decoded.Groups) == 0 {
		t.Fatalf("no groups rendered:\n%s", raw)
	}

	total := 0

	for _, g := range decoded.Groups {
		if g.Name == "" {
			t.Error("a group has no name")
		}

		for _, r := range g.Rules {
			total++

			if r.Labels["severity"] == "" {
				t.Errorf("%s: no severity label", r.Alert)
			}

			if r.Annotations["summary"] == "" {
				t.Errorf("%s: no summary annotation", r.Alert)
			}
		}
	}

	if total != len(kept) {
		t.Errorf("rendered %d rules, generated %d specs", total, len(kept))
	}

	if !strings.HasPrefix(string(raw), "---\n") {
		t.Error("output has no document-start marker; the repo's yamllint requires one")
	}
}

// TestRenderPrometheusRule_StampsTheNamespace pins the CR shape.
//
// A rendered manifest without an explicit namespace lands wherever the
// applying tool defaults to, which under ArgoCD is frequently not where
// the operator intended — the same failure the chart templates carry a
// namespace for (PR #67).
func TestRenderPrometheusRule_StampsTheNamespace(t *testing.T) {
	t.Parallel()

	kept, _ := alert.Generate(modelWith())

	raw, err := alert.RenderPrometheusRule(alert.PrometheusRuleMeta{
		Name:      "repo-guardian",
		Namespace: "monitoring",
		Labels:    map[string]string{"release": "kube-prometheus-stack"},
	}, alert.Groups(kept))
	if err != nil {
		t.Fatalf("RenderPrometheusRule() = %v, want nil", err)
	}

	body := string(raw)

	for _, want := range []string{
		"apiVersion: monitoring.coreos.com/v1",
		"kind: PrometheusRule",
		"name: repo-guardian",
		"namespace: monitoring",
		"release: kube-prometheus-stack",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("rendered CR is missing %q:\n%s", want, body)
		}
	}
}

// TestRenderPrometheusRule_RequiresAName pins the refusal.
func TestRenderPrometheusRule_RequiresAName(t *testing.T) {
	t.Parallel()

	if _, err := alert.RenderPrometheusRule(alert.PrometheusRuleMeta{}, nil); err == nil {
		t.Error("RenderPrometheusRule() with no name = nil, want an error")
	}
}

// TestRender_IsDeterministic pins byte-stability for the drift gate.
func TestRender_IsDeterministic(t *testing.T) {
	t.Parallel()

	kept, _ := alert.Generate(modelWith(
		monitoring.MechanismCustomProperties,
		monitoring.MechanismFileRules,
		monitoring.MechanismStrictScope,
	))

	first, err := alert.RenderGroups(alert.Groups(kept))
	if err != nil {
		t.Fatalf("RenderGroups() = %v, want nil", err)
	}

	for i := range 8 {
		got, err := alert.RenderGroups(alert.Groups(kept))
		if err != nil {
			t.Fatalf("RenderGroups() = %v, want nil", err)
		}

		if !bytes.Equal(got, first) {
			t.Fatalf("render %d differs:\n%s\n%s", i, first, got)
		}
	}
}

// TestFor_RendersAsPrometheusDurations pins the formatting.
//
// time.Duration.String() gives "1h0m0s" and "30m0s". Prometheus parses
// them, but a generated manifest a human reviews should read the way a
// hand-written one does.
func TestFor_RendersAsPrometheusDurations(t *testing.T) {
	t.Parallel()

	kept, _ := alert.Generate(modelWith())

	raw, err := alert.RenderGroups(alert.Groups(kept))
	if err != nil {
		t.Fatalf("RenderGroups() = %v, want nil", err)
	}

	if strings.Contains(string(raw), "0m0s") {
		t.Errorf("durations render in Go's format:\n%s", raw)
	}
}

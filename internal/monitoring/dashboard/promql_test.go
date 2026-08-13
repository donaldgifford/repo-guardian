package dashboard_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/monitoring"
	"github.com/donaldgifford/repo-guardian/internal/monitoring/dashboard"
)

// panelTarget is one query on one panel.
type panelTarget struct {
	Dashboard string
	Panel     string
	Expr      string
}

// suiteTargets extracts every PromQL expression in the suite.
func suiteTargets(t *testing.T, m *monitoring.Model) []panelTarget {
	t.Helper()

	var out []panelTarget

	suite := dashboard.Suite(m, dashboard.Datasources{}.WithDefaults())

	for i := range suite {
		d := &suite[i]

		raw, err := dashboard.Render(d.Builder)
		if err != nil {
			t.Fatalf("Render(%s) = %v, want nil", d.Slug, err)
		}

		var decoded struct {
			Panels []struct {
				Title   string `json:"title"`
				Type    string `json:"type"`
				Targets []struct {
					Expr string `json:"expr"`
				} `json:"targets"`
			} `json:"panels"`
		}

		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("the rendered %s is not JSON: %v", d.Slug, err)
		}

		for _, p := range decoded.Panels {
			if p.Type == "row" {
				continue
			}

			if len(p.Targets) == 0 {
				t.Errorf("%s: panel %q has no query; it would render permanently empty", d.Slug, p.Title)
			}

			for _, tgt := range p.Targets {
				out = append(out, panelTarget{Dashboard: d.Slug, Panel: p.Title, Expr: tgt.Expr})
			}
		}
	}

	return out
}

// grafanaVars are the interpolations Grafana performs before a query
// reaches Prometheus. promtool sees the raw expression, so they are
// substituted with something representative first.
var grafanaVars = strings.NewReplacer(
	"$__rate_interval", "5m",
	"$__interval", "1m",
	"$org", "acme",
	"[[org]]", "acme",
)

// TestSuite_EveryPanelQueryParses is the only thing standing between a
// typo and a permanently empty panel.
//
// A dashboard with invalid PromQL renders perfectly. The panel just
// shows nothing, which is indistinguishable from a compliant fleet —
// the exact confusion this whole design exists to remove. Nothing in
// Go can tell the two apart, because a query is a string here.
func TestSuite_EveryPanelQueryParses(t *testing.T) {
	t.Parallel()

	bin, err := exec.LookPath("promtool")
	if err != nil {
		t.Skip("promtool not on PATH; mise supplies it (see mise.toml)")
	}

	targets := append(suiteTargets(t, legacyModel()), suiteTargets(t, strictModel())...)

	// A suite that generated nothing would pass this test vacuously,
	// which is the failure mode promtool's own "0 rules found" exit-0
	// taught us to guard against.
	if len(targets) == 0 {
		t.Skip("the suite has no panels yet; nothing to parse")
	}

	var b strings.Builder

	b.WriteString("groups:\n  - name: dashboard-panels\n    rules:\n")

	for i, tgt := range targets {
		// Wrapped as alert rules because `promtool check rules` is the
		// only expression parser promtool exposes offline. The alert
		// name carries the panel so a failure names the panel rather
		// than a line number.
		fmt.Fprintf(&b, "      - alert: Panel%d\n        expr: |-\n%s\n        annotations:\n          panel: %q\n",
			i, indent(grafanaVars.Replace(tgt.Expr), "          "), tgt.Dashboard+" / "+tgt.Panel)
	}

	path := filepath.Join(t.TempDir(), "panels.yaml")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("WriteFile() = %v, want nil", err)
	}

	// bin comes from LookPath and path is a file under t.TempDir().
	out, err := exec.CommandContext(t.Context(), bin, "check", "rules", path).CombinedOutput()
	if err != nil {
		t.Fatalf("promtool rejected a panel query: %v\n%s\n---\n%s", err, out, b.String())
	}
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}

	return strings.Join(lines, "\n")
}

// TestSuite_PostureQueriesDedupeAcrossReplicas pins the aggregation
// order on the leader-published gauges.
//
// The posture gauges are served by one replica, and a demoted replica
// keeps serving its last values until the process restarts. `sum` over
// those series multiplies the fleet by the number of replicas that ever
// held the lock — silently, and in the direction that makes compliance
// look worse than it is. The inner aggregation must be `max by (...)`.
func TestSuite_PostureQueriesDedupeAcrossReplicas(t *testing.T) {
	t.Parallel()

	postureGauges := []string{
		"repo_guardian_repos_tracked",
		"repo_guardian_repos_actionable",
		"repo_guardian_repos_unmeasurable",
	}

	for _, tgt := range append(suiteTargets(t, legacyModel()), suiteTargets(t, strictModel())...) {
		for _, gauge := range postureGauges {
			if !strings.Contains(tgt.Expr, gauge) {
				continue
			}

			if !strings.Contains(tgt.Expr, "max by (") {
				t.Errorf("%s / %s queries %s without a max-by dedupe:\n%s",
					tgt.Dashboard, tgt.Panel, gauge, tgt.Expr)
			}
		}
	}
}

// TestSuite_LeaderGaugesAreNotSummed pins the same rule for the
// scheduler's own leadership gauge.
func TestSuite_LeaderGaugesAreNotSummed(t *testing.T) {
	t.Parallel()

	for _, tgt := range append(suiteTargets(t, legacyModel()), suiteTargets(t, strictModel())...) {
		if !strings.Contains(tgt.Expr, "repo_guardian_scheduler_is_leader") {
			continue
		}

		if strings.Contains(tgt.Expr, "sum(repo_guardian_scheduler_is_leader") {
			t.Errorf("%s / %s sums the leader gauge; during failover both replicas hold a series:\n%s",
				tgt.Dashboard, tgt.Panel, tgt.Expr)
		}
	}
}

// TestSuite_EveryPanelIsDescribed pins that a panel says what it means.
//
// A compliance panel with no description is a number an operator has to
// guess at, and the guesses that matter here — is this a rate or a
// state, does it include parked repositories — are exactly the ones
// INV-0013 found people getting wrong.
func TestSuite_EveryPanelIsDescribed(t *testing.T) {
	t.Parallel()

	suite := dashboard.Suite(strictModel(), dashboard.Datasources{}.WithDefaults())

	for i := range suite {
		d := &suite[i]

		raw, err := dashboard.Render(d.Builder)
		if err != nil {
			t.Fatalf("Render(%s) = %v, want nil", d.Slug, err)
		}

		var decoded struct {
			Panels []struct {
				Title       string `json:"title"`
				Type        string `json:"type"`
				Description string `json:"description"`
			} `json:"panels"`
		}

		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("the rendered %s is not JSON: %v", d.Slug, err)
		}

		for _, p := range decoded.Panels {
			if p.Type == "row" {
				continue
			}

			if strings.TrimSpace(p.Description) == "" {
				t.Errorf("%s: panel %q has no description", d.Slug, p.Title)
			}
		}
	}
}

package dashboard_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/monitoring"
	"github.com/donaldgifford/repo-guardian/internal/monitoring/dashboard"
)

// legacyModel is a policy with no top-level scope block, so no org is
// declarable and the rows have to be discovered.
func legacyModel() *monitoring.Model {
	return &monitoring.Model{
		Rules: []monitoring.Rule{{Name: "codeowners", Kind: monitoring.RuleKindFile}},
	}
}

// strictModel is a policy with a top-level scope, including one glob
// pattern that cannot become a declared row.
func strictModel() *monitoring.Model {
	return &monitoring.Model{
		Strict: true,
		Orgs: []monitoring.Org{
			{Name: "acme"},
			{Name: "acme-labs-*", Pattern: true},
			{Name: "widgets"},
		},
		Rules: []monitoring.Rule{
			{Name: "codeowners", Kind: monitoring.RuleKindFile, Orgs: []string{"acme"}},
			{Name: "dependabot", Kind: monitoring.RuleKindFile, Orgs: []string{"acme", "widgets"}},
		},
		Mechanisms: monitoring.Mechanisms{monitoring.MechanismStrictScope: struct{}{}},
	}
}

// findDashboard returns one dashboard from a generated suite.
func findDashboard(t *testing.T, m *monitoring.Model) map[string]any {
	t.Helper()

	suite := dashboard.Suite(m, dashboard.Datasources{}.WithDefaults())

	const slug = "repo-guardian-detail"

	for i := range suite {
		if suite[i].Slug != slug {
			continue
		}

		raw, err := dashboard.Render(suite[i].Builder)
		if err != nil {
			t.Fatalf("Render(%s) = %v, want nil", slug, err)
		}

		var out map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("the rendered %s is not JSON: %v", slug, err)
		}

		return out
	}

	t.Fatalf("no dashboard with slug %q in the suite", slug)

	return nil
}

// rowTitles returns the row titles of a rendered dashboard, in order.
func rowTitles(t *testing.T, d map[string]any) []string {
	t.Helper()

	panels, ok := d["panels"].([]any)
	if !ok {
		t.Fatalf("panels is %T, want a list", d["panels"])
	}

	var out []string

	for _, p := range panels {
		panel, ok := p.(map[string]any)
		if !ok || panel["type"] != "row" {
			continue
		}

		title, _ := panel["title"].(string)
		out = append(out, title)
	}

	return out
}

// TestE2_GeneratesARowPerDeclaredOrg is the point of deriving
// dashboards from the config at all.
//
// A declared row that renders empty says "this org has stopped
// reporting". A row discovered from the series cannot say that: when
// the org disappears, so does its row, and the dashboard looks exactly
// as healthy as before. That difference is the whole reason the model
// carries an org list.
func TestE2_GeneratesARowPerDeclaredOrg(t *testing.T) {
	t.Parallel()

	got := rowTitles(t, findDashboard(t, strictModel()))

	want := []string{"Fleet", "Organisation: acme", "Organisation: widgets"}
	if len(got) != len(want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestE2_PatternOrgsGetNoRow pins that a glob is not enumerated.
//
// `orgs = ["acme-labs-*"]` cannot be expanded without asking the API,
// and a row literally titled "acme-labs-*" would query
// `{org="acme-labs-*"}` — an exact-match selector against a name no
// repository has, so the row would render permanently empty and look
// exactly like an org that went silent.
func TestE2_PatternOrgsGetNoRow(t *testing.T) {
	t.Parallel()

	d := findDashboard(t, strictModel())

	for _, title := range rowTitles(t, d) {
		if strings.Contains(title, "*") {
			t.Errorf("row %q was generated for a glob pattern", title)
		}
	}

	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("Marshal() = %v, want nil", err)
	}

	if strings.Contains(string(raw), "acme-labs-") {
		t.Errorf("the pattern org leaked into a query:\n%s", raw)
	}
}

// TestE2_LegacyModeFallsBackToTheVariable pins the escape hatch.
func TestE2_LegacyModeFallsBackToTheVariable(t *testing.T) {
	t.Parallel()

	d := findDashboard(t, legacyModel())

	rows := rowTitles(t, d)
	if len(rows) != 2 || rows[1] != "Organisation: $org" {
		t.Errorf("rows = %v, want a fleet row and one variable-driven org row", rows)
	}

	templating, ok := d["templating"].(map[string]any)
	if !ok {
		t.Fatalf("templating is %T, want an object", d["templating"])
	}

	list, ok := templating["list"].([]any)
	if !ok || len(list) == 0 {
		t.Fatalf("the dashboard declares no template variable, so $org never resolves: %v", templating)
	}
}

// TestE2_DeclaredOrgsDeclareNoVariable pins the other direction.
//
// A variable nothing references invites an operator to switch it and
// wonder why no panel moved.
func TestE2_DeclaredOrgsDeclareNoVariable(t *testing.T) {
	t.Parallel()

	d := findDashboard(t, strictModel())

	templating, ok := d["templating"].(map[string]any)
	if !ok {
		return
	}

	if list, ok := templating["list"].([]any); ok && len(list) > 0 {
		t.Errorf("declared-row dashboard still carries a template variable: %v", list)
	}
}

// TestE2_OmitsFleetScopedInfrastructure pins the E2/E3 split.
//
// Queue, store and scheduler series are fleet-scoped. Repeated under a
// per-org heading they would show the same number in every row and
// invite someone to read it as that org's share.
func TestE2_OmitsFleetScopedInfrastructure(t *testing.T) {
	t.Parallel()

	d := findDashboard(t, strictModel())

	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("Marshal() = %v, want nil", err)
	}

	for _, series := range []string{
		"repo_guardian_queue_depth",
		"repo_guardian_queue_delayed_depth",
		"repo_guardian_store_query_seconds",
		"repo_guardian_scheduler_is_leader",
	} {
		if strings.Contains(string(raw), series) {
			t.Errorf("E2 charts %s, which is fleet-scoped and belongs on E3", series)
		}
	}
}

// TestE2_JoinsInstallationKeyedSeriesOntoOrgs pins the group_left.
//
// rate_limit_remaining is keyed by installation_id, which nobody reads
// per org without the installation_info join. Dropping the join would
// leave the panel technically correct and practically unreadable.
func TestE2_JoinsInstallationKeyedSeriesOntoOrgs(t *testing.T) {
	t.Parallel()

	d := findDashboard(t, strictModel())

	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("Marshal() = %v, want nil", err)
	}

	body := string(raw)

	if !strings.Contains(body, "repo_guardian_installation_info") {
		t.Errorf("E2 never joins installation_info, so its installation-keyed panels cannot be read per org")
	}

	if !strings.Contains(body, "group_left(org)") {
		t.Errorf("the installation_info join does not carry the org label across")
	}
}

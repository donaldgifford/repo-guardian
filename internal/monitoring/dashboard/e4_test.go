package dashboard_test

import (
	"strings"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/monitoring"
	"github.com/donaldgifford/repo-guardian/internal/monitoring/dashboard"
)

const e4Slug = "repo-guardian-logs"

// e4Targets returns E4's panel queries.
func e4Targets(t *testing.T) []panelTarget {
	t.Helper()

	var out []panelTarget

	for _, tgt := range suiteTargets(t, strictModel()) {
		if tgt.Dashboard == e4Slug {
			out = append(out, tgt)
		}
	}

	if len(out) == 0 {
		t.Fatal("the suite has no Loki dashboard; E1-E3 cannot answer 'which repository'")
	}

	return out
}

// TestE4_IsTheOnlyLokiDashboard pins the datasource split.
//
// A Prometheus panel pointed at Loki (or the reverse) does not error —
// it loads forever, or returns nothing — so this is not a cosmetic
// tidiness rule. It is also the reason Query and LogQuery are separate
// types rather than one type with a flag.
func TestE4_IsTheOnlyLokiDashboard(t *testing.T) {
	t.Parallel()

	for _, tgt := range suiteTargets(t, strictModel()) {
		onE4 := tgt.Dashboard == e4Slug

		switch {
		case onE4 && tgt.Datasource != dsLoki:
			t.Errorf("E4 panel %q reads %s; the evidence tier is Loki-only", tgt.Panel, tgt.Datasource)
		case !onE4 && tgt.Datasource == dsLoki:
			t.Errorf("%s panel %q reads Loki; log panels belong on E4", tgt.Dashboard, tgt.Panel)
		}
	}
}

// TestE4_EveryQueryStartsFromTheStreamSelector pins the one input that
// cannot be verified from inside this repository.
//
// Stream labels are minted by the log shipper, not by repo-guardian, so
// a wrong selector is the single most likely reason a freshly imported
// E4 is blank. A panel that hard-coded a selector instead of taking the
// configured one would stay blank after the operator fixed it, and there
// would be nothing to suggest why.
func TestE4_EveryQueryStartsFromTheStreamSelector(t *testing.T) {
	t.Parallel()

	ds := dashboard.Datasources{LogStream: `job="platform/repo-guardian"`}.WithDefaults()

	suite := dashboard.Suite(strictModel(), ds)

	var found bool

	for i := range suite {
		if suite[i].Slug != e4Slug {
			continue
		}

		found = true

		raw, err := dashboard.Render(suite[i].Builder)
		if err != nil {
			t.Fatalf("Render(%s) = %v, want nil", e4Slug, err)
		}

		body := string(raw)

		if !strings.Contains(body, `job=\"platform/repo-guardian\"`) {
			t.Errorf("the configured stream selector never reaches a query:\n%s", body)
		}

		if strings.Contains(body, `app=\"repo-guardian\"`) {
			t.Error("a panel hard-codes the default stream selector, so overriding it leaves that panel blank")
		}
	}

	if !found {
		t.Fatalf("no dashboard with slug %q", e4Slug)
	}
}

// TestE4_MatchesTheLockedCatalogParseLine ties the panel to the log.
//
// The literal is duplicated from internal/reconciler/log_contract_test.go
// on purpose: it is a contract between two packages that do not import
// each other, and a shared constant would let a rename move both ends at
// once and pass. Two independent copies means a rename fails here.
func TestE4_MatchesTheLockedCatalogParseLine(t *testing.T) {
	t.Parallel()

	const locked = "catalog-info parse failed"

	var matched bool

	for _, tgt := range e4Targets(t) {
		if strings.Contains(tgt.Expr, locked) {
			matched = true
		}
	}

	if !matched {
		t.Errorf("no E4 panel matches %q; the counter says how many repositories have a broken "+
			"catalog and only the log says which", locked)
	}
}

// TestE4_ChartsNoPrometheusSeries pins the tier split from the other
// direction to E3's test.
//
// A repo_guardian_* name in a LogQL expression is not a query that
// returns the wrong thing — it is one that returns nothing, forever,
// while looking entirely reasonable in the panel editor.
func TestE4_ChartsNoPrometheusSeries(t *testing.T) {
	t.Parallel()

	for _, tgt := range e4Targets(t) {
		if strings.Contains(tgt.Expr, seriesPrefixRepoGuardian) {
			t.Errorf("E4 panel %q queries a Prometheus series from Loki:\n%s", tgt.Panel, tgt.Expr)
		}
	}
}

// TestE4_GraphPanelsUseMetricQueries is the closest thing to a LogQL
// parser available offline.
//
// There is no logcli in mise.toml and vendoring Loki's parser to check a
// dozen strings is not a trade worth making, so this checks the one
// mistake that is both easy to make and silent: a log-selector
// expression on a graph panel renders an empty graph, and a metric
// expression on a logs panel renders no lines. Neither errors.
func TestE4_GraphPanelsUseMetricQueries(t *testing.T) {
	t.Parallel()

	// The LogQL range-aggregation and unwrap forms E4 uses.
	rangeOps := []string{"count_over_time(", "rate(", "max_over_time(", "sum_over_time("}

	for _, tgt := range e4Targets(t) {
		var isMetric bool

		for _, op := range rangeOps {
			if strings.Contains(tgt.Expr, op) {
				isMetric = true

				break
			}
		}

		switch tgt.PanelType {
		case "timeseries":
			if !isMetric {
				t.Errorf("E4 graph panel %q has no range aggregation, so it plots nothing:\n%s",
					tgt.Panel, tgt.Expr)
			}
		case "logs":
			if isMetric {
				t.Errorf("E4 logs panel %q runs a metric query, so it lists no lines:\n%s",
					tgt.Panel, tgt.Expr)
			}
		}
	}
}

// TestE4_IsModelIndependent pins that the evidence tier does not vary
// with the policy.
//
// E1 and E2 are generated from the config — that is their whole point.
// E4 deliberately is not: it matches on log lines the binary emits
// regardless of which rules are configured, so an operator debugging a
// misconfigured policy still gets a working dashboard. A future edit
// that makes E4 config-driven would break exactly that case.
func TestE4_IsModelIndependent(t *testing.T) {
	t.Parallel()

	render := func(m *monitoring.Model) string {
		t.Helper()

		suite := dashboard.Suite(m, dashboard.Datasources{}.WithDefaults())

		for i := range suite {
			if suite[i].Slug != e4Slug {
				continue
			}

			raw, err := dashboard.Render(suite[i].Builder)
			if err != nil {
				t.Fatalf("Render(%s) = %v, want nil", e4Slug, err)
			}

			return string(raw)
		}

		t.Fatalf("no dashboard with slug %q", e4Slug)

		return ""
	}

	if render(legacyModel()) != render(strictModel()) {
		t.Error("E4 differs between two policies; the evidence tier must work for any configuration")
	}
}

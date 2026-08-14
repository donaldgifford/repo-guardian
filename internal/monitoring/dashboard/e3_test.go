package dashboard_test

import (
	"strings"
	"testing"
)

// e3Targets returns E3's panel queries.
func e3Targets(t *testing.T) []panelTarget {
	t.Helper()

	var out []panelTarget

	for _, tgt := range suiteTargets(t, strictModel()) {
		if tgt.Dashboard == "repo-guardian-system" {
			out = append(out, tgt)
		}
	}

	if len(out) == 0 {
		t.Fatal("the suite has no system dashboard")
	}

	return out
}

// verifiedForeignSeries are the non-repo_guardian series E3 may chart.
//
// Every name here was read off a live scrape of the bridge, not taken
// from the semconv specification, because the two disagree in ways that
// silently produce empty panels:
//
//   - otelpgx publishes the pgx-NATIVE `pgxpool_*` family, not
//     `db.client.connection.*`, and keys it on
//     `db_client_connection_pool_name`.
//   - redisotel publishes `db_client_connections_*` — plural
//     "connections", which is the older semconv shape — keyed on
//     `pool_name` with `db_system="redis"` (the protocol, not the
//     server; repo-guardian runs Valkey).
//   - otelhttp's histograms are `http_{server,client}_request_duration_seconds`.
//
// A panel built from the spec instead renders empty, and an empty panel
// on a system dashboard reads as a quiet, healthy service.
var verifiedForeignSeries = []string{
	"http_server_request_duration_seconds",
	"http_client_request_duration_seconds",
	"db_client_connections_usage",
	"pgxpool_acquired_connections",
	"pgxpool_idle_connections",
	"pgxpool_max_connections",
	"pgxpool_empty_acquire_wait_time_nanoseconds_total",
	"go_goroutines",
	"process_resident_memory_bytes",
}

// seriesPattern matches a metric name in an expression.
const seriesPrefixRepoGuardian = "repo_guardian_"

// TestE3_ChartsOnlyVerifiedForeignSeries pins the names against drift.
//
// The risk this guards is specific and has already been walked into
// once in this repo: someone tidies a query to match the semconv
// documentation, the series stops existing, and the panel goes quiet
// rather than red.
func TestE3_ChartsOnlyVerifiedForeignSeries(t *testing.T) {
	t.Parallel()

	for _, tgt := range e3Targets(t) {
		for _, word := range identifiers(tgt.Expr) {
			if strings.HasPrefix(word, seriesPrefixRepoGuardian) || isPromQLKeyword(word) {
				continue
			}

			if !containsString(verifiedForeignSeries, trimSuffixes(word)) {
				t.Errorf("%s charts %q, which is not in the verified-series list; "+
					"scrape it before adding it, do not take the name from the semconv spec",
					tgt.Panel, word)
			}
		}
	}
}

// TestE3_UsesTheNativePgxFamily is the specific case worth naming.
func TestE3_UsesTheNativePgxFamily(t *testing.T) {
	t.Parallel()

	var body strings.Builder

	for _, tgt := range e3Targets(t) {
		body.WriteString(tgt.Expr)
	}

	if !strings.Contains(body.String(), "pgxpool_") {
		t.Error("E3 charts no pgxpool_* series; otelpgx publishes that family and nothing else")
	}

	// The semconv singular-connection family is what a spec-reading
	// edit would reach for, and otelpgx does not publish it.
	if strings.Contains(body.String(), "db_client_connection_count") {
		t.Error("E3 charts db.client.connection.* series, which otelpgx does not publish")
	}
}

// TestE3_CarriesNoComplianceSeries pins the Finding I tier split.
//
// A business gauge next to a service counter cannot be read: when the
// picture looks wrong there is no way to tell which half is lying.
func TestE3_CarriesNoComplianceSeries(t *testing.T) {
	t.Parallel()

	for _, tgt := range e3Targets(t) {
		for _, business := range []string{
			"repo_guardian_repos_tracked",
			"repo_guardian_repos_actionable",
			"repo_guardian_files_missing_total",
			"repo_guardian_prs_created_total",
		} {
			if strings.Contains(tgt.Expr, business) {
				t.Errorf("E3 panel %q charts the business-tier series %s", tgt.Panel, business)
			}
		}
	}
}

// identifiers pulls METRIC NAMES out of a PromQL expression.
//
// A crude scanner rather than a parser, and it only has to be right
// about one thing: what is a series name and what is a label name.
// Label names appear in two places — inside `{...}` matchers and inside
// the parenthesised list after a grouping keyword (`by`, `on`,
// `group_left`, ...) — and both are skipped. Everything else that looks
// like an identifier and is not a function name is treated as a series.
func identifiers(expr string) []string {
	var (
		out      []string
		cur      strings.Builder
		inLabel  bool
		pending  bool // a grouping keyword was just read
		groupers int  // open parens of a grouping list
	)

	flush := func() {
		word := cur.String()
		cur.Reset()

		if word == "" || inLabel || groupers > 0 {
			return
		}

		if isGroupingKeyword(word) {
			pending = true

			return
		}

		out = append(out, word)
	}

	for _, r := range expr {
		switch {
		case r == '{':
			flush()

			inLabel = true
		case r == '}':
			inLabel = false
		case r == '(':
			flush()

			if pending {
				pending = false
				groupers++
			} else if groupers > 0 {
				groupers++
			}
		case r == ')':
			flush()

			if groupers > 0 {
				groupers--
			}
		case isIdentRune(r):
			cur.WriteRune(r)
		default:
			flush()
		}
	}

	flush()

	return out
}

// groupingKeywords introduce a parenthesised list of LABEL names.
var groupingKeywords = map[string]struct{}{
	"by": {}, "without": {}, "on": {}, "ignoring": {},
	"group_left": {}, "group_right": {},
}

func isGroupingKeyword(word string) bool {
	_, ok := groupingKeywords[word]

	return ok
}

func isIdentRune(r rune) bool {
	return r == '_' ||
		(r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9')
}

// promQLKeywords are the function names, aggregation operators,
// modifiers and duration literals that appear where a series name
// would.
var promQLKeywords = map[string]struct{}{
	"sum": {}, "max": {}, "min": {}, "avg": {}, "count": {}, "rate": {},
	"increase": {}, "histogram_quantile": {}, "clamp_min": {}, "clamp_max": {},
	"by": {}, "without": {}, "on": {}, "ignoring": {}, "group_left": {},
	"group_right": {}, "and": {}, "or": {}, "unless": {}, "offset": {},
	"scalar": {}, "vector": {}, "le": {}, "absent": {}, "topk": {}, "bottomk": {},
}

func isPromQLKeyword(word string) bool {
	if _, ok := promQLKeywords[word]; ok {
		return true
	}

	// Numbers, durations (5m, 1h) and the label names that survive the
	// crude scanner.
	return word == "" || (word[0] >= '0' && word[0] <= '9')
}

// trimSuffixes strips the exposition suffixes Prometheus appends to a
// histogram family, so a bucket query matches its base name.
func trimSuffixes(name string) string {
	for _, suffix := range []string{"_bucket", "_count", "_sum"} {
		if trimmed, ok := strings.CutSuffix(name, suffix); ok {
			return trimmed
		}
	}

	return name
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}

	return false
}

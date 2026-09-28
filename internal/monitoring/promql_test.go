package monitoring_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/monitoring"
	"github.com/donaldgifford/repo-guardian/internal/monitoring/alert"
)

const sel = `namespace="rg"`

func TestScopePromQL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, in, want string
	}{
		{"bare metric", `up`, `up{namespace="rg"}`},
		{"existing matchers", `m{a="b"}`, `m{namespace="rg", a="b"}`},
		{"empty braces", `m{}`, `m{namespace="rg"}`},
		{"range", `rate(m[5m])`, `rate(m{namespace="rg"}[5m])`},
		{"grafana range variable", `rate(m[$__rate_interval])`, `rate(m{namespace="rg"}[$__rate_interval])`},
		{
			"aggregation with by before the body",
			`sum by (le, code) (rate(m_bucket[5m]))`,
			`sum by (le, code) (rate(m_bucket{namespace="rg"}[5m]))`,
		},
		{
			"aggregation with by after the body",
			`sum(rate(m[5m])) by (le)`,
			`sum(rate(m{namespace="rg"}[5m])) by (le)`,
		},
		{
			"vector matching",
			"a\n  * on (installation_id) group_left(org) b",
			"a{namespace=\"rg\"}\n  * on (installation_id) group_left(org) b{namespace=\"rg\"}",
		},
		{"empty on", `a and on() (b > 0)`, `a{namespace="rg"} and on() (b{namespace="rg"} > 0)`},
		{
			"unless and offset",
			`(m unless m offset 1h) > 0`,
			`(m{namespace="rg"} unless m{namespace="rg"} offset 1h) > 0`,
		},
		{
			"string holding a brace and a metric-like word",
			`m{reason="a}b or c"}`,
			`m{namespace="rg", reason="a}b or c"}`,
		},
		{"quantile literal", `histogram_quantile(0.99, x)`, `histogram_quantile(0.99, x{namespace="rg"})`},
		{"arithmetic", `sum(rate(a[1h])) * 3600`, `sum(rate(a{namespace="rg"}[1h])) * 3600`},
		{"recording-rule colons", `job:m:rate5m`, `job:m:rate5m{namespace="rg"}`},
		{"template variable in a matcher", `m{org="$org"}`, `m{namespace="rg", org="$org"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := monitoring.ScopePromQL(tt.in, sel); got != tt.want {
				t.Errorf("ScopePromQL(%q)\n got %q\nwant %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestScopePromQL_EmptySelectorIsIdentity(t *testing.T) {
	t.Parallel()

	for _, s := range alert.Catalogue() {
		if got := monitoring.ScopePromQL(s.Expr, ""); got != s.Expr {
			t.Errorf("%s: ScopePromQL(expr, \"\") changed the expression", s.Name)
		}
	}
}

func TestValidateMatchers(t *testing.T) {
	t.Parallel()

	for _, ok := range []string{
		"",
		`namespace="repo-guardian"`,
		`namespace="a", job=~"b|c"`,
		`a!="x",b!~"y"`,
		`a="with \"escaped\" quotes"`,
	} {
		if err := monitoring.ValidateMatchers(ok); err != nil {
			t.Errorf("ValidateMatchers(%q) = %v, want nil", ok, err)
		}
	}

	for _, bad := range []string{
		`{namespace="a"}`,
		`namespace=a`,
		`namespace='a'`,
		`namespace="a",`,
		`namespace="a"} or vector(1) or {x="y"`,
		`1abc="x"`,
	} {
		if err := monitoring.ValidateMatchers(bad); err == nil {
			t.Errorf("ValidateMatchers(%q) = nil, want an error", bad)
		}
	}
}

// catalogueSeries matches every metric the alert catalogue reads.
// Independent of the scoper, for the reason given on the dashboard
// test's seriesName.
var catalogueSeries = regexp.MustCompile(`\b(repo_guardian_[a-z_]+|temporal_[a-z_]+)\b`)

// TestScopePromQL_ScopesTheWholeCatalogue runs the scoper over every
// alert, including the mechanism-excluded ones, and has promtool parse
// the result.
func TestScopePromQL_ScopesTheWholeCatalogue(t *testing.T) {
	t.Parallel()

	const two = `namespace="rg", job!="x"`

	specs := alert.Catalogue()
	if len(specs) == 0 {
		t.Fatal("the catalogue is empty; the check would pass vacuously")
	}

	for i := range specs {
		specs[i].Expr = monitoring.ScopePromQL(specs[i].Expr, two)

		locs := catalogueSeries.FindAllStringIndex(specs[i].Expr, -1)
		if len(locs) == 0 {
			t.Errorf("%s reads no recognised series; widen catalogueSeries", specs[i].Name)
		}

		for _, loc := range locs {
			if !strings.HasPrefix(specs[i].Expr[loc[1]:], "{"+two) {
				t.Errorf("%s: series %s is not scoped:\n%s", specs[i].Name, specs[i].Expr[loc[0]:loc[1]], specs[i].Expr)
			}
		}
	}

	bin, err := exec.LookPath("promtool")
	if err != nil {
		t.Skip("promtool not on PATH; mise supplies it (see mise.toml)")
	}

	raw, err := alert.RenderGroups(alert.Groups(specs))
	if err != nil {
		t.Fatalf("RenderGroups() = %v, want nil", err)
	}

	path := filepath.Join(t.TempDir(), "alerts.yaml")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("WriteFile() = %v, want nil", err)
	}

	// bin comes from LookPath and path is a file under t.TempDir().
	if out, err := exec.CommandContext(t.Context(), bin, "check", "rules", path).CombinedOutput(); err != nil {
		t.Fatalf("promtool rejected the scoped rules: %v\n%s\n---\n%s", err, out, raw)
	}
}

package alert_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/monitoring/alert"
)

// TestCatalogue_PromtoolAcceptsEveryExpression parses the whole
// catalogue as PromQL.
//
// Nothing else in Go-land can tell a valid expression from an invalid
// one: the catalogue is a table of strings, so a missing paren or a
// mistyped function name compiles, renders, round-trips through YAML and
// passes every other test in this package. It surfaces when Prometheus
// loads the manifest — which, for a generated tier, is on the operator's
// cluster rather than in CI.
//
// Renders Catalogue() rather than Generate(), deliberately: an
// expression excluded by a mechanism is still an expression that ships
// to somebody, and dry-run-suppressed alerts would otherwise never be
// checked at all.
func TestCatalogue_PromtoolAcceptsEveryExpression(t *testing.T) {
	t.Parallel()

	bin, err := exec.LookPath("promtool")
	if err != nil {
		t.Skip("promtool not on PATH; mise supplies it (see mise.toml)")
	}

	specs := alert.Catalogue()

	// promtool exits 0 on "0 rules found", so an empty render passes
	// vacuously — the same trap the lint-alerts-chart make target
	// carries a guard for.
	if len(specs) == 0 {
		t.Fatal("the catalogue is empty; promtool would accept it vacuously")
	}

	raw, err := alert.RenderGroups(alert.Groups(specs))
	if err != nil {
		t.Fatalf("RenderGroups() = %v, want nil", err)
	}

	path := filepath.Join(t.TempDir(), "alerts.yaml")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("WriteFile() = %v, want nil", err)
	}

	// bin comes from LookPath and path is a file under t.TempDir(); no
	// part of the command line is caller-supplied.
	out, err := exec.CommandContext(t.Context(), bin, "check", "rules", path).CombinedOutput()
	if err != nil {
		t.Fatalf("promtool rejected the generated rules: %v\n%s\n---\n%s", err, out, raw)
	}
}

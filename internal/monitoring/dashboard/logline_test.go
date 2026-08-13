package dashboard

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLogLines_AreStillEmittedByTheBinary is the only thing keeping E4
// honest.
//
// Every LogQL matcher on E4 is a string literal pointed at a log line
// somewhere in internal/. Nothing in the compiler connects the two, and
// the failure when they drift is the worst kind available here: a
// matcher that no longer matches returns no rows, and no rows renders
// exactly like "this never happens". An operator would read a blank
// "repositories with an unparseable catalog-info" panel as good news.
//
// So this walks the source and insists each literal is still emitted.
// It lives in package dashboard rather than dashboard_test so it reads
// the same constants the panels do — a duplicated list here could drift
// from the panels and pass while they broke.
//
// It is deliberately a substring search over source text rather than
// anything cleverer: the point is to fail loudly on a reword, and a
// reword is exactly a source-text change.
func TestLogLines_AreStillEmittedByTheBinary(t *testing.T) {
	t.Parallel()

	// The internal/ tree, from this package's directory.
	const root = "../.."

	sources := goSources(t, root)

	for _, line := range []struct{ name, text string }{
		{"logCatalogParseFailed", logCatalogParseFailed},
		{"logRepositoryParked", logRepositoryParked},
		{"logAttemptCapDropped", logAttemptCapDropped},
		{"logStoreWriteback", logStoreWriteback},
		{"logRuleStateWriteback", logRuleStateWriteback},
		{"logDeferringJob", logDeferringJob},
		{"logSweepComplete", logSweepComplete},
		{"logRejectedIP", logRejectedIP},
		{"logNoIP", logNoIP},
		{"logInvalidPayload", logInvalidPayload},
		{"logEnqueueFailed", logEnqueueFailed},
	} {
		if !emitted(sources, line.text) {
			t.Errorf("%s = %q, but nothing under internal/ logs it any more; "+
				"the E4 panel matching it now renders empty, which reads as 'this never happens'",
				line.name, line.text)
		}
	}
}

// selfPackage is the tree that DECLARES the literals, as opposed to the
// trees that emit them.
//
// Excluding it is the entire difference between this test working and
// passing vacuously: internal/monitoring/dashboard is itself under
// internal/, so without this every literal would match its own const
// declaration and the walk would confirm nothing at all.
const selfPackage = "monitoring"

// goSources reads every non-test Go file under root, excluding the
// package under test.
//
// Non-test only: a literal that survives solely in a test file is a
// literal the running binary never emits, which is the same empty panel
// with an extra step.
func goSources(t *testing.T, root string) []string {
	t.Helper()

	var out []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			if d.Name() == selfPackage {
				return fs.SkipDir
			}

			return nil
		}

		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		out = append(out, string(body))

		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}

	if len(out) == 0 {
		t.Fatalf("no Go sources under %s; this test would pass vacuously", root)
	}

	return out
}

func emitted(sources []string, text string) bool {
	for _, src := range sources {
		if strings.Contains(src, text) {
			return true
		}
	}

	return false
}

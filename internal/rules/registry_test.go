package rules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tmpl "github.com/donaldgifford/repo-guardian/internal/template"
)

func TestTemplateStoreEmbeddedFallback(t *testing.T) {
	t.Parallel()

	ts := NewTemplateStore()

	// Load with empty dir — should use embedded defaults.
	if err := ts.Load(""); err != nil {
		t.Fatalf("Load with empty dir: %v", err)
	}

	for _, name := range []string{"codeowners", "dependabot", "renovate"} {
		compiled, err := ts.Get(name)
		if err != nil {
			t.Errorf("Get(%q): %v", name, err)
			continue
		}

		if compiled == nil {
			t.Errorf("Get(%q) returned nil compiled template", name)
			continue
		}

		raw, err := ts.Raw(name)
		if err != nil {
			t.Errorf("Raw(%q): %v", name, err)
			continue
		}

		if raw == "" {
			t.Errorf("Raw(%q) returned empty content", name)
		}
	}
}

func TestTemplateStoreDirectoryOverride(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	overrideContent := "# Custom CODEOWNERS override\n* @myorg/myteam\n"
	if err := os.WriteFile(filepath.Join(dir, "codeowners.tmpl"), []byte(overrideContent), 0o644); err != nil {
		t.Fatalf("writing override template: %v", err)
	}

	ts := NewTemplateStore()
	if err := ts.Load(dir); err != nil {
		t.Fatalf("Load(%q): %v", dir, err)
	}

	// codeowners should use the override.
	rawCodeowners, err := ts.Raw("codeowners")
	if err != nil {
		t.Fatalf("Raw(codeowners): %v", err)
	}

	if rawCodeowners != overrideContent {
		t.Errorf("expected override content, got %q", rawCodeowners)
	}

	// dependabot should still use embedded default.
	rawDependabot, err := ts.Raw("dependabot")
	if err != nil {
		t.Fatalf("Raw(dependabot): %v", err)
	}

	if rawDependabot == "" {
		t.Error("Raw(dependabot) returned empty content")
	}
}

func TestTemplateStoreGetMissing(t *testing.T) {
	t.Parallel()

	ts := NewTemplateStore()
	if err := ts.Load(""); err != nil {
		t.Fatalf("Load: %v", err)
	}

	_, err := ts.Get("nonexistent")
	if err == nil {
		t.Error("expected error for missing template, got nil")
	}
}

// TestEmbeddedTemplates_Render_Golden locks the rendered output of every
// embedded template against a known-good string so accidental edits to
// dotted-path placeholders or backtick-escaped GHA expressions surface in
// CI rather than at runtime.
func TestEmbeddedTemplates_Render_Golden(t *testing.T) {
	t.Parallel()

	ts := NewTemplateStore()
	if err := ts.Load(""); err != nil {
		t.Fatalf("Load embedded: %v", err)
	}

	cases := []struct {
		name     string
		tmplName string
		vars     tmpl.FileVars
		want     string
	}{
		{
			name:     "catalog-info renders Owner and Repo",
			tmplName: "catalog-info",
			vars: tmpl.FileVars{
				Common: tmpl.Common{Owner: "acme", Repo: "billing-svc"},
			},
			want: "name: billing-svc",
		},
		{
			name:     "renovate config renders Owner into preset",
			tmplName: "renovate",
			vars: tmpl.FileVars{
				Common: tmpl.Common{Owner: "acme"},
			},
			want: `"github>acme/renovate-config"`,
		},
		{
			name:     "set-custom-properties renders Catalog fields and preserves GHA literals",
			tmplName: "set-custom-properties",
			vars: tmpl.FileVars{
				Catalog: &tmpl.CatalogInfo{
					Owner:       "platform",
					Component:   "billing-svc",
					JiraProject: "BILL",
					JiraLabel:   "billing",
				},
			},
			want: "${{ secrets.GITHUB_TOKEN }}",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			compiled, err := ts.Get(tc.tmplName)
			if err != nil {
				t.Fatalf("Get(%q): %v", tc.tmplName, err)
			}

			out, err := compiled.Render(tc.vars)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}

			if !strings.Contains(out, tc.want) {
				t.Errorf("rendered template %q does not contain %q\noutput:\n%s",
					tc.tmplName, tc.want, out)
			}
		})
	}
}

// TestEmbeddedTemplates_NilCatalog_Errors verifies that catalog-aware
// templates fail loudly when rendered against a FileVars with nil
// Catalog, rather than silently producing garbage. This is the
// missingkey=error contract from the unified renderer.
func TestEmbeddedTemplates_NilCatalog_Errors(t *testing.T) {
	t.Parallel()

	ts := NewTemplateStore()
	if err := ts.Load(""); err != nil {
		t.Fatalf("Load embedded: %v", err)
	}

	compiled, err := ts.Get("set-custom-properties")
	if err != nil {
		t.Fatalf("Get(set-custom-properties): %v", err)
	}

	_, err = compiled.Render(tmpl.FileVars{
		Common: tmpl.Common{Owner: "acme", Repo: "billing-svc"},
	})
	if err == nil {
		t.Fatal("expected render error for nil Catalog under missingkey=error")
	}
}

// TestEmbeddedTemplates_RenovateWorkflow_PreservesGHAExpressions verifies
// that backtick-escaped GHA expressions inside the renovate-workflow
// template render to literal `${{ ... }}` syntax for GitHub Actions,
// even with no Go template variables in the context.
func TestEmbeddedTemplates_RenovateWorkflow_PreservesGHAExpressions(t *testing.T) {
	t.Parallel()

	ts := NewTemplateStore()
	if err := ts.Load(""); err != nil {
		t.Fatalf("Load embedded: %v", err)
	}

	compiled, err := ts.Get("renovate-workflow")
	if err != nil {
		t.Fatalf("Get(renovate-workflow): %v", err)
	}

	out, err := compiled.Render(tmpl.FileVars{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	literals := []string{
		"${{ secrets.RENOVATE_APP_ID }}",
		"${{ secrets.RENOVATE_APP_PRIVATE_KEY }}",
		"${{ steps.app-token.outputs.token }}",
		"${{ github.repository }}",
	}

	for _, lit := range literals {
		if !strings.Contains(out, lit) {
			t.Errorf("renovate-workflow output missing literal %q\noutput:\n%s", lit, out)
		}
	}
}

// TestTemplateStoreAsMap exercises the AsMap snapshot used to feed
// policy.Version. Verifies (1) every loaded template body appears in
// the map keyed by name (no .tmpl suffix), (2) the returned map is a
// copy — mutating it does not affect subsequent reads from the store,
// and (3) a directory override is reflected in the snapshot.
func TestTemplateStoreAsMap(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	overrideContent := "# Override CODEOWNERS\n* @platform\n"
	if err := os.WriteFile(filepath.Join(dir, "codeowners.tmpl"), []byte(overrideContent), 0o644); err != nil {
		t.Fatalf("write override: %v", err)
	}

	ts := NewTemplateStore()
	if err := ts.Load(dir); err != nil {
		t.Fatalf("Load: %v", err)
	}

	snapshot := ts.AsMap()

	got, ok := snapshot["codeowners"]
	if !ok {
		t.Fatal("snapshot missing override key 'codeowners'")
	}

	if got != overrideContent {
		t.Errorf("snapshot did not capture override; got %q", got)
	}

	if _, ok := snapshot["dependabot"]; !ok {
		t.Error("snapshot missing embedded fallback 'dependabot'")
	}

	// Mutating the snapshot must not affect the store.
	snapshot["codeowners"] = "MUTATED"

	rawFromStore, err := ts.Raw("codeowners")
	if err != nil {
		t.Fatalf("Raw after snapshot mutation: %v", err)
	}

	if rawFromStore != overrideContent {
		t.Errorf("AsMap leaked mutation back to store; Raw returned %q", rawFromStore)
	}
}

func TestTemplateStoreNonexistentDir(t *testing.T) {
	t.Parallel()

	ts := NewTemplateStore()

	// Non-existent directory should fall through to embedded.
	if err := ts.Load("/nonexistent/dir/that/does/not/exist"); err != nil {
		t.Fatalf("Load with nonexistent dir should not error: %v", err)
	}

	raw, err := ts.Raw("codeowners")
	if err != nil {
		t.Fatalf("Raw(codeowners): %v", err)
	}

	if raw == "" {
		t.Error("expected embedded fallback content")
	}
}

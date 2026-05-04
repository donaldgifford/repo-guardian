// Package rules defines the FileRule registry and template store for
// repo-guardian's file compliance checks.
package rules

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tmpl "github.com/donaldgifford/repo-guardian/internal/template"
)

//go:embed templates/*.tmpl
var embeddedTemplates embed.FS

// FileRule defines a required file and how to detect/create it.
type FileRule struct {
	// Name is a human-readable name for logging and PR descriptions.
	Name string

	// Paths to check in priority order. If ANY path exists, the rule is satisfied.
	Paths []string

	// PRSearchTerms are strings to search for in open PR titles/branches
	// to determine if someone is already working on adding this file.
	PRSearchTerms []string

	// DefaultTemplateName is the key into the template store
	// for the default file content.
	DefaultTemplateName string

	// TargetPath is where the default file will be created if missing.
	TargetPath string

	// Enabled allows rules to be toggled without removal.
	Enabled bool
}

// DefaultRules defines the initial set of file compliance rules.
// CODEOWNERS and Dependabot are enabled; Renovate is defined but disabled.
var DefaultRules = []FileRule{
	{
		Name:                "CODEOWNERS",
		Paths:               []string{"CODEOWNERS", ".github/CODEOWNERS", "docs/CODEOWNERS"},
		PRSearchTerms:       []string{"codeowners", "CODEOWNERS"},
		DefaultTemplateName: "codeowners",
		TargetPath:          ".github/CODEOWNERS",
		Enabled:             true,
	},
	{
		Name:                "Dependabot",
		Paths:               []string{".github/dependabot.yml", ".github/dependabot.yaml"},
		PRSearchTerms:       []string{"dependabot"},
		DefaultTemplateName: "dependabot",
		TargetPath:          ".github/dependabot.yml",
		Enabled:             true,
	},
	{
		Name: "Renovate",
		Paths: []string{
			"renovate.json",
			"renovate.json5",
			".renovaterc",
			".renovaterc.json",
			".github/renovate.json",
			".github/renovate.json5",
		},
		PRSearchTerms:       []string{"renovate"},
		DefaultTemplateName: "renovate",
		TargetPath:          "renovate.json",
		Enabled:             false,
	},
}

// Registry holds a set of FileRules and provides query methods.
type Registry struct {
	rules []FileRule
}

// NewRegistry creates a Registry from the given rules.
func NewRegistry(rules []FileRule) *Registry {
	return &Registry{rules: rules}
}

// EnabledRules returns only the rules where Enabled is true.
func (r *Registry) EnabledRules() []FileRule {
	enabled := make([]FileRule, 0, len(r.rules))

	for _, rule := range r.rules {
		if rule.Enabled {
			enabled = append(enabled, rule)
		}
	}

	return enabled
}

// RuleByName returns the rule with the given name and true,
// or a zero FileRule and false if not found.
func (r *Registry) RuleByName(name string) (FileRule, bool) {
	for _, rule := range r.rules {
		if strings.EqualFold(rule.Name, name) {
			return rule, true
		}
	}

	return FileRule{}, false
}

// AllRules returns all rules in the registry.
func (r *Registry) AllRules() []FileRule {
	result := make([]FileRule, len(r.rules))
	copy(result, r.rules)

	return result
}

// TemplateStore loads and serves file templates compiled with the
// internal/template renderer. It tracks both the raw template body (for
// byte-exact CheckExact comparisons) and the parsed *template.Compiled
// (for rendering with variable contexts). Templates are loaded from a
// directory if provided, with embedded defaults filling in any
// remaining names.
type TemplateStore struct {
	renderer *tmpl.Renderer
	raw      map[string]string
	compiled map[string]*tmpl.Compiled
}

// NewTemplateStore creates an empty TemplateStore wired to a fresh
// Renderer. Callers that already hold a Renderer should use
// NewTemplateStoreWithRenderer to share it.
func NewTemplateStore() *TemplateStore {
	return NewTemplateStoreWithRenderer(tmpl.NewRenderer())
}

// NewTemplateStoreWithRenderer creates an empty TemplateStore that
// shares the supplied Renderer. Useful when the caller already owns a
// Renderer and wants the same FuncMap applied across all parsed
// templates in the process.
func NewTemplateStoreWithRenderer(r *tmpl.Renderer) *TemplateStore {
	return &TemplateStore{
		renderer: r,
		raw:      make(map[string]string),
		compiled: make(map[string]*tmpl.Compiled),
	}
}

// Load reads templates from the given directory (if non-empty and
// exists), then fills in any missing templates from the embedded
// defaults. Each loaded template is parsed into a *template.Compiled
// at load time so render-hot-path callers don't pay parse cost; parse
// errors fail the load with the template name in the error message.
func (ts *TemplateStore) Load(dir string) error {
	if dir != "" {
		if err := ts.loadFromDir(dir); err != nil {
			if !os.IsNotExist(err) {
				return fmt.Errorf("reading template directory %s: %w", dir, err)
			}
		}
	}

	return ts.loadEmbeddedDefaults()
}

// Get returns the parsed template for the given name. Callers render
// against any context type via Compiled.Render. Returns an error when
// the name is not registered.
func (ts *TemplateStore) Get(name string) (*tmpl.Compiled, error) {
	c, ok := ts.compiled[name]
	if !ok {
		return nil, fmt.Errorf("template %q not found", name)
	}

	return c, nil
}

// Raw returns the unrendered template body for the given name. Useful
// for byte-exact CheckExact comparisons that must operate on the
// authored template text rather than its rendered output. Render-time
// callers should prefer Get.
func (ts *TemplateStore) Raw(name string) (string, error) {
	body, ok := ts.raw[name]
	if !ok {
		return "", fmt.Errorf("template %q not found", name)
	}

	return body, nil
}

// store associates name with content, parsing the body via the shared
// Renderer. It applies the legacy-syntax translator first so embedded
// templates that still use OWNER_VALUE-style placeholders compile into
// equivalent dotted-path Go templates during the Phase 2 transition.
//
// Templates that contain GitHub Actions expressions (`${{ secrets.X }}`)
// are stored as raw passthrough since their `{{` `}}` markers are GHA
// syntax, not Go template syntax, and would fail to parse otherwise.
func (ts *TemplateStore) store(name, content string) error {
	body := translateLegacyPlaceholders(content)

	if containsGHAExpression(body) {
		ts.raw[name] = content
		ts.compiled[name] = ts.renderer.Raw(name, body)

		return nil
	}

	c, err := ts.renderer.Parse(name, body)
	if err != nil {
		return fmt.Errorf("compiling template %q: %w", name, err)
	}

	ts.raw[name] = content
	ts.compiled[name] = c

	return nil
}

// containsGHAExpression reports whether body contains a GitHub Actions
// expression marker (`${{`). Such markers collide with Go template
// `{{` syntax and require the raw-passthrough compile path.
func containsGHAExpression(body string) bool {
	return strings.Contains(body, "${{")
}

func (ts *TemplateStore) loadFromDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".tmpl") {
			continue
		}

		path := filepath.Clean(filepath.Join(dir, entry.Name()))

		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading template %s: %w", entry.Name(), err)
		}

		name := strings.TrimSuffix(entry.Name(), ".tmpl")
		if err := ts.store(name, string(content)); err != nil {
			return err
		}
	}

	return nil
}

func (ts *TemplateStore) loadEmbeddedDefaults() error {
	entries, err := embeddedTemplates.ReadDir("templates")
	if err != nil {
		return fmt.Errorf("reading embedded templates: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".tmpl") {
			continue
		}

		name := strings.TrimSuffix(entry.Name(), ".tmpl")

		// Don't override directory-loaded templates.
		if _, exists := ts.compiled[name]; exists {
			continue
		}

		content, err := embeddedTemplates.ReadFile("templates/" + entry.Name())
		if err != nil {
			return fmt.Errorf("reading embedded template %s: %w", entry.Name(), err)
		}

		if err := ts.store(name, string(content)); err != nil {
			return err
		}
	}

	return nil
}

// translateLegacyPlaceholders rewrites the legacy OWNER_VALUE-style
// placeholders used by the pre-DESIGN-0013 reconciler templates into
// dotted-path Go template syntax so they compile with the unified
// renderer. This is a Phase 2 transitional shim — Phase 3 deletes this
// function and rewrites the embedded templates directly.
//
// CLAUDE: delete this function and the legacyReplacements list when
// Phase 3 of IMPL-0012 lands. The embedded templates will already use
// dotted-path syntax at that point.
func translateLegacyPlaceholders(content string) string {
	if strings.Contains(content, "{{") {
		return content
	}

	for _, r := range legacyReplacements {
		content = strings.ReplaceAll(content, r.old, r.new)
	}

	return content
}

// legacyReplacements maps the pre-DESIGN-0013 placeholder syntax onto
// the dotted-path Go template syntax. Used only by the Phase 2
// transitional translator above.
var legacyReplacements = []struct {
	old string
	new string
}{
	{"OWNER_VALUE", "{{ .Catalog.Owner }}"},
	{"COMPONENT_VALUE", "{{ .Catalog.Component }}"},
	{"JIRA_PROJECT_VALUE", "{{ .Catalog.JiraProject }}"},
	{"JIRA_LABEL_VALUE", "{{ .Catalog.JiraLabel }}"},
	{"REPO_NAME", "{{ .Repo }}"},
	{"ORG_NAME", "{{ .Owner }}"},
}

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
// Renderer. Templates may contain Go template actions; templates that
// embed GitHub Actions `${{ ... }}` expressions must escape them inside
// backtick-raw-string Go template actions so the parser sees them as
// literal text rather than malformed Go template syntax.
func (ts *TemplateStore) store(name, content string) error {
	c, err := ts.renderer.Parse(name, content)
	if err != nil {
		return fmt.Errorf("compiling template %q: %w", name, err)
	}

	ts.raw[name] = content
	ts.compiled[name] = c

	return nil
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

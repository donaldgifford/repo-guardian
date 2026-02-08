// Package rules defines the FileRule registry and template store for
// repo-guardian's file compliance checks.
package rules

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	var enabled []FileRule

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

// TemplateStore loads and serves file templates, using embedded
// defaults as fallbacks when a directory override is not available.
type TemplateStore struct {
	templates map[string]string
}

// NewTemplateStore creates an empty TemplateStore.
func NewTemplateStore() *TemplateStore {
	return &TemplateStore{
		templates: make(map[string]string),
	}
}

// Load reads templates from the given directory (if non-empty and exists),
// then fills in any missing templates from the embedded defaults.
func (ts *TemplateStore) Load(dir string) error {
	// Load from directory if provided.
	if dir != "" {
		if err := ts.loadFromDir(dir); err != nil {
			// Directory doesn't exist or can't be read; fall through to embedded.
			if !os.IsNotExist(err) {
				return fmt.Errorf("reading template directory %s: %w", dir, err)
			}
		}
	}

	// Fill in missing templates from embedded defaults.
	return ts.loadEmbeddedDefaults()
}

// Get returns the template content for the given name.
func (ts *TemplateStore) Get(name string) (string, error) {
	content, ok := ts.templates[name]
	if !ok {
		return "", fmt.Errorf("template %q not found", name)
	}

	return content, nil
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
		ts.templates[name] = string(content)
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
		if _, exists := ts.templates[name]; exists {
			continue
		}

		content, err := embeddedTemplates.ReadFile("templates/" + entry.Name())
		if err != nil {
			return fmt.Errorf("reading embedded template %s: %w", entry.Name(), err)
		}

		ts.templates[name] = string(content)
	}

	return nil
}

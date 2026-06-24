// Package rules defines the template store for repo-guardian's file
// compliance checks. Templates are loaded from an optional directory and
// fall back to the embedded defaults.
package rules

import (
	"embed"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"

	tmpl "github.com/donaldgifford/repo-guardian/internal/template"
)

//go:embed templates/*.tmpl
var embeddedTemplates embed.FS

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

// AsMap returns a copy of the raw template bodies keyed by template
// name (without the ".tmpl" suffix). The map is used as the
// templates-half input to policy.Version so an edit to a ConfigMap
// template entry produces a different policy hash and triggers
// re-enqueue of every repo on the next sweep. The returned map is a
// shallow copy; callers may mutate it without affecting the store.
func (ts *TemplateStore) AsMap() map[string]string {
	out := make(map[string]string, len(ts.raw))
	maps.Copy(out, ts.raw)

	return out
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

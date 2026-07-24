// Package template is the unified Go text/template-based renderer used by
// repo-guardian for file-template generation, PR titles, and PR bodies. A
// single Renderer instance compiles templates at policy load time and
// renders them at engine hot-path time against typed variable contexts
// defined in contexts.go.
//
// # Helpers
//
// The renderer ships a small curated helper set documented in helpers.go:
// env, default, join, lower, upper, title, propenv, yamlq. The full Sprig
// library is deliberately not included to keep the surface auditable.
//
// # Security posture for the env helper
//
// The env helper reads arbitrary process environment variables with no
// allow-list. The threat model assumes the operator who writes the policy
// HCL also provisions the binary's runtime environment; reading those env
// vars they themselves set is not privilege escalation. Operators should
// NOT reference secret env vars (such as GITHUB_PRIVATE_KEY or
// WEBHOOK_SECRET) inside template bodies that get rendered into PR text,
// because the rendered output is visible to PR reviewers. See
// docs/ADDING_RULES.md "Customizing PR text" for guidance.
//
// # Concurrency
//
// Renderer holds a template.FuncMap set once at construction and never
// mutated. Compiled wraps a fully parsed *text/template.Template that is
// safe for concurrent Execute calls. Callers may share a single Renderer
// across worker goroutines without locking.
package template

import (
	"errors"
	"fmt"
	"strings"
	texttemplate "text/template"
)

// ErrNilCompiled is returned by (*Compiled).Render when called on a nil
// receiver. Callers using the nil sentinel pattern (where a nil
// *Compiled means "no template configured at this scope") must check
// for nil before rendering; this error exists so misuse fails loudly
// rather than panicking.
var ErrNilCompiled = errors.New("template: Render called on nil *Compiled")

// Renderer compiles template strings into reusable Compiled values. A
// single Renderer is constructed at startup and shared by all callers;
// it carries the curated function map and parser options.
type Renderer struct {
	funcs texttemplate.FuncMap
}

// NewRenderer returns a Renderer wired with the curated helper set and
// the missingkey=error option so that references to nil-pointer fields
// (e.g. .Catalog.Owner when .Catalog is nil) fail loudly at render time
// rather than silently producing empty output.
func NewRenderer() *Renderer {
	return &Renderer{funcs: funcMap()}
}

// Parse compiles body into a Compiled template named by name. Parse
// errors are returned wrapped with the template name for caller
// readability. The returned *Compiled is safe for concurrent Render calls.
func (r *Renderer) Parse(name, body string) (*Compiled, error) {
	tpl, err := texttemplate.New(name).
		Funcs(r.funcs).
		Option("missingkey=error").
		Parse(body)
	if err != nil {
		return nil, fmt.Errorf("parse template %q: %w", name, err)
	}

	return &Compiled{name: name, tpl: tpl}, nil
}

// Compiled is a parsed template ready for repeated execution against
// any of the typed variable contexts in contexts.go. A nil *Compiled
// is the idiomatic sentinel for "no template configured at this scope"
// used by the PRTemplate inheritance chain in internal/policy.
type Compiled struct {
	name string
	tpl  *texttemplate.Template
}

// Name reports the name the template was registered under at Parse time.
// Useful for logs and error messages at the call site.
func (c *Compiled) Name() string {
	return c.name
}

// Render executes the compiled template against vars and returns the
// rendered string. vars must be one of the context types defined in
// contexts.go (or compatible struct). Render errors are wrapped with
// the template name; callers should treat a render error as a hard
// failure for the affected operation.
//
// A nil *Compiled receiver returns ErrNilCompiled; callers using the
// nil sentinel pattern must check for nil before rendering.
func (c *Compiled) Render(vars any) (string, error) {
	if c == nil {
		return "", ErrNilCompiled
	}

	var buf strings.Builder

	if err := c.tpl.Execute(&buf, vars); err != nil {
		return "", fmt.Errorf("render template %q: %w", c.name, err)
	}

	return buf.String(), nil
}

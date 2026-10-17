package template

// PRConfig is the HCL-decoded form of a `pr { }` block. It carries both
// PR-detection settings (SearchTerms — used to find an open PR for a
// rule) and PR-templating fields (Title, Body, Labels, Inherits — used
// to customize the rendered PR text). The same block type appears in
// three scopes:
//
//   - `defaults { pr { } }` — process-wide template defaults.
//   - `rule "file" { pr { } }` — per-rule template plus search_terms.
//   - `reconcile "<type>" { pr { } }` — per-reconciler template.
//
// Title and Body use *string so the loader can distinguish "absent"
// (nil) from explicit empty string (`title = ""`). Labels uses []string
// with a sidecar LabelsSet bool because HCL's []string decoder cannot
// distinguish `labels = []` (explicit empty) from absence (both yield
// nil under the manual element-iterator decode path).
//
// Inherits uses *bool: nil means "not declared" (defaults to true at
// resolve time); explicit false stops propagation from the parent
// scope; explicit true is the same as absence.
//
// CompiledTitle and CompiledBody are populated by the loader after HCL
// decode by parsing Title/Body strings via the package-level renderer.
// Engine hot-path callers consume these compiled forms directly via
// the resolved *PRTemplate returned from pr.go.
type PRConfig struct {
	SearchTerms []string `hcl:"search_terms,optional"`

	Title    *string  `hcl:"title,optional"`
	Body     *string  `hcl:"body,optional"`
	Labels   []string `hcl:"labels,optional"`
	Inherits *bool    `hcl:"inherits,optional"`

	// LabelsSet is true when the `labels` attribute was explicitly
	// present in HCL (including `labels = []`). The loader populates
	// this from the raw attribute map before discarding the body.
	LabelsSet bool `hcl:"-"`

	// CompiledTitle is the parsed Title template. Nil when Title is
	// nil. Populated by the loader; engine consumes via Resolve.
	CompiledTitle *Compiled `hcl:"-"`

	// CompiledBody is the parsed Body template. Nil when Body is nil.
	CompiledBody *Compiled `hcl:"-"`
}

// PRTemplate is the resolved, render-ready form of a `pr { }` block.
// It is produced by ResolveRulePR / ResolveReconcilerPR after merging
// HCL scopes (defaults → rule → reconciler) field by field. Each of
// Title, Body, Labels, and Inherits independently inherits from the
// parent scope when unset and Inherits=true; an explicit Inherits=false
// at any scope stops propagation from above and falls through directly
// to engine built-in values for fields the scope did not set.
type PRTemplate struct {
	Title    *Compiled
	Body     *Compiled
	Labels   []string
	Inherits bool

	// LabelsSet mirrors PRConfig.LabelsSet — true means Labels was
	// explicitly set at this scope (including the empty-list override
	// `labels = []`). Resolution honors LabelsSet to distinguish
	// "explicit empty override" from "absent, inherit from parent".
	LabelsSet bool
}

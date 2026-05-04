package template

// Common holds variable fields shared by every render context. It is
// embedded into FileVars and PRVars so templates can reference these
// fields uniformly regardless of context type.
type Common struct {
	// Owner is the GitHub org or user login that owns the repository.
	Owner string
	// Repo is the repository name without the owner prefix.
	Repo string
	// DefaultBranch is the repository's default branch (typically "main").
	DefaultBranch string
	// Date is an RFC3339 timestamp captured at render time.
	Date string
}

// FileVars is the variable context passed to file-template rendering
// (catalog-info.yaml, CODEOWNERS, custom-properties workflow, etc.). The
// zero value is a valid empty context, which is load-bearing for the
// strict-mode validator in strict.go.
type FileVars struct {
	Common
	// Rule identifies the rule the file is being rendered for.
	Rule Rule
	// Catalog is non-nil only for catalog-info-aware templates such as
	// set-custom-properties.tmpl. Templates that may receive a nil
	// Catalog must guard with `{{ if .Catalog }}...{{ end }}` since
	// dereferencing a nil pointer field fails under missingkey=error.
	Catalog *CatalogInfo
	// Org is a convenience alias for Owner used by templates that read
	// more naturally with an org-named field. Callers must populate
	// Org explicitly (typically `Org: c.Owner` at construction); there
	// is no automatic synchronization with Common.Owner.
	Org string
}

// PRVars is the variable context passed to PR title and PR body
// rendering. Single-rule PRs populate Rule and leave Rules nil; bundled
// multi-rule PRs leave Rule zero-valued and populate Rules. The zero
// value is a valid empty context.
type PRVars struct {
	Common
	// Rule is set for single-rule PRs and zero-valued for bundled PRs.
	Rule Rule
	// Rules lists every rule firing in a bundled PR. Nil for single-rule
	// PRs. Body templates iterate this slice to format the bundle.
	Rules []Rule
	// Files lists every file path included in this PR.
	Files []string
	// Reconciler is the reconciler name when the PR is opened by a
	// reconciler (e.g. "custom_properties"); empty string otherwise.
	Reconciler string
}

// Rule names a rule by its HCL label and the file path it targets.
type Rule struct {
	// Name is the HCL rule label (e.g. "codeowners").
	Name string
	// Target is the file path the rule operates on (e.g.
	// ".github/CODEOWNERS").
	Target string
}

// CatalogInfo carries the parsed catalog-info.yaml fields surfaced to
// templates that need them. All fields default to empty string when
// absent in the source catalog-info.yaml.
type CatalogInfo struct {
	// Owner is the spec.owner field from catalog-info.yaml.
	Owner string
	// Component is the metadata.name field from catalog-info.yaml.
	Component string
	// JiraProject is the backstage.io/jira-project-key annotation.
	JiraProject string
	// JiraLabel is the backstage.io/jira-component annotation.
	JiraLabel string
}

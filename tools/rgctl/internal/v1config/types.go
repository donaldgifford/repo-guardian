package v1config

// Document is what a v1 policy file declares.
type Document struct {
	// Path is the file as given to [Read].
	Path string `json:"path"`
	// Orgs is scope.orgs. It is empty in legacy mode.
	Orgs []string `json:"orgs"`
	// LegacyMode is true when the file has no top-level scope block, which
	// in v1 means every rule applies to every installed org.
	LegacyMode bool `json:"legacy_mode"`
	// Guardian is every known attribute of the guardian block, as written.
	Guardian []Attr `json:"guardian"`
	// Locals is every attribute of any locals block, as written.
	Locals []Attr `json:"locals"`
	// Ignore is the global ignore.repos patterns.
	Ignore []string `json:"ignore"`
	// Defaults is defaults.pr, or nil.
	Defaults *PRBlock `json:"defaults_pr,omitempty"`
	// Rules is every rule block of a known type, in file order.
	Rules []Rule `json:"rules"`
	// Unrecognised is every block or attribute the reader did not know.
	Unrecognised []Item `json:"unrecognised"`
}

// Rule is one rule "<type>" "<name>" block.
type Rule struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Line    int    `json:"line"`
	Enabled *bool  `json:"enabled,omitempty"`
	// Paths, Check, Template and Target are file-rule attributes.
	Paths    []string `json:"paths,omitempty"`
	Check    string   `json:"check,omitempty"`
	Template string   `json:"template,omitempty"`
	Target   string   `json:"target,omitempty"`
	// Scope is the rule's scope.orgs; nil when the rule has no scope block.
	Scope []string `json:"scope,omitempty"`
	// Ignore is the rule's ignore.repos patterns.
	Ignore []string `json:"ignore,omitempty"`
	// When is the when { rule_satisfied } gate.
	When *Attr `json:"when,omitempty"`
	// Assertions holds one entry per assertion block; Source joins the
	// block's attributes in file order.
	Assertions  []Attr       `json:"assertions,omitempty"`
	Reconcilers []Reconciler `json:"reconcilers,omitempty"`
	PR          *PRBlock     `json:"pr,omitempty"`
	// Attrs holds the known attributes that have no field above: a setting
	// rule's property, expected and remediate, a branch_protection rule's
	// branch and requirements, and any common attribute whose value is not a
	// literal.
	Attrs []Attr `json:"attrs,omitempty"`
}

// Reconciler is one reconcile "<type>" block.
type Reconciler struct {
	Type  string   `json:"type"`
	Line  int      `json:"line"`
	Attrs []Attr   `json:"attrs,omitempty"`
	PR    *PRBlock `json:"pr,omitempty"`
}

// PRBlock is a pr block: title and body as source text, labels, inherits,
// search_terms.
type PRBlock struct {
	Line  int    `json:"line"`
	Attrs []Attr `json:"attrs"`
}

// Attr is one attribute as written.
type Attr struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Line   int    `json:"line"`
}

// Item is a block or attribute the reader did not recognise.
type Item struct {
	// Kind is "block" or "attribute".
	Kind string `json:"kind"`
	// Name locates the item, e.g. `widget` or `rule "file" "x" > colour`.
	Name string `json:"name"`
	// Source is the attribute as written, or the block's header.
	Source string `json:"source"`
	Line   int    `json:"line"`
}

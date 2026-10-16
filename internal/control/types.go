package control

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/template"
)

// ValidSlug is the grammar of a control slug and of a rule id
// (DESIGN-0033 AR-0033-01).
var ValidSlug = regexp.MustCompile(`^[a-z][a-z0-9]*([_-][a-z0-9]+)*$`)

// Type is a control type: Go code that knows one kind of resource. It
// validates a catalogue definition and builds a Control from it. One Type
// serves every catalogue version of its control; a version bump is
// catalogue data, not a new Type.
type Type interface {
	// Name is the type's name, such as "codeowners".
	Name() string
	// Semantics is bumped when the evaluator's meaning changes; it is
	// part of the revision digest (D19).
	Semantics() int
	// RuleKinds says what rules a definition may declare, with their
	// parameters.
	RuleKinds() []RuleKindSpec
	// Build validates the definition's parameters at policy load and
	// returns the runnable control.
	Build(def Definition, env BuildEnv) (Control, error)
}

// Control is one catalogue definition, ready to run.
type Control interface {
	// ID is the definition's slug and version.
	ID() ID
	// Owns is what the control may write; every ChangeSet is validated
	// against it (DESIGN-0030 ownership).
	Owns() []Resource
	// Reads is what invalidates a result: files read without owning,
	// tree prefixes and every alternate location of a file control.
	Reads() []Resource
	// Rules are the declared rules, in display order.
	Rules() []Rule

	// Evaluate reads, never writes. The reader is pinned to one commit
	// of the default branch, or of a remediation PR's head.
	Evaluate(ctx context.Context, in EvalInput) (Evaluation, error)

	// Remediate returns the change that makes the failing, remediable
	// rules pass where the observed state allows it, computed against
	// the same pinned state, and names the rules it could not fix. It
	// writes nothing.
	Remediate(ctx context.Context, in RemediationInput) (ChangeSet, error)
}

// Definition is one catalogue entry after HCL decode and before Build:
// what the loader hands to Type.Build. It is data; the Control that Build
// returns is the behaviour.
type Definition struct {
	ID    ID
	Title string
	// Type is the Type.Name() that builds it.
	Type string
	// Rules are validated by Type.Build; at least one.
	Rules []Rule
	// Template is a template store name; it must pass every remediable
	// rule.
	Template string
	// PR is the pr {} block (DESIGN-0030); display-only, outside the
	// revision.
	PR template.PRConfig
	// Apply is "recommend" (default), "direct" or "workflow";
	// API-remediated types only (D15).
	Apply string
	// Params are type-level parameters: dependency_updates.tool,
	// file.path, ...
	Params map[string]any
}

// BuildEnv is what Type.Build may depend on. It is passed explicitly so
// that no type reaches a package-level global for its templates (D17).
type BuildEnv struct {
	Templates TemplateSource
	Renderer  *template.Renderer
}

// TemplateSource is the part of the template store Build needs.
type TemplateSource interface {
	Lookup(name string) (body string, ok bool)
}

// ID names one catalogue definition. Version is the integer the catalogue
// declares; policies reference it as "codeowners@2". Results bind to the
// revision digest, not to this pair (D19).
type ID struct {
	Slug    string
	Version int
}

// String renders the ID as slug@version.
func (id ID) String() string {
	return id.Slug + "@" + strconv.Itoa(id.Version)
}

// ParseID parses "slug@N": a slug in the ValidSlug grammar and a positive
// integer version.
func ParseID(s string) (ID, error) {
	slug, v, ok := strings.Cut(s, "@")
	if !ok {
		return ID{}, fmt.Errorf("control id %q: want slug@version", s)
	}

	if !ValidSlug.MatchString(slug) {
		return ID{}, fmt.Errorf("control id %q: slug must match %s", s, ValidSlug)
	}

	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || strconv.Itoa(n) != v {
		return ID{}, fmt.Errorf("control id %q: version must be a positive integer", s)
	}

	return ID{Slug: slug, Version: n}, nil
}

// Resource kinds.
const (
	KindFile     = "file"
	KindSetting  = "setting"
	KindRuleset  = "ruleset"
	KindLabel    = "label"
	KindProperty = "property"
	KindTree     = "tree"
)

// Resource is something exactly one active control may own, or that a
// control reads (DESIGN-0030). Kind is file, setting, ruleset, label,
// property or tree; Key is the path, property name, ruleset name, label
// name or directory prefix. Keys are canonicalized by the framework
// (Canonical), never by a type, and are static per definition.
type Resource struct {
	Kind string
	Key  string
}

// String renders the resource as kind:key, such as
// "file:.github/CODEOWNERS".
func (r Resource) String() string {
	return r.Kind + ":" + r.Key
}

// RuleKind is a check a Type offers, such as "exists" or "equals".
type RuleKind string

// RuleID is a rule's id: bare, unique within its control, in the
// ValidSlug grammar.
type RuleID string

// RuleKindSpec is what a Type says about one of its kinds: the parameters
// it accepts and whether Remediate can fix it. The policy loader
// validates against it and the reference docs are generated from it.
type RuleKindSpec struct {
	Kind   RuleKind
	Params []ParamSpec
	// Remediable means a rule of this kind may declare remediate = true.
	Remediable bool
}

// ParamSpec is one parameter of a rule kind.
type ParamSpec struct {
	Name string
	// Type is "string", "bool", "number", "list(string)" or
	// "map(string)".
	Type     string
	Required bool
}

// Rule is one declared requirement of a control.
type Rule struct {
	// ID is qualified in logs and the UI as repo_settings@1/no-wiki.
	ID RuleID
	// Number is display only: "1.2".
	Number string
	// Title is display only.
	Title string
	// Kind is one of Type.RuleKinds(); it defaults to the id when the id
	// names a kind.
	Kind RuleKind
	// Params are kind-specific and validated by Type.Build.
	Params map[string]any
	// Remediate means Remediate fixes this rule's failures when the
	// observed state allows it (D14).
	Remediate bool
}

// RuleStatus is the outcome of one rule. The set is closed: Validate
// rejects any other value before a control status is derived.
type RuleStatus string

// The rule statuses.
const (
	Pass RuleStatus = "pass"
	Fail RuleStatus = "fail"
	// Error is a status that could not be determined this run;
	// transient, retried next run.
	Error RuleStatus = "error"
	// Unknown cannot be determined until the input changes; not retried
	// by itself.
	Unknown       RuleStatus = "unknown"
	NotApplicable RuleStatus = "not_applicable"
)

// RuleResult is one rule's outcome with its evidence. Evidence is typed,
// versioned and text-only: scalars and arrays only, at most 16 KiB per
// rule, with evidence_kind and evidence_version always present (D18).
type RuleResult struct {
	RuleID   RuleID
	Status   RuleStatus
	Evidence map[string]any
}

// ReadState is what an evaluation saw of one resource.
type ReadState string

// The read states.
const (
	Present   ReadState = "present"
	Absent    ReadState = "absent"
	ReadError ReadState = "error"
)

// ResourceRead records one resource the evaluation looked at (D13). A
// file read is pinned to the evaluated commit and its Digest is the blob
// SHA. An API read is a time-stamped observation: Digest is sha256 over
// the canonical JSON of the observed value, Version carries the
// provider's ETag or updated_at where one exists, and ObservedAt is when
// it was fetched. An absent property and a present null value are
// different reads.
type ResourceRead struct {
	Resource Resource
	State    ReadState
	// Digest is empty when absent.
	Digest  string
	Version string
	// ObservedAt is zero for a commit-pinned file.
	ObservedAt time.Time
	// Err says why State is error.
	Err string
}

// Evaluation is what Evaluate returns. Validate checks it holds exactly
// one result per declared rule before StatusOf runs (D18).
type Evaluation struct {
	Results []RuleResult
	Reads   []ResourceRead
}

// RepoContext is the repository an evaluation runs against.
type RepoContext struct {
	Org           string
	Name          string
	DefaultBranch string
	// SHA is the pinned commit.
	SHA  string
	Vars template.Vars
}

// EvalInput is everything Evaluate may use.
type EvalInput struct {
	Repo RepoContext
	// Reader is read-only; file reads are pinned to Repo.SHA and every
	// read is cached.
	Reader Reader
}

// RemediationInput is EvalInput plus the evaluation of the same state.
type RemediationInput struct {
	EvalInput
	Evaluation Evaluation
}

// APIChange is one direct change to a non-file resource.
type APIChange struct {
	Resource Resource
	// Op is "set" or "delete".
	Op string
	// Value is the resource kind's value type; nil clears.
	Value any
	// Reason is as FileChange.Reason.
	Reason string
}

// ChangeSet is what a remediation proposes (D14). Every failing rule of
// the control appears in exactly one of Fixed, Manual or Blocked: a Fixed
// rule passes once the change is applied; a Manual rule needs a human and
// has a note; a Blocked rule could not be remediated from this
// observation and has a note. A change set with no Files and no API
// entries is a recommendation.
type ChangeSet struct {
	// Files are applied as one commit; at most MaxChangeSetFiles,
	// MaxFileBytes each, regular files only.
	Files []FileChange
	// API changes are applied one resource at a time.
	API     []APIChange
	Fixed   []RuleID
	Manual  []RuleID
	Blocked []RuleID
	// Notes are manual steps for the rules in Manual and Blocked.
	Notes []string
}

// IsRecommendation reports whether the change set writes nothing.
func (cs *ChangeSet) IsRecommendation() bool {
	return len(cs.Files) == 0 && len(cs.API) == 0
}

// WorkflowApply is implemented by a Type whose API changes may be
// proposed with apply = "workflow" (D15): the remediation PR carries a
// workflow file that applies ChangeSet.API on merge. Such a control also
// owns file:.github/workflows/repo-guardian-<slug>.yml.
type WorkflowApply interface {
	WorkflowFile(def Definition, changes []APIChange) (FileChange, error)
}

// Status is a control's status, derived from its rule results by
// StatusOf.
type Status string

// The control statuses.
const (
	StatusCompliant     Status = "compliant"
	StatusNonCompliant  Status = "non_compliant"
	StatusUnknown       Status = "unknown"
	StatusNotApplicable Status = "not_applicable"
)

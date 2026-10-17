package control

import "context"

// stubControl is a Control with fixed rules and resources for framework
// tests; it never evaluates anything.
type stubControl struct {
	id    ID
	rules []Rule
	owns  []Resource
}

func newStub(ids ...RuleID) *stubControl {
	c := &stubControl{id: ID{Slug: "stub", Version: 1}}
	for _, id := range ids {
		c.rules = append(c.rules, Rule{ID: id, Kind: RuleKind(id)})
	}

	return c
}

func (c *stubControl) ID() ID           { return c.id }
func (c *stubControl) Owns() []Resource { return c.owns }
func (*stubControl) Reads() []Resource  { return nil }
func (c *stubControl) Rules() []Rule    { return c.rules }

func (*stubControl) Evaluate(context.Context, EvalInput) (Evaluation, error) {
	return Evaluation{}, nil
}

func (*stubControl) Remediate(context.Context, RemediationInput) (ChangeSet, error) {
	return ChangeSet{}, nil
}

// ev is valid evidence for tests.
func ev() map[string]any {
	return map[string]any{EvidenceKind: "test", EvidenceVersion: 1}
}

func result(id RuleID, s RuleStatus) RuleResult {
	return RuleResult{RuleID: id, Status: s, Evidence: ev()}
}

// stubType is a Type for framework tests.
type stubType struct {
	name      string
	semantics int
	kinds     []RuleKindSpec
	files     []string
}

func (t *stubType) Name() string              { return t.name }
func (t *stubType) Semantics() int            { return t.semantics }
func (t *stubType) RuleKinds() []RuleKindSpec { return t.kinds }

func (*stubType) Build(def Definition, _ BuildEnv) (Control, error) { //nolint:gocritic // Type.Build signature
	return &stubControl{id: def.ID, rules: def.Rules}, nil
}

func (t *stubType) ReferencedFiles(Definition) []string { return t.files }

// templates is a TemplateSource over a map.
type templates map[string]string

func (m templates) Lookup(name string) (string, bool) {
	b, ok := m[name]

	return b, ok
}

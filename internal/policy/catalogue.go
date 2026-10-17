package policy

import (
	"fmt"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/donaldgifford/repo-guardian/internal/control"
	"github.com/donaldgifford/repo-guardian/internal/rules"
	"github.com/donaldgifford/repo-guardian/internal/template"
)

// definition is one built catalogue entry.
type definition struct {
	def      control.Definition
	ctl      control.Control
	revision string
	pos      hcl.Range
}

// attrTitle is the title attribute, on controls and rules.
const attrTitle = "title"

var catalogueSchema = &hcl.BodySchema{
	Blocks: []hcl.BlockHeaderSchema{{Type: blockControl, LabelNames: []string{"slug"}}},
}

var controlSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "version", Required: true},
		{Name: "type", Required: true},
		{Name: attrTitle},
		{Name: "description"},
		{Name: "template"},
	},
	Blocks: []hcl.BlockHeaderSchema{
		{Type: "rule", LabelNames: []string{"id"}},
		{Type: "remediation"},
		{Type: "pr"},
	},
}

var ruleSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{{Name: "number"}, {Name: attrTitle}, {Name: "kind"}, {Name: "remediate"}},
}

var remediationApplySchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{{Name: "apply"}},
}

// decodeCatalogue decodes every control block in the catalogue files and
// builds it through the registry.
func (l *loader) decodeCatalogue(names []string, bodies map[string]hcl.Body) (map[control.ID]*definition, error) {
	defs := make(map[control.ID]*definition)

	for _, name := range names {
		content, diags := bodies[name].Content(catalogueSchema)
		if diags.HasErrors() {
			return nil, diags
		}

		for _, block := range content.Blocks {
			d, err := l.decodeControl(block)
			if err != nil {
				return nil, err
			}

			if prev, dup := defs[d.def.ID]; dup {
				return nil, fmt.Errorf("%s: control %s is defined twice (first at %s)", at(block.DefRange), d.def.ID, at(prev.pos))
			}

			defs[d.def.ID] = d
		}
	}

	return defs, nil
}

// decodeControl decodes one control block into a Definition, builds it
// and computes its revision.
func (l *loader) decodeControl(block *hcl.Block) (*definition, error) {
	slug := block.Labels[0]
	if !control.ValidSlug.MatchString(slug) {
		return nil, fmt.Errorf("%s: control slug %q must match %s", at(block.LabelRanges[0]), slug, control.ValidSlug)
	}

	content, diags := block.Body.Content(withParams(block.Body, controlSchema))
	if diags.HasErrors() {
		return nil, diags
	}

	def := control.Definition{ID: control.ID{Slug: slug}}

	if err := l.decodeControlAttrs(content.Attributes, &def); err != nil {
		return nil, err
	}

	params, err := l.params(content.Attributes, controlSchema)
	if err != nil {
		return nil, err
	}

	def.Params = params

	typ, ok := l.reg.Type(def.Type)
	if !ok {
		return nil, fmt.Errorf("%s: control %s: unknown control type %q", at(content.Attributes["type"].Range), def.ID, def.Type)
	}

	if err := l.decodeControlBlocks(content.Blocks, typ, &def); err != nil {
		return nil, err
	}

	env := control.BuildEnv{Templates: l.templates, Renderer: l.renderer}

	ctl, err := l.reg.Build(def, env)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", at(block.DefRange), err)
	}

	rev, err := control.Revision(typ, def, env)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", at(block.DefRange), err)
	}

	return &definition{def: def, ctl: ctl, revision: rev, pos: block.DefRange}, nil
}

// decodeControlAttrs decodes version, type, title, description and
// template.
func (l *loader) decodeControlAttrs(attrs hcl.Attributes, def *control.Definition) error {
	var version int
	if diags := gohcl.DecodeExpression(attrs["version"].Expr, l.ctx, &version); diags.HasErrors() {
		return diags
	}

	if version < 1 {
		return fmt.Errorf("%s: control %s: version must be a positive integer", at(attrs["version"].Range), def.ID.Slug)
	}

	def.ID.Version = version

	for name, dst := range map[string]*string{"type": &def.Type, attrTitle: &def.Title, "description": &def.Description, "template": &def.Template} {
		attr, ok := attrs[name]
		if !ok {
			continue
		}

		if diags := gohcl.DecodeExpression(attr.Expr, l.ctx, dst); diags.HasErrors() {
			return diags
		}
	}

	return nil
}

// decodeControlBlocks decodes the rule, remediation and pr blocks.
func (l *loader) decodeControlBlocks(blocks hcl.Blocks, typ control.Type, def *control.Definition) error {
	kinds := make(map[control.RuleKind]bool)
	for _, k := range typ.RuleKinds() {
		kinds[k.Kind] = true
	}

	for _, b := range blocks {
		switch b.Type {
		case "rule":
			rule, err := l.decodeRule(b, kinds, def.ID)
			if err != nil {
				return err
			}

			def.Rules = append(def.Rules, rule)
		case "remediation":
			content, diags := b.Body.Content(remediationApplySchema)
			if diags.HasErrors() {
				return diags
			}

			if attr, ok := content.Attributes["apply"]; ok {
				if diags := gohcl.DecodeExpression(attr.Expr, l.ctx, &def.Apply); diags.HasErrors() {
					return diags
				}
			}
		case "pr":
			pr, err := l.decodePR(b)
			if err != nil {
				return err
			}

			def.PR = pr
		}
	}

	return nil
}

// decodeRule decodes one rule block. A rule without kind takes its id as
// its kind only when the type declares a kind of that name
// (DESIGN-0033 AR-0033-01).
func (l *loader) decodeRule(b *hcl.Block, kinds map[control.RuleKind]bool, id control.ID) (control.Rule, error) {
	rule := control.Rule{ID: control.RuleID(b.Labels[0])}
	if !control.ValidSlug.MatchString(string(rule.ID)) {
		return rule, fmt.Errorf("%s: %s: rule id %q must match %s", at(b.LabelRanges[0]), id, rule.ID, control.ValidSlug)
	}

	content, diags := b.Body.Content(withParams(b.Body, ruleSchema))
	if diags.HasErrors() {
		return rule, diags
	}

	var kind string

	for name, dst := range map[string]any{"number": &rule.Number, attrTitle: &rule.Title, "kind": &kind, "remediate": &rule.Remediate} {
		if attr, ok := content.Attributes[name]; ok {
			if diags := gohcl.DecodeExpression(attr.Expr, l.ctx, dst); diags.HasErrors() {
				return rule, diags
			}
		}
	}

	rule.Kind = control.RuleKind(kind)
	if rule.Kind == "" {
		if !kinds[control.RuleKind(rule.ID)] {
			return rule, fmt.Errorf("%s: %s/%s: a rule without kind needs an id that is a rule kind of its type", at(b.DefRange), id, rule.ID)
		}

		rule.Kind = control.RuleKind(rule.ID)
	}

	params, err := l.params(content.Attributes, ruleSchema)
	if err != nil {
		return rule, err
	}

	rule.Params = params

	return rule, nil
}

// decodePR decodes a pr {} block and compiles its title and body.
func (l *loader) decodePR(b *hcl.Block) (template.PRConfig, error) {
	var pr template.PRConfig
	if diags := gohcl.DecodeBody(b.Body, l.ctx, &pr); diags.HasErrors() {
		return pr, diags
	}

	if attrs, diags := b.Body.JustAttributes(); !diags.HasErrors() {
		_, pr.LabelsSet = attrs["labels"]
	}

	for _, f := range []struct {
		src *string
		dst **template.Compiled
	}{{pr.Title, &pr.CompiledTitle}, {pr.Body, &pr.CompiledBody}} {
		if f.src == nil {
			continue
		}

		c, err := l.renderer.Parse("pr", *f.src)
		if err != nil {
			return pr, fmt.Errorf("%s: pr template: %w", at(b.DefRange), err)
		}

		*f.dst = c
	}

	return pr, nil
}

// withParams extends schema with every other attribute body declares,
// so Content accepts parameters while still refusing unknown blocks.
func withParams(body hcl.Body, schema *hcl.BodySchema) *hcl.BodySchema {
	out := &hcl.BodySchema{Attributes: slices.Clone(schema.Attributes), Blocks: schema.Blocks}

	sb, ok := body.(*hclsyntax.Body)
	if !ok {
		return out
	}

	for name := range sb.Attributes {
		if !slices.ContainsFunc(schema.Attributes, func(a hcl.AttributeSchema) bool { return a.Name == name }) {
			out.Attributes = append(out.Attributes, hcl.AttributeSchema{Name: name})
		}
	}

	return out
}

// params decodes the attributes schema does not name as parameters.
func (l *loader) params(attrs hcl.Attributes, schema *hcl.BodySchema) (map[string]any, error) {
	var out map[string]any

	for name, attr := range attrs {
		if slices.ContainsFunc(schema.Attributes, func(a hcl.AttributeSchema) bool { return a.Name == name }) {
			continue
		}

		v, diags := attr.Expr.Value(l.ctx)
		if diags.HasErrors() {
			return nil, diags
		}

		g, err := goValue(v)
		if err != nil {
			return nil, fmt.Errorf("%s: %s: %w", at(attr.Range), name, err)
		}

		if out == nil {
			out = make(map[string]any)
		}

		out[name] = g
	}

	return out, nil
}

// templateSource is the policy root's templates/ over the embedded
// fallbacks (DESIGN-0030 D20, INV-0022 spike 7). A name resolves to
// templates/<name>.tmpl, then templates/<name>, then the embedded
// template of that name; the embedded ones used are recorded for the
// policy version.
type templateSource struct {
	files    map[string][]byte
	embedded map[string]string
	used     map[string]bool
}

func newTemplateSource(files map[string][]byte) (*templateSource, error) {
	store := rules.NewTemplateStore()
	if err := store.Load(""); err != nil {
		return nil, fmt.Errorf("embedded templates: %w", err)
	}

	return &templateSource{files: files, embedded: store.AsMap(), used: make(map[string]bool)}, nil
}

// Lookup implements control.TemplateSource.
func (t *templateSource) Lookup(name string) (string, bool) {
	bare := strings.TrimSuffix(name, ".tmpl")

	for _, p := range []string{templatesDir + "/" + bare + ".tmpl", templatesDir + "/" + name} {
		if b, ok := t.files[p]; ok {
			return string(b), true
		}
	}

	body, ok := t.embedded[bare]
	if ok {
		t.used[bare] = true
	}

	return body, ok
}

// usedEmbedded returns the embedded templates Lookup served, by name.
func (t *templateSource) usedEmbedded() map[string]string {
	out := make(map[string]string, len(t.used))
	for name := range t.used {
		out[name] = t.embedded[name]
	}

	return out
}

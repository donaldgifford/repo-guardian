package policy

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"

	"github.com/donaldgifford/repo-guardian/internal/control"
)

var orgFileSchema = &hcl.BodySchema{Blocks: []hcl.BlockHeaderSchema{{Type: "org", LabelNames: []string{"name"}}}}

// layerBlocks are the blocks both an org and a repos block hold.
var layerBlocks = []hcl.BlockHeaderSchema{
	{Type: blockReplace, LabelNames: []string{blockControl}},
	{Type: blockExclude, LabelNames: []string{blockControl}},
	{Type: blockControl, LabelNames: []string{blockControl}},
}

var orgSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{{Name: attrMode}, {Name: attrControls}},
	Blocks: append(slices.Clone(layerBlocks),
		hcl.BlockHeaderSchema{Type: blockExclRep},
		hcl.BlockHeaderSchema{Type: blockRepos, LabelNames: []string{"label"}},
		hcl.BlockHeaderSchema{Type: blockRemed},
	),
}

var reposSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{{Name: attrMatch, Required: true}, {Name: attrMode}, {Name: attrControls}},
	Blocks:     layerBlocks,
}

var replaceSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{{Name: "with", Required: true}, {Name: attrReason}},
}

var excludeSchema = &hcl.BodySchema{Attributes: []hcl.AttributeSchema{{Name: attrReason}}}

var overrideSchema = &hcl.BodySchema{Attributes: []hcl.AttributeSchema{{Name: attrMode, Required: true}}}

var excludeReposSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{{Name: attrMatch, Required: true}, {Name: attrReason}},
}

// decodeOrgs decodes every org block in the org files. A second policy
// for one org (names compare case-insensitively, as GitHub logins do) and
// an org not in enterprise.orgs fail load.
func (l *loader) decodeOrgs(names []string, bodies map[string]hcl.Body, ent *enterprisePolicy) ([]*orgPolicy, error) {
	var orgs []*orgPolicy

	for _, name := range names {
		content, diags := bodies[name].Content(orgFileSchema)
		if diags.HasErrors() {
			return nil, diags
		}

		for _, b := range content.Blocks {
			org, err := l.decodeOrg(b)
			if err != nil {
				return nil, err
			}

			if i := slices.IndexFunc(orgs, func(o *orgPolicy) bool { return strings.EqualFold(o.name, org.name) }); i >= 0 {
				return nil, fmt.Errorf("%s: a second policy for org %q (first at %s)", at(b.DefRange), org.name, at(orgs[i].pos))
			}

			if !slices.ContainsFunc(ent.orgs, func(o string) bool { return strings.EqualFold(o, org.name) }) {
				return nil, fmt.Errorf(
					"%s: org %q is not in enterprise.orgs (%s); onboarding is the enterprise list",
					at(b.LabelRanges[0]),
					org.name,
					at(ent.orgsPos),
				)
			}

			orgs = append(orgs, org)
		}
	}

	return orgs, nil
}

// decodeOrg decodes one org block.
func (l *loader) decodeOrg(b *hcl.Block) (*orgPolicy, error) {
	content, diags := b.Body.Content(orgSchema)
	if diags.HasErrors() {
		return nil, diags
	}

	org := &orgPolicy{name: b.Labels[0], pos: b.DefRange}

	ly, err := l.decodeLayer(content)
	if err != nil {
		return nil, err
	}

	org.layer = ly

	for _, blk := range content.Blocks {
		switch blk.Type {
		case blockExclRep:
			er, err := l.decodeExcludeRepos(blk)
			if err != nil {
				return nil, err
			}

			org.excludeRepos = append(org.excludeRepos, er)
		case blockRepos:
			rb, err := l.decodeRepos(blk)
			if err != nil {
				return nil, err
			}

			org.repos = append(org.repos, rb)
		case blockRemed:
			r, err := l.decodeRemediation(blk)
			if err != nil {
				return nil, err
			}

			org.remed = r
		}
	}

	return org, nil
}

// decodeRepos decodes one labelled repos block.
func (l *loader) decodeRepos(b *hcl.Block) (reposBlock, error) {
	content, diags := b.Body.Content(reposSchema)
	if diags.HasErrors() {
		return reposBlock{}, diags
	}

	rb := reposBlock{label: b.Labels[0], pos: b.DefRange}

	if diags := gohcl.DecodeExpression(content.Attributes[attrMatch].Expr, l.ctx, &rb.match); diags.HasErrors() {
		return reposBlock{}, diags
	}

	ly, err := l.decodeLayer(content)
	if err != nil {
		return reposBlock{}, err
	}

	rb.layer = ly

	return rb, nil
}

// decodeLayer decodes mode, controls, replace, exclude and control
// overrides, ordering the operations by their position in the file.
func (l *loader) decodeLayer(content *hcl.BodyContent) (layer, error) {
	var ly layer

	if a, ok := content.Attributes[attrMode]; ok {
		m, err := l.decodeMode(a)
		if err != nil {
			return ly, err
		}

		ly.mode = m
	}

	if a, ok := content.Attributes[attrControls]; ok {
		refs, err := l.decodeRefs(a)
		if err != nil {
			return ly, err
		}

		for _, r := range refs {
			ly.ops = append(ly.ops, op{kind: opAdd, id: r.id, pos: r.pos})
		}
	}

	for _, b := range content.Blocks {
		switch b.Type {
		case blockReplace, blockExclude:
			o, err := l.decodeOp(b)
			if err != nil {
				return ly, err
			}

			ly.ops = append(ly.ops, o)
		case blockControl:
			ov, err := l.decodeOverride(b)
			if err != nil {
				return ly, err
			}

			ly.overrides = append(ly.overrides, ov)
		}
	}

	// A stable sort keeps a controls list in its own order.
	slices.SortStableFunc(ly.ops, func(a, b op) int { return cmp.Compare(a.pos.Start.Byte, b.pos.Start.Byte) })

	return ly, nil
}

// decodeOp decodes a replace or exclude block.
func (l *loader) decodeOp(b *hcl.Block) (op, error) {
	id, err := decodeLabelID(b)
	if err != nil {
		return op{}, err
	}

	o := op{kind: opExclude, id: id, pos: b.DefRange}
	schema := excludeSchema

	if b.Type == blockReplace {
		o.kind, schema = opReplace, replaceSchema
	}

	content, diags := b.Body.Content(schema)
	if diags.HasErrors() {
		return op{}, diags
	}

	if a, ok := content.Attributes[attrReason]; ok {
		if diags := gohcl.DecodeExpression(a.Expr, l.ctx, &o.reason); diags.HasErrors() {
			return op{}, diags
		}
	}

	if a, ok := content.Attributes["with"]; ok {
		var s string
		if diags := gohcl.DecodeExpression(a.Expr, l.ctx, &s); diags.HasErrors() {
			return op{}, diags
		}

		with, err := control.ParseID(s)
		if err != nil {
			return op{}, fmt.Errorf("%s: %w", at(a.Range), err)
		}

		o.with = with
	}

	return o, nil
}

// decodeOverride decodes a control "x@N" { mode } block.
func (l *loader) decodeOverride(b *hcl.Block) (override, error) {
	id, err := decodeLabelID(b)
	if err != nil {
		return override{}, err
	}

	content, diags := b.Body.Content(overrideSchema)
	if diags.HasErrors() {
		return override{}, diags
	}

	m, err := l.decodeMode(content.Attributes[attrMode])
	if err != nil {
		return override{}, err
	}

	return override{id: id, mode: *m, pos: b.DefRange}, nil
}

// decodeExcludeRepos decodes an exclude_repos block.
func (l *loader) decodeExcludeRepos(b *hcl.Block) (excludeRepos, error) {
	content, diags := b.Body.Content(excludeReposSchema)
	if diags.HasErrors() {
		return excludeRepos{}, diags
	}

	er := excludeRepos{pos: b.DefRange}

	if diags := gohcl.DecodeExpression(content.Attributes[attrMatch].Expr, l.ctx, &er.match); diags.HasErrors() {
		return excludeRepos{}, diags
	}

	if a, ok := content.Attributes[attrReason]; ok {
		if diags := gohcl.DecodeExpression(a.Expr, l.ctx, &er.reason); diags.HasErrors() {
			return excludeRepos{}, diags
		}
	}

	return er, nil
}

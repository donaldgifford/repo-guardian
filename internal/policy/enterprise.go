package policy

import (
	"fmt"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"

	"github.com/donaldgifford/repo-guardian/internal/control"
)

// Attribute and block names shared by the enterprise and org schemas.
const (
	attrMode     = "mode"
	attrControls = "controls"
	attrMatch    = "match"
	attrReason   = "reason"
	blockRemed   = "remediation"
	blockExclude = "exclude"
	blockReplace = "replace"
	blockControl = "control"
	blockExclRep = "exclude_repos"
	blockRepos   = "repos"
)

var enterpriseFileSchema = &hcl.BodySchema{Blocks: []hcl.BlockHeaderSchema{{Type: "enterprise"}}}

// enterpriseSchema names the blocks an enterprise policy may not hold, so
// their presence gets a precise error instead of "unsupported block".
var enterpriseSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{{Name: "orgs", Required: true}, {Name: attrMode}, {Name: attrControls}},
	Blocks: []hcl.BlockHeaderSchema{
		{Type: blockRemed},
		{Type: blockExclude, LabelNames: []string{blockControl}},
		{Type: blockReplace, LabelNames: []string{blockControl}},
		{Type: blockExclRep},
		{Type: blockRepos, LabelNames: []string{"label"}},
		{Type: blockControl, LabelNames: []string{blockControl}},
	},
}

var remediationSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{{Name: "max_open_prs"}, {Name: "reopen_after"}},
}

// decodeEnterprise decodes enterprise.hcl: exactly one enterprise block.
func (l *loader) decodeEnterprise(body hcl.Body) (*enterprisePolicy, error) {
	content, diags := body.Content(enterpriseFileSchema)
	if diags.HasErrors() {
		return nil, diags
	}

	if n := len(content.Blocks); n != 1 {
		return nil, fmt.Errorf("%s: want exactly one enterprise block, found %d", enterpriseFile, n)
	}

	block := content.Blocks[0]

	ec, diags := block.Body.Content(enterpriseSchema)
	if diags.HasErrors() {
		return nil, diags
	}

	ent := &enterprisePolicy{pos: block.DefRange}

	attr := ec.Attributes["orgs"]
	if diags := gohcl.DecodeExpression(attr.Expr, l.ctx, &ent.orgs); diags.HasErrors() {
		return nil, diags
	}

	ent.orgsPos = attr.Range

	if a, ok := ec.Attributes[attrMode]; ok {
		m, err := l.decodeMode(a)
		if err != nil {
			return nil, err
		}

		ent.mode = m
	}

	if a, ok := ec.Attributes[attrControls]; ok {
		refs, err := l.decodeRefs(a)
		if err != nil {
			return nil, err
		}

		ent.controls = refs
	}

	for _, b := range ec.Blocks {
		if b.Type != blockRemed {
			return nil, fmt.Errorf(
				"%s: the enterprise policy is the baseline and has no exclusions or variations: %s block not allowed",
				at(b.DefRange),
				b.Type,
			)
		}

		r, err := l.decodeRemediation(b)
		if err != nil {
			return nil, err
		}

		ent.remed = r
	}

	return ent, nil
}

// decodeMode decodes a mode attribute; validate checks its value.
func (l *loader) decodeMode(attr *hcl.Attribute) (*modeDecl, error) {
	var s string
	if diags := gohcl.DecodeExpression(attr.Expr, l.ctx, &s); diags.HasErrors() {
		return nil, diags
	}

	return &modeDecl{mode: Mode(s), pos: attr.Range}, nil
}

// decodeRefs decodes a list of "slug@N" references.
func (l *loader) decodeRefs(attr *hcl.Attribute) ([]ref, error) {
	var ids []string
	if diags := gohcl.DecodeExpression(attr.Expr, l.ctx, &ids); diags.HasErrors() {
		return nil, diags
	}

	refs := make([]ref, 0, len(ids))

	for _, s := range ids {
		id, err := control.ParseID(s)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", at(attr.Range), err)
		}

		refs = append(refs, ref{id: id, pos: attr.Range})
	}

	return refs, nil
}

// decodeLabelID parses a block label naming a control.
func decodeLabelID(b *hcl.Block) (control.ID, error) {
	id, err := control.ParseID(b.Labels[0])
	if err != nil {
		return control.ID{}, fmt.Errorf("%s: %w", at(b.LabelRanges[0]), err)
	}

	return id, nil
}

// decodeRemediation decodes a remediation {} block; validate checks the
// values.
func (l *loader) decodeRemediation(b *hcl.Block) (remediationDecl, error) {
	var r remediationDecl

	content, diags := b.Body.Content(remediationSchema)
	if diags.HasErrors() {
		return r, diags
	}

	if a, ok := content.Attributes["max_open_prs"]; ok {
		var n int
		if diags := gohcl.DecodeExpression(a.Expr, l.ctx, &n); diags.HasErrors() {
			return r, diags
		}

		r.maxOpenPRs, r.maxOpenPRsPos = &n, a.Range
	}

	if a, ok := content.Attributes["reopen_after"]; ok {
		var s string
		if diags := gohcl.DecodeExpression(a.Expr, l.ctx, &s); diags.HasErrors() {
			return r, diags
		}

		r.reopenAfter, r.reopenAfterPos = &s, a.Range
	}

	return r, nil
}

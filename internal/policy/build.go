package policy

import (
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// build turns the parsed files into a Snapshot.
func (l *loader) build(layout rootLayout, bodies map[string]hcl.Body) (*Snapshot, error) {
	for _, name := range layout.hcl {
		if diags := l.checkExpressions(bodies[name]); diags.HasErrors() {
			return nil, diags
		}
	}

	defs, err := l.decodeCatalogue(layout.catalogue, bodies)
	if err != nil {
		return nil, err
	}

	return &Snapshot{files: l.files, defs: defs, embeddedUsed: l.templates.usedEmbedded()}, nil
}

// checkExpressions evaluates every attribute in body, recursively,
// against the loader's context: literals and file() only, no variables
// and no other function, whatever the decoder later does with the value.
func (l *loader) checkExpressions(body hcl.Body) hcl.Diagnostics {
	sb, ok := body.(*hclsyntax.Body)
	if !ok {
		return nil
	}

	var diags hcl.Diagnostics

	for _, attr := range sb.Attributes {
		_, d := attr.Expr.Value(l.ctx)
		diags = append(diags, d...)
	}

	for _, b := range sb.Blocks {
		diags = append(diags, l.checkExpressions(b.Body)...)
	}

	return diags
}

package v1config

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// Rule types the reader understands, and the attributes and nested blocks
// each one knows. The lists mirror the v1 schema in internal/policy as of
// 2026-10-05; a v1 attribute added later appears under Unrecognised rather
// than failing.
const (
	ruleFile             = "file"
	ruleSetting          = "setting"
	ruleBranchProtection = "branch_protection"

	// Item kinds.
	kindBlock     = "block"
	kindAttribute = "attribute"
)

var (
	guardianAttrs = set("dry_run", "schedule_interval", "worker_count", "queue_size", "log_level",
		"skip_forks", "skip_archived", "rate_limit_threshold", "auto_close_pr", "orphan_cleanup")
	ruleAttrs = map[string]map[string]bool{
		ruleFile:    set("enabled", "check", "paths", "target", "template"),
		ruleSetting: set("enabled", "property", "expected", "remediate"),
		ruleBranchProtection: set("enabled", "branch", "require_pr", "required_approvals", "dismiss_stale_reviews",
			"require_status_checks", "enforce_admins", "require_linear_history", "remediate"),
	}
	ruleBlocks = map[string]map[string]bool{
		ruleFile:             set("pr", "assertion", "ignore", "scope", "reconcile", "when"),
		ruleSetting:          set("ignore", "scope"),
		ruleBranchProtection: set("ignore", "scope"),
	}
	prAttrs         = set("search_terms", "title", "body", "labels", "inherits")
	assertionAttrs  = set("pattern", "not_pattern", "yaml_path", "contains", "equals", "non_empty", "message")
	reconcilerAttrs = set("watch", "mode", "delete_extra", "annotation_properties")
)

func set(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// Read parses the v1 policy at path. A missing file or a syntax error is an
// error; an unknown block or attribute is not.
func Read(path string) (*Document, error) {
	src, err := os.ReadFile(path) //nolint:gosec // the operator names the file to read
	if err != nil {
		return nil, fmt.Errorf("read policy: %w", err)
	}
	return Parse(src, path)
}

// Parse reads a v1 policy from src; filename is used in diagnostics and as
// [Document.Path].
func Parse(src []byte, filename string) (*Document, error) {
	file, diags := hclsyntax.ParseConfig(src, filename, hcl.InitialPos)
	if diags.HasErrors() {
		return nil, fmt.Errorf("parse policy: %w", diags)
	}
	body, ok := file.Body.(*hclsyntax.Body)
	if !ok {
		return nil, errors.New("parse policy: not native HCL syntax")
	}
	r := &reader{src: src, doc: &Document{Path: filename, LegacyMode: true}}
	r.top(body)
	return r.doc, nil
}

type reader struct {
	src []byte
	doc *Document
}

func (r *reader) top(body *hclsyntax.Body) {
	for _, a := range sortedAttrs(body) {
		r.unknownAttr("", a)
	}
	for _, b := range body.Blocks {
		switch b.Type {
		case "guardian":
			r.guardian(b)
		case "locals":
			for _, a := range sortedAttrs(b.Body) {
				r.doc.Locals = append(r.doc.Locals, r.attr(a))
			}
			r.unknownBlocks(b.Type, b.Body)
		case "ignore":
			r.doc.Ignore = r.repos("ignore", b)
		case "scope":
			r.doc.LegacyMode = false
			r.doc.Orgs = r.orgs("scope", b)
		case "defaults":
			r.defaults(b)
		case "rule":
			r.rule(b)
		default:
			r.unknownBlock("", b)
		}
	}
}

func (r *reader) guardian(b *hclsyntax.Block) {
	for _, a := range sortedAttrs(b.Body) {
		if guardianAttrs[a.Name] {
			r.doc.Guardian = append(r.doc.Guardian, r.attr(a))
		} else {
			r.unknownAttr("guardian", a)
		}
	}
	r.unknownBlocks("guardian", b.Body)
}

func (r *reader) defaults(b *hclsyntax.Block) {
	for _, a := range sortedAttrs(b.Body) {
		r.unknownAttr("defaults", a)
	}
	for _, nb := range b.Body.Blocks {
		if nb.Type == "pr" {
			r.doc.Defaults = r.pr("defaults > pr", nb)
			continue
		}
		r.unknownBlock("defaults", nb)
	}
}

func (r *reader) rule(b *hclsyntax.Block) {
	if len(b.Labels) != 2 || ruleAttrs[b.Labels[0]] == nil {
		r.unknownBlock("", b)
		return
	}
	rule := Rule{Type: b.Labels[0], Name: b.Labels[1], Line: b.DefRange().Start.Line}
	where := fmt.Sprintf("rule %q %q", rule.Type, rule.Name)
	for _, a := range sortedAttrs(b.Body) {
		if !ruleAttrs[rule.Type][a.Name] {
			r.unknownAttr(where, a)
			continue
		}
		r.ruleAttr(&rule, a)
	}
	for _, nb := range b.Body.Blocks {
		if !ruleBlocks[rule.Type][nb.Type] {
			r.unknownBlock(where, nb)
			continue
		}
		r.ruleBlock(&rule, where, nb)
	}
	r.doc.Rules = append(r.doc.Rules, rule)
}

// ruleAttr stores a known rule attribute in its field when the value is a
// literal, and in Attrs otherwise.
func (r *reader) ruleAttr(rule *Rule, a *hclsyntax.Attribute) {
	switch a.Name {
	case "enabled":
		if v, ok := literalBool(a.Expr); ok {
			rule.Enabled = &v
			return
		}
	case "paths":
		if v, ok := literalStrings(a.Expr); ok {
			rule.Paths = v
			return
		}
	case "check", "template", "target":
		if v, ok := literalString(a.Expr); ok {
			switch a.Name {
			case "check":
				rule.Check = v
			case "template":
				rule.Template = v
			default:
				rule.Target = v
			}
			return
		}
	}
	rule.Attrs = append(rule.Attrs, r.attr(a))
}

func (r *reader) ruleBlock(rule *Rule, where string, b *hclsyntax.Block) {
	switch b.Type {
	case "scope":
		rule.Scope = r.orgs(where+" > scope", b)
	case "ignore":
		rule.Ignore = r.repos(where+" > ignore", b)
	case "when":
		for _, a := range sortedAttrs(b.Body) {
			if a.Name != "rule_satisfied" {
				r.unknownAttr(where+" > when", a)
				continue
			}
			at := r.attr(a)
			rule.When = &at
		}
		r.unknownBlocks(where+" > when", b.Body)
	case "assertion":
		rule.Assertions = append(rule.Assertions, r.assertion(where+" > assertion", b))
	case "reconcile":
		rule.Reconcilers = append(rule.Reconcilers, r.reconciler(where, b))
	case "pr":
		rule.PR = r.pr(where+" > pr", b)
	}
}

func (r *reader) assertion(where string, b *hclsyntax.Block) Attr {
	parts := make([]string, 0, len(b.Body.Attributes))
	for _, a := range sortedAttrs(b.Body) {
		if !assertionAttrs[a.Name] {
			r.unknownAttr(where, a)
			continue
		}
		parts = append(parts, a.Name+" = "+r.text(a.Expr.Range()))
	}
	r.unknownBlocks(where, b.Body)
	return Attr{Name: "assertion", Source: strings.Join(parts, ", "), Line: b.DefRange().Start.Line}
}

func (r *reader) reconciler(where string, b *hclsyntax.Block) Reconciler {
	rec := Reconciler{Line: b.DefRange().Start.Line}
	if len(b.Labels) > 0 {
		rec.Type = b.Labels[0]
	}
	where = fmt.Sprintf("%s > reconcile %q", where, rec.Type)
	for _, a := range sortedAttrs(b.Body) {
		if !reconcilerAttrs[a.Name] {
			r.unknownAttr(where, a)
			continue
		}
		rec.Attrs = append(rec.Attrs, r.attr(a))
	}
	for _, nb := range b.Body.Blocks {
		if nb.Type == "pr" {
			rec.PR = r.pr(where+" > pr", nb)
			continue
		}
		r.unknownBlock(where, nb)
	}
	return rec
}

func (r *reader) pr(where string, b *hclsyntax.Block) *PRBlock {
	pr := &PRBlock{Line: b.DefRange().Start.Line, Attrs: []Attr{}}
	for _, a := range sortedAttrs(b.Body) {
		if !prAttrs[a.Name] {
			r.unknownAttr(where, a)
			continue
		}
		pr.Attrs = append(pr.Attrs, r.attr(a))
	}
	r.unknownBlocks(where, b.Body)
	return pr
}

// orgs reads a scope block's orgs list.
func (r *reader) orgs(where string, b *hclsyntax.Block) []string {
	return r.stringList(where, b, "orgs")
}

// repos reads an ignore block's repos list.
func (r *reader) repos(where string, b *hclsyntax.Block) []string {
	return r.stringList(where, b, "repos")
}

// stringList reads the one list attribute a scope or ignore block carries. A
// non-literal list is kept as its source text in a single element.
func (r *reader) stringList(where string, b *hclsyntax.Block, name string) []string {
	var out []string
	for _, a := range sortedAttrs(b.Body) {
		if a.Name != name {
			r.unknownAttr(where, a)
			continue
		}
		if v, ok := literalStrings(a.Expr); ok {
			out = v
		} else {
			out = []string{r.text(a.Expr.Range())}
		}
	}
	r.unknownBlocks(where, b.Body)
	return out
}

func (r *reader) attr(a *hclsyntax.Attribute) Attr {
	return Attr{Name: a.Name, Source: r.text(a.Expr.Range()), Line: a.SrcRange.Start.Line}
}

func (r *reader) unknownAttr(where string, a *hclsyntax.Attribute) {
	r.doc.Unrecognised = append(r.doc.Unrecognised, Item{
		Kind:   kindAttribute,
		Name:   join(where, a.Name),
		Source: r.text(a.SrcRange),
		Line:   a.SrcRange.Start.Line,
	})
}

func (r *reader) unknownBlock(where string, b *hclsyntax.Block) {
	r.doc.Unrecognised = append(r.doc.Unrecognised, Item{
		Kind:   kindBlock,
		Name:   join(where, b.Type),
		Source: strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(r.text(b.DefRange())), "{")),
		Line:   b.DefRange().Start.Line,
	})
}

// unknownBlocks reports every nested block of a body that takes none.
func (r *reader) unknownBlocks(where string, body *hclsyntax.Body) {
	for _, b := range body.Blocks {
		r.unknownBlock(where, b)
	}
}

func (r *reader) text(rng hcl.Range) string {
	return string(rng.SliceBytes(r.src))
}

func join(where, name string) string {
	if where == "" {
		return name
	}
	return where + " > " + name
}

// sortedAttrs returns a body's attributes in file order; hclsyntax keeps
// them in a map.
func sortedAttrs(body *hclsyntax.Body) []*hclsyntax.Attribute {
	attrs := make([]*hclsyntax.Attribute, 0, len(body.Attributes))
	for _, a := range body.Attributes {
		attrs = append(attrs, a)
	}
	slices.SortFunc(attrs, func(a, b *hclsyntax.Attribute) int {
		return a.SrcRange.Start.Byte - b.SrcRange.Start.Byte
	})
	return attrs
}

// literal evaluates expr with no evaluation context, only when it references
// no variables. Function calls fail without a context and report false.
func literal(expr hclsyntax.Expression) (cty.Value, bool) {
	if len(expr.Variables()) > 0 {
		return cty.NilVal, false
	}
	v, diags := expr.Value(nil)
	if diags.HasErrors() || !v.IsWhollyKnown() || v.IsNull() {
		return cty.NilVal, false
	}
	return v, true
}

func literalString(expr hclsyntax.Expression) (string, bool) {
	v, ok := literal(expr)
	if !ok || v.Type() != cty.String {
		return "", false
	}
	return v.AsString(), true
}

func literalBool(expr hclsyntax.Expression) (bool, bool) {
	v, ok := literal(expr)
	if !ok || v.Type() != cty.Bool {
		return false, false
	}
	return v.True(), true
}

func literalStrings(expr hclsyntax.Expression) ([]string, bool) {
	v, ok := literal(expr)
	if !ok || (!v.Type().IsTupleType() && !v.Type().IsListType()) {
		return nil, false
	}
	out := make([]string, 0, v.LengthInt())
	for it := v.ElementIterator(); it.Next(); {
		_, ev := it.Element()
		if ev.IsNull() || ev.Type() != cty.String {
			return nil, false
		}
		out = append(out, ev.AsString())
	}
	return out, true
}

// Orgs reads the policy at path and returns its scope.orgs, or legacy true
// when the file has no scope block (every installed org).
func Orgs(path string) (orgs []string, legacy bool, err error) {
	doc, err := Read(path)
	if err != nil {
		return nil, false, err
	}
	return doc.Orgs, doc.LegacyMode, nil
}

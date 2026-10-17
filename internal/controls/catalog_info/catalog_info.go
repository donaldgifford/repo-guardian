// Package catalog_info is the catalog-info control type (DESIGN-0031
// § Catalog info): a Backstage catalog-info.yaml exists, is a Component,
// and has its fields and annotations filled in with real values. An
// absent file gets the template, which passes exists and kind; the field
// rules need a human value and are never remediated. A present file is
// never edited (D9).
package catalog_info

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/donaldgifford/repo-guardian/internal/catalog"
	"github.com/donaldgifford/repo-guardian/internal/control"
)

// Name is the type name a catalogue definition names.
const Name = "catalog_info"

// The rule kinds.
const (
	KindExists         control.RuleKind = "exists"
	KindKind           control.RuleKind = "kind"
	KindFieldSet       control.RuleKind = "field_set"
	KindAnnotationSet  control.RuleKind = "annotation_set"
	KindNoPlaceholders control.RuleKind = "no_placeholders"
)

// The rule parameters.
const (
	ParamField        = "field"
	ParamContains     = "contains"
	ParamAnnotation   = "annotation"
	ParamFields       = "fields"
	ParamPlaceholders = "placeholders"
)

// The locations, in the order Backstage's default discovery reads them.
const (
	PathYAML = "catalog-info.yaml"
	PathYML  = "catalog-info.yml"
)

var spec = control.FileSpec{Locations: []string{PathYAML, PathYML}, Canonical: PathYAML}

// DefaultPlaceholders are the tokens no_placeholders rejects when a rule
// names none: a value equal to one, or starting with one followed by a
// colon or a space ("TODO: add the owner"), compared case-insensitively.
// A value wrapped in angle brackets ("<team>") is a placeholder too.
var DefaultPlaceholders = []string{"TODO", "TBD", "FIXME", "CHANGEME", "CHANGE_ME", "REPLACE_ME", "XXX"}

const evidenceVersion = 1

// Type is the catalog-info control type.
type Type struct{}

var _ control.Type = Type{}

// Name returns "catalog_info".
func (Type) Name() string { return Name }

// Semantics is 1.
func (Type) Semantics() int { return 1 }

// RuleKinds says exists and kind are remediable (the template satisfies
// both); the field rules are not, because a template cannot know a
// repository's owner or system.
func (Type) RuleKinds() []control.RuleKindSpec {
	return []control.RuleKindSpec{
		{Kind: KindExists, Remediable: true},
		{Kind: KindKind, Remediable: true},
		{Kind: KindFieldSet, Params: []control.ParamSpec{
			{Name: ParamField, Type: control.ParamString, Required: true},
			{Name: ParamContains, Type: control.ParamString},
		}},
		{Kind: KindAnnotationSet, Params: []control.ParamSpec{{Name: ParamAnnotation, Type: control.ParamString, Required: true}}},
		{Kind: KindNoPlaceholders, Params: []control.ParamSpec{
			{Name: ParamFields, Type: control.ParamStringList},
			{Name: ParamPlaceholders, Type: control.ParamStringList},
		}},
	}
}

// Build compiles the template and returns the file control.
func (Type) Build(def control.Definition, env control.BuildEnv) (control.Control, error) { //nolint:gocritic // hugeParam: Type.Build's signature
	cfg := control.FileControlConfig[*catalog.Document]{Def: def, Spec: spec, Type: fileType{}, MaxBytes: catalog.MaxBytes}

	if def.Template != "" {
		if env.Templates == nil || env.Renderer == nil {
			return nil, errors.New("no template store")
		}

		body, ok := env.Templates.Lookup(def.Template)
		if !ok {
			return nil, fmt.Errorf("template %q not found", def.Template)
		}

		tmpl, err := env.Renderer.Parse(def.Template, body)
		if err != nil {
			return nil, fmt.Errorf("template %q: %w", def.Template, err)
		}

		cfg.Template = tmpl
	}

	return control.NewFileControl(cfg)
}

// fileType is the catalog-info FileType.
type fileType struct{}

// Parse is catalog.ParseDocument, with its parse errors carrying the
// line into the evidence.
func (fileType) Parse(content []byte) (*catalog.Document, error) {
	d, err := catalog.ParseDocument(content)

	var pe *catalog.ParseError
	if errors.As(err, &pe) {
		return nil, &control.ParseError{Line: pe.Line, Msg: pe.Msg}
	}

	return d, err
}

// Check evaluates one rule against a present, parsed file.
func (fileType) Check(d *catalog.Document, rule control.Rule) control.RuleResult { //nolint:gocritic // hugeParam: FileType's signature
	var (
		pass   bool
		detail map[string]any
	)

	switch rule.Kind {
	case KindExists:
		pass = true
	case KindKind:
		pass = d.IsComponent()
		detail = map[string]any{"api_version": d.Entity.APIVersion, "kind": d.Entity.Kind}
	case KindFieldSet:
		pass, detail = fieldSet(d, &rule)
	case KindAnnotationSet:
		pass, detail = annotationSet(d, &rule)
	case KindNoPlaceholders:
		pass, detail = noPlaceholders(d, &rule)
	}

	status := control.Fail
	if pass {
		status = control.Pass
	}

	ev := map[string]any{control.EvidenceKind: Name + "." + string(rule.Kind), control.EvidenceVersion: evidenceVersion}
	for k, v := range detail {
		ev[k] = v
	}

	return control.RuleResult{RuleID: rule.ID, Status: status, Evidence: ev}
}

// fieldSet: the dotted field is a non-empty scalar, containing the
// contains text when one is given.
func fieldSet(d *catalog.Document, rule *control.Rule) (bool, map[string]any) {
	field := str(rule.Params[ParamField])
	value, span, ok := d.Lookup(strings.Split(field, ".")...)
	detail := map[string]any{ParamField: field, "set": ok && strings.TrimSpace(value) != ""}

	if ok {
		detail["line"] = span.Line
	}

	pass := detail["set"] == true

	if want, has := rule.Params[ParamContains].(string); has {
		pass = pass && strings.Contains(value, want)
		detail[ParamContains] = want
	}

	return pass, detail
}

// annotationSet: metadata.annotations has the key with a non-empty
// value. Annotation keys hold dots, so they are not field paths.
func annotationSet(d *catalog.Document, rule *control.Rule) (bool, map[string]any) {
	key := str(rule.Params[ParamAnnotation])
	value := d.Entity.Metadata.Annotations[key]
	set := strings.TrimSpace(value) != ""

	return set, map[string]any{ParamAnnotation: key, "set": set}
}

// noPlaceholders: no checked scalar is a placeholder. The checked
// scalars are the named fields, else every scalar in the document.
func noPlaceholders(d *catalog.Document, rule *control.Rule) (bool, map[string]any) {
	tokens := DefaultPlaceholders
	if list := stringList(rule.Params[ParamPlaceholders]); list != nil {
		tokens = list
	}

	var (
		fields []string
		lines  []int
	)

	check := func(field, value string, line int) {
		if isPlaceholder(value, tokens) {
			fields, lines = append(fields, field), append(lines, line)
		}
	}

	if named := stringList(rule.Params[ParamFields]); named != nil {
		for _, f := range named {
			if v, span, ok := d.Lookup(strings.Split(f, ".")...); ok {
				check(f, v, span.Line)
			}
		}
	} else {
		walkScalars(d.Root(), "", check)
	}

	return len(fields) == 0, map[string]any{"placeholder_fields": fields, "placeholder_lines": lines}
}

func isPlaceholder(value string, tokens []string) bool {
	v := strings.TrimSpace(value)
	if len(v) > 2 && strings.HasPrefix(v, "<") && strings.HasSuffix(v, ">") {
		return true
	}

	upper := strings.ToUpper(v)

	return slices.ContainsFunc(tokens, func(tok string) bool {
		tok = strings.ToUpper(tok)

		return upper == tok || strings.HasPrefix(upper, tok+":") || strings.HasPrefix(upper, tok+" ")
	})
}

// walkScalars visits every scalar under n with its dotted path.
func walkScalars(n *yaml.Node, path string, visit func(field, value string, line int)) {
	switch n.Kind {
	case yaml.ScalarNode:
		visit(path, n.Value, n.Line)
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			walkScalars(n.Content[i+1], join(path, n.Content[i].Value), visit)
		}
	case yaml.SequenceNode:
		for i, c := range n.Content {
			walkScalars(c, fmt.Sprintf("%s[%d]", path, i), visit)
		}
	case yaml.DocumentNode, yaml.AliasNode:
	}
}

func join(path, key string) string {
	if path == "" {
		return key
	}

	return path + "." + key
}

// str is a string parameter; Build has checked its type.
func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}

	return ""
}

func stringList(v any) []string {
	list, ok := v.([]any)
	if !ok {
		return nil
	}

	out := make([]string, 0, len(list))

	for _, x := range list {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}

	return out
}

// Fix never edits a present file: field values need a human (D9), and a
// non-Component file is its team's to change.
func (fileType) Fix(d *catalog.Document, _ []control.Rule) (*catalog.Document, []control.RuleID, error) {
	return d, nil, nil
}

// Render returns the source bytes.
func (fileType) Render(d *catalog.Document) ([]byte, error) { return d.Bytes(), nil }

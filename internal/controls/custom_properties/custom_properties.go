// Package custom_properties is the custom-properties control type
// (DESIGN-0031 § Custom properties, D16; DESIGN-0033 D6): repository
// custom properties derived from catalog-info.yaml. Each managed
// property is repo-guardian's own definition with five facts: source (a
// catalog-info path or an annotation key), normalize, scope (the orgs it
// applies in), key (the GitHub property name) and a value pattern. The
// built-in Owner (spec.owner, entity reference stripped to its name) and
// Component (metadata.name) are always managed.
//
// matches compares each property with its derived value, and the source
// state decides the outcome: a valid Component clears a property whose
// source is gone; an absent file clears the whole managed set; an
// unparseable file is unknown{parse_error} and retains everything; a
// non-Component entity is not_applicable and retains everything.
// value_pattern and defined are never remediated, and each property's
// write is gated on its own valid value and a key the org schema
// defines, so one bad value withholds one write.
package custom_properties

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/donaldgifford/repo-guardian/internal/control"
)

// Name is the type name a catalogue definition names.
const Name = "custom_properties"

// The rule kinds.
const (
	KindMatches      control.RuleKind = "matches"
	KindValuePattern control.RuleKind = "value_pattern"
	KindDefined      control.RuleKind = "defined"
)

// ParamProperty narrows a rule to one managed property, by key; absent,
// the rule covers every managed property.
const ParamProperty = "property"

// The definition parameters. properties = { <key> = { source, normalize,
// scope, key, pattern } } declares or overrides a property;
// annotation_properties = { "<annotation>" = "<key>" } is v1's shorthand
// for a property sourced from an annotation.
const (
	ParamProperties           = "properties"
	ParamAnnotationProperties = "annotation_properties"
)

// The property facts.
const (
	factSource    = "source"
	factNormalize = "normalize"
	factScope     = "scope"
	factKey       = "key"
	factPattern   = "pattern"
)

// SourceAnnotation prefixes a source naming an annotation key, since
// annotation keys hold dots and slashes; any other source is a dotted
// catalog-info path.
const SourceAnnotation = "annotation:"

// The normalize rules.
const (
	NormalizeNone      = "none"
	NormalizeEntityRef = "entity_ref" // "group:default/platform" → "platform"
	NormalizeLower     = "lower"
)

// The built-in properties.
const (
	KeyOwner     = "Owner"
	KeyComponent = "Component"
)

// The catalog-info locations, read and never owned.
const (
	PathYAML = "catalog-info.yaml"
	PathYML  = "catalog-info.yml"
)

var catalogSpec = control.FileSpec{Locations: []string{PathYAML, PathYML}, Canonical: PathYAML}

// validKey is GitHub's property-name charset and length.
var validKey = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,75}$`)

const (
	maxBytes        = 1 << 20
	evidenceVersion = 1
)

// property is one managed property.
type property struct {
	key       string
	source    string
	normalize string
	scope     []string
	pattern   *regexp.Regexp
}

// inScope reports whether the property applies in org.
func (p *property) inScope(org string) bool {
	return len(p.scope) == 0 || slices.ContainsFunc(p.scope, func(s string) bool { return strings.EqualFold(s, org) })
}

// valid reports whether a derived value may be written: a clear always
// may, a value only when it matches the pattern.
func (p *property) valid(v *string) bool {
	return v == nil || p.pattern == nil || p.pattern.MatchString(*v)
}

func builtins() map[string]*property {
	return map[string]*property{
		KeyOwner:     {key: KeyOwner, source: "spec.owner", normalize: NormalizeEntityRef},
		KeyComponent: {key: KeyComponent, source: "metadata.name", normalize: NormalizeNone},
	}
}

// Type is the custom-properties control type.
type Type struct{}

var (
	_ control.Type          = Type{}
	_ control.WorkflowApply = Type{}
)

// Name returns "custom_properties".
func (Type) Name() string { return Name }

// Semantics is 1.
func (Type) Semantics() int { return 1 }

// RuleKinds says only matches is remediable; value_pattern and defined
// report what a write would be refused for.
func (Type) RuleKinds() []control.RuleKindSpec {
	params := []control.ParamSpec{{Name: ParamProperty, Type: control.ParamString}}

	return []control.RuleKindSpec{
		{Kind: KindMatches, Remediable: true, Params: params},
		{Kind: KindValuePattern, Params: params},
		{Kind: KindDefined, Params: params},
	}
}

// Build assembles the managed set: the built-ins, then properties, then
// annotation_properties, and checks every rendered key is valid and
// unique and every rule's property is managed.
func (Type) Build(def control.Definition, _ control.BuildEnv) (control.Control, error) { //nolint:gocritic // hugeParam: Type.Build's signature
	props := builtins()

	if err := declareProperties(props, def.Params[ParamProperties]); err != nil {
		return nil, fmt.Errorf("%s: %s: %w", def.ID, ParamProperties, err)
	}

	if err := declareAnnotations(props, def.Params[ParamAnnotationProperties]); err != nil {
		return nil, fmt.Errorf("%s: %s: %w", def.ID, ParamAnnotationProperties, err)
	}

	c := &propsControl{def: def}

	seen := make(map[string]string)

	for _, name := range sortedKeys(props) {
		p := props[name]
		if p.source == "" {
			continue // a property without a source never enters the managed set
		}

		if !validKey.MatchString(p.key) {
			return nil, fmt.Errorf("%s: property %s: key %q must match %s", def.ID, name, p.key, validKey)
		}

		if other, dup := seen[strings.ToLower(p.key)]; dup {
			return nil, fmt.Errorf("%s: properties %s and %s both render key %q", def.ID, other, name, p.key)
		}

		seen[strings.ToLower(p.key)] = name
		c.props = append(c.props, p)
		c.owned = append(c.owned, control.Resource{Kind: control.KindProperty, Key: p.key})
	}

	for _, r := range def.Rules {
		if key, ok := r.Params[ParamProperty].(string); ok && c.property(key) == nil {
			return nil, fmt.Errorf("%s/%s: %q is not a managed property", def.ID, r.ID, key)
		}
	}

	if def.Apply == control.ApplyWorkflow {
		c.owned = append(c.owned, control.MustCanonical(control.KindFile, WorkflowPath(def)))
	}

	return c, nil
}

func declareProperties(props map[string]*property, v any) error {
	if v == nil {
		return nil
	}

	in, ok := v.(map[string]any)
	if !ok {
		return fmt.Errorf("must be an object")
	}

	for _, name := range sortedKeys(in) {
		facts, ok := in[name].(map[string]any)
		if !ok {
			return fmt.Errorf("property %s must be an object", name)
		}

		p, ok := props[name]
		if !ok {
			p = &property{key: name, normalize: NormalizeNone}
			props[name] = p
		}

		if err := p.apply(facts); err != nil {
			return fmt.Errorf("property %s: %w", name, err)
		}
	}

	return nil
}

// apply sets the facts a declaration names, leaving the rest as they
// were, so a built-in can be given a pattern or a scope alone.
func (p *property) apply(facts map[string]any) error {
	for fact, v := range facts {
		var err error

		switch fact {
		case factSource:
			p.source, err = asString(fact, v)
		case factKey:
			p.key, err = asString(fact, v)
		case factNormalize:
			p.normalize, err = asString(fact, v)
			if err == nil && !slices.Contains([]string{NormalizeNone, NormalizeEntityRef, NormalizeLower}, p.normalize) {
				err = fmt.Errorf("normalize %q: want %q, %q or %q", p.normalize, NormalizeNone, NormalizeEntityRef, NormalizeLower)
			}
		case factPattern:
			p.pattern, err = asPattern(v)
		case factScope:
			p.scope, err = asStrings(fact, v)
		default:
			err = fmt.Errorf("unknown fact %q", fact)
		}

		if err != nil {
			return err
		}
	}

	return nil
}

func declareAnnotations(props map[string]*property, v any) error {
	if v == nil {
		return nil
	}

	in, ok := v.(map[string]any)
	if !ok {
		return fmt.Errorf("must be an object")
	}

	for _, annotation := range sortedKeys(in) {
		key, ok := in[annotation].(string)
		if !ok || key == "" || annotation == "" {
			return fmt.Errorf("annotation %q must map to a property key", annotation)
		}

		if strings.EqualFold(key, KeyOwner) || strings.EqualFold(key, KeyComponent) {
			return fmt.Errorf("annotation %q targets the built-in key %q", annotation, key)
		}

		if _, dup := props[key]; dup {
			return fmt.Errorf("annotation %q targets %q, which is already declared", annotation, key)
		}

		props[key] = &property{key: key, source: SourceAnnotation + annotation, normalize: NormalizeNone}
	}

	return nil
}

func asString(fact string, v any) (string, error) {
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", fact)
	}

	return s, nil
}

func asStrings(fact string, v any) ([]string, error) {
	list, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("%s must be a list of strings", fact)
	}

	out := make([]string, 0, len(list))

	for _, x := range list {
		s, ok := x.(string)
		if !ok {
			return nil, fmt.Errorf("%s must be a list of strings", fact)
		}

		out = append(out, s)
	}

	return out, nil
}

func asPattern(v any) (*regexp.Regexp, error) {
	s, err := asString(factPattern, v)
	if err != nil {
		return nil, err
	}

	re, err := regexp.Compile(s)
	if err != nil {
		return nil, fmt.Errorf("pattern: %w", err)
	}

	return re, nil
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	slices.Sort(out)

	return out
}

// propsControl is the built control.
type propsControl struct {
	def   control.Definition
	props []*property
	owned []control.Resource
}

func (c *propsControl) ID() control.ID { return c.def.ID }

// Owns is property:<key> per managed property, and the workflow file
// under apply = "workflow".
func (c *propsControl) Owns() []control.Resource { return append([]control.Resource(nil), c.owned...) }

// Reads is both catalog-info locations, read and not owned, so a catalog
// edit selects this control as well as catalog_info (D12). The
// properties themselves carry no push signal.
func (*propsControl) Reads() []control.Resource {
	return []control.Resource{control.MustCanonical(control.KindFile, PathYAML), control.MustCanonical(control.KindFile, PathYML)}
}

func (c *propsControl) Rules() []control.Rule { return append([]control.Rule(nil), c.def.Rules...) }

func (c *propsControl) property(key string) *property {
	for _, p := range c.props {
		if p.key == key {
			return p
		}
	}

	return nil
}

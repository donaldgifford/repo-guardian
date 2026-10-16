package catalog

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// MaxBytes is ParseDocument's size cap.
const MaxBytes = 1 << 20

// ErrTooLarge is a document over MaxBytes; it is never parsed.
var ErrTooLarge = fmt.Errorf("catalog-info over the %d-byte limit", MaxBytes)

// ParseError is an unparseable document with the 1-based line the YAML
// parser named, zero when it named none.
type ParseError struct {
	Line int
	Msg  string
}

func (e *ParseError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("catalog-info line %d: %s", e.Line, e.Msg)
	}

	return "catalog-info: " + e.Msg
}

// Document is a parsed catalog-info.yaml that keeps its source bytes and
// node tree beside the decoded Entity, so a control type can report
// where a field is and edit it without re-encoding the file. Only the
// first YAML document is read.
type Document struct {
	src    []byte
	lines  []int // byte offset of each line's start
	root   *yaml.Node
	Entity Entity
}

// Span is where a scalar's source text sits: Line and Column are
// 1-based, [Start, End) are byte offsets into the document. Editable is
// false for block and multi-line scalars, whose text Replace cannot
// substitute safely.
type Span struct {
	Line     int
	Column   int
	Start    int
	End      int
	Editable bool
}

var yamlLine = regexp.MustCompile(`line (\d+)`)

// ParseDocument parses content of at most MaxBytes. Unparseable YAML is
// a *ParseError; so is a document whose root is not a mapping.
func ParseDocument(content []byte) (*Document, error) {
	if len(content) > MaxBytes {
		return nil, ErrTooLarge
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		pe := &ParseError{Msg: err.Error()}
		if m := yamlLine.FindStringSubmatch(err.Error()); m != nil {
			if line, aerr := strconv.Atoi(m[1]); aerr == nil {
				pe.Line = line
			}
		}

		return nil, pe
	}

	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, &ParseError{Line: 1, Msg: "the document is not a mapping"}
	}

	d := &Document{src: content, root: doc.Content[0], lines: lineStarts(content)}

	entity, err := decodeEntity(d.root)
	if err != nil {
		return nil, err
	}

	d.Entity = entity

	return d, nil
}

// decodeEntity reads the Entity fields from the node tree in linear time.
// yaml.v3's Node.Decode checks a mapping for duplicate keys by comparing
// every pair, so a 1 MiB file of distinct keys took minutes (found by
// FuzzParseDocument_RoundTrip). This walks the same four mappings
// Decode would (the root, metadata, its annotations, spec), refuses a
// duplicate key in any of them as Decode does, and takes every scalar
// as its string form, a null as "".
func decodeEntity(root *yaml.Node) (Entity, error) {
	var e Entity

	if err := unique(root); err != nil {
		return Entity{}, err
	}

	for key, dst := range map[string]*string{"apiVersion": &e.APIVersion, "kind": &e.Kind} {
		if err := scalarInto(root, key, dst); err != nil {
			return Entity{}, err
		}
	}

	meta, err := mapping(mappingValue(root, "metadata"))
	if err != nil {
		return Entity{}, err
	}

	if meta != nil {
		if err := scalarInto(meta, "name", &e.Metadata.Name); err != nil {
			return Entity{}, err
		}

		if e.Metadata.Annotations, err = stringMap(mappingValue(meta, "annotations")); err != nil {
			return Entity{}, err
		}
	}

	spec, err := mapping(mappingValue(root, "spec"))
	if err != nil || spec == nil {
		return e, err
	}

	for key, dst := range map[string]*string{"owner": &e.Spec.Owner, "lifecycle": &e.Spec.Lifecycle, "type": &e.Spec.Type, "system": &e.Spec.System} {
		if err := scalarInto(spec, key, dst); err != nil {
			return Entity{}, err
		}
	}

	return e, nil
}

// resolve follows an alias; a nil or null node is nil.
func resolve(n *yaml.Node) *yaml.Node {
	for i := 0; n != nil && n.Kind == yaml.AliasNode && i < 16; i++ {
		n = n.Alias
	}

	if n == nil || (n.Kind == yaml.ScalarNode && n.Tag == "!!null") {
		return nil
	}

	return n
}

// mapping returns n as a uniquely keyed mapping, nil when absent or
// null.
func mapping(n *yaml.Node) (*yaml.Node, error) {
	n = resolve(n)

	switch {
	case n == nil:
		return nil, nil //nolint:nilnil // an absent mapping is not an error
	case n.Kind != yaml.MappingNode:
		return nil, &ParseError{Line: n.Line, Msg: fmt.Sprintf("line %d: want a mapping", n.Line)}
	}

	return n, unique(n)
}

func scalarInto(m *yaml.Node, key string, dst *string) error {
	v, err := scalar(mappingValue(m, key))
	*dst = v

	return err
}

func scalar(n *yaml.Node) (string, error) {
	n = resolve(n)

	switch {
	case n == nil:
		return "", nil
	case n.Kind != yaml.ScalarNode:
		return "", &ParseError{Line: n.Line, Msg: fmt.Sprintf("line %d: want a string", n.Line)}
	}

	return n.Value, nil
}

func stringMap(n *yaml.Node) (map[string]string, error) {
	m, err := mapping(n)
	if err != nil || m == nil {
		return nil, err
	}

	out := make(map[string]string, len(m.Content)/2)

	for i := 0; i+1 < len(m.Content); i += 2 {
		k, err := scalar(m.Content[i])
		if err != nil {
			return nil, err
		}

		if out[k], err = scalar(m.Content[i+1]); err != nil {
			return nil, err
		}
	}

	return out, nil
}

// unique refuses a mapping that defines a key twice, in linear time.
func unique(m *yaml.Node) error {
	seen := make(map[string]int, len(m.Content)/2)

	for i := 0; i+1 < len(m.Content); i += 2 {
		k := m.Content[i]
		if k.Kind != yaml.ScalarNode {
			continue
		}

		if line, dup := seen[k.Value]; dup {
			return &ParseError{Line: k.Line, Msg: fmt.Sprintf("line %d: mapping key %q already defined at line %d", k.Line, k.Value, line)}
		}

		seen[k.Value] = k.Line
	}

	return nil
}

// Bytes returns the source, byte-identical to what was parsed.
func (d *Document) Bytes() []byte { return bytes.Clone(d.src) }

// IsComponent reports whether the entity is a Backstage Component.
func (d *Document) IsComponent() bool {
	return d.Entity.APIVersion == componentAPIVersion && d.Entity.Kind == componentKind
}

// Properties extracts the custom property values exactly as Parse does,
// ErrNotComponent included.
func (d *Document) Properties(annotationProps map[string]string) (*Properties, error) {
	return properties(&d.Entity, annotationProps)
}

// Lookup returns the scalar at a mapping path, such as
// ("metadata", "annotations", "jira/project-key"), with its span. It
// reports false when a key is missing or the node is not a scalar.
func (d *Document) Lookup(path ...string) (value string, span Span, ok bool) {
	n := d.root

	for _, key := range path {
		n = mappingValue(n, key)
		if n == nil {
			return "", Span{}, false
		}
	}

	if n.Kind != yaml.ScalarNode {
		return "", Span{}, false
	}

	return n.Value, d.span(n), true
}

// Replace returns the source with span's text replaced by text, every
// other byte unchanged. text is inserted verbatim, so the caller quotes
// it. It refuses a span that is not Editable.
func (d *Document) Replace(span Span, text string) ([]byte, error) {
	if !span.Editable || span.Start < 0 || span.End > len(d.src) || span.Start > span.End {
		return nil, errors.New("span is not editable")
	}

	out := make([]byte, 0, len(d.src)-(span.End-span.Start)+len(text))
	out = append(out, d.src[:span.Start]...)
	out = append(out, text...)

	return append(out, d.src[span.End:]...), nil
}

func mappingValue(n *yaml.Node, key string) *yaml.Node {
	if n.Kind != yaml.MappingNode {
		return nil
	}

	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}

	return nil
}

// span locates a scalar's source text. yaml.v3 columns count runes.
func (d *Document) span(n *yaml.Node) Span {
	s := Span{Line: n.Line, Column: n.Column}
	if n.Line < 1 || n.Line > len(d.lines) {
		return s
	}

	start := d.lines[n.Line-1]
	for range n.Column - 1 {
		_, size := utf8.DecodeRune(d.src[start:])
		start += size
	}

	s.Start, s.End = start, start

	end, ok := scalarEnd(d.src, start, n)
	if ok {
		s.End, s.Editable = end, true
	}

	return s
}

// scalarEnd finds the end of a single-line scalar's source text: the
// closing quote, or a plain scalar's last non-blank byte before a
// comment or the line end.
func scalarEnd(src []byte, start int, n *yaml.Node) (int, bool) {
	lineEnd := bytes.IndexByte(src[start:], '\n')
	if lineEnd < 0 {
		lineEnd = len(src)
	} else {
		lineEnd += start
	}

	line := src[start:lineEnd]

	switch n.Style {
	case yaml.DoubleQuotedStyle:
		for i := 1; i < len(line); i++ {
			switch line[i] {
			case '\\':
				i++
			case '"':
				return start + i + 1, true
			}
		}
	case yaml.SingleQuotedStyle:
		for i := 1; i < len(line); i++ {
			if line[i] != '\'' {
				continue
			}

			if i+1 < len(line) && line[i+1] == '\'' {
				i++

				continue
			}

			return start + i + 1, true
		}
	case 0:
		text := line
		if i := bytes.Index(text, []byte(" #")); i >= 0 {
			text = text[:i]
		}

		text = bytes.TrimRight(text, " \t\r")
		if string(text) == n.Value {
			return start + len(text), true
		}
	}

	return 0, false
}

func lineStarts(src []byte) []int {
	starts := []int{0}

	for i, b := range src {
		if b == '\n' {
			starts = append(starts, i+1)
		}
	}

	return starts
}

// Root returns the document's root mapping node, for a caller that walks
// the tree. It must not be modified.
func (d *Document) Root() *yaml.Node { return d.root }

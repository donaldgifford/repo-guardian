package dependency_updates

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

// errExtendsNotList is an extends key whose value is not a list of
// strings: it is reported, never rewritten.
var errExtendsNotList = errors.New(`"extends" is not a list of strings`)

// extendsOf returns the extends list of a Renovate config object. A
// JSON5 config is normalised to JSON first.
func extendsOf(content []byte, json5 bool) ([]string, error) {
	if json5 {
		var err error
		if content, err = normaliseJSON5(content); err != nil {
			return nil, err
		}
	}

	var cfg map[string]json.RawMessage
	if err := json.Unmarshal(content, &cfg); err != nil {
		return nil, err
	}

	raw, ok := cfg[keyExtends]
	if !ok {
		return nil, nil
	}

	var list []string
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, errExtendsNotList
	}

	return list, nil
}

// packageRenovate returns whether package.json has a renovate key, and
// that config's extends list.
func packageRenovate(content []byte) (present bool, extends []string, err error) {
	var pkg map[string]json.RawMessage
	if err := json.Unmarshal(content, &pkg); err != nil {
		return false, nil, err
	}

	raw, ok := pkg[ToolRenovate]
	if !ok {
		return false, nil, nil
	}

	extends, err = extendsOf(raw, false)

	return true, extends, err
}

// addExtends is the node-preserving edit D5 asks for: it adds preset to
// the top-level extends list of a JSON object, or adds the key, and
// leaves every other byte as it was.
func addExtends(src []byte, preset string) ([]byte, error) {
	quoted, err := quoteJSON(preset)
	if err != nil {
		return nil, err
	}

	dec := json.NewDecoder(bytes.NewReader(src))

	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, errors.New("the config is not a JSON object")
	}

	openEnd := int(dec.InputOffset())
	firstKey := -1

	for dec.More() {
		keyStart := skip(src, int(dec.InputOffset()), " \t\r\n,")
		if firstKey < 0 {
			firstKey = keyStart
		}

		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}

		if key, ok := tok.(string); ok && key == keyExtends {
			return insertIntoList(src, dec, quoted)
		}

		var skipped json.RawMessage
		if err := dec.Decode(&skipped); err != nil {
			return nil, err
		}
	}

	entry := `"extends": [` + string(quoted) + `]`
	if firstKey < 0 {
		return splice(src, openEnd, entry), nil
	}

	return splice(src, firstKey, entry+","+string(src[openEnd:firstKey])), nil
}

// insertIntoList appends quoted to the list the decoder is positioned
// before, matching a multi-line list's indentation.
func insertIntoList(src []byte, dec *json.Decoder, quoted []byte) ([]byte, error) {
	if tok, err := dec.Token(); err != nil || tok != json.Delim('[') {
		return nil, errExtendsNotList
	}

	open := int(dec.InputOffset()) - 1
	lastStart, lastEnd := -1, -1

	for dec.More() {
		start := skip(src, int(dec.InputOffset()), " \t\r\n,")

		var elem string
		if err := dec.Decode(&elem); err != nil {
			return nil, errExtendsNotList
		}

		lastStart, lastEnd = start, int(dec.InputOffset())
	}

	if _, err := dec.Token(); err != nil {
		return nil, err
	}

	closeAt := int(dec.InputOffset()) - 1

	switch {
	case lastEnd < 0:
		return splice(src, open+1, string(quoted)), nil
	case bytes.IndexByte(src[open:closeAt], '\n') >= 0:
		lineStart := bytes.LastIndexByte(src[:lastStart], '\n') + 1
		indent := src[lineStart:lastStart]

		if len(bytes.TrimLeft(indent, " \t")) != 0 {
			indent = nil
		}

		return splice(src, lastEnd, ",\n"+string(indent)+string(quoted)), nil
	default:
		return splice(src, lastEnd, ", "+string(quoted)), nil
	}
}

// quoteJSON is s as a JSON string, without the HTML escaping
// json.Marshal applies: a preset like "github>org/x" stays readable.
func quoteJSON(s string) ([]byte, error) {
	var buf bytes.Buffer

	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)

	if err := enc.Encode(s); err != nil {
		return nil, err
	}

	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

func skip(src []byte, i int, chars string) int {
	for i < len(src) && bytes.IndexByte([]byte(chars), src[i]) >= 0 {
		i++
	}

	return i
}

func splice(src []byte, at int, text string) []byte {
	return slices.Concat(src[:at:at], []byte(text), src[at:])
}

// normaliseJSON5 rewrites the JSON5 a Renovate config uses into JSON:
// comments dropped, single-quoted strings and bare keys quoted, trailing
// commas removed. Anything else JSON5 allows (hex, Infinity, …) is left
// for the JSON parser to reject.
func normaliseJSON5(src []byte) ([]byte, error) {
	var out bytes.Buffer

	for i := 0; i < len(src); {
		c := src[i]

		switch {
		case c == '"' || c == '\'':
			end, err := copyString(&out, src, i)
			if err != nil {
				return nil, err
			}

			i = end
		case c == '/' && i+1 < len(src) && (src[i+1] == '/' || src[i+1] == '*'):
			i = skipComment(src, i)

			out.WriteByte(' ')
		case c == ',':
			if !trailingComma(src, i) {
				out.WriteByte(c)
			}

			i++
		case isIdentStart(c):
			i = copyIdent(&out, src, i)
		default:
			out.WriteByte(c)
			i++
		}
	}

	return out.Bytes(), nil
}

// trailingComma reports whether the comma at src[i] closes a list or an
// object, which JSON forbids.
func trailingComma(src []byte, i int) bool {
	next := nextSignificant(src, i+1)

	return next < len(src) && (src[next] == '}' || src[next] == ']')
}

// copyIdent writes the identifier at src[i], quoted when it is a key, and
// returns the index after it.
func copyIdent(out *bytes.Buffer, src []byte, i int) int {
	j := i
	for j < len(src) && isIdent(src[j]) {
		j++
	}

	word := src[i:j]
	if next := nextSignificant(src, j); next < len(src) && src[next] == ':' {
		fmt.Fprintf(out, "%q", word)
	} else {
		out.Write(word)
	}

	return j
}

// copyString writes the string starting at src[i] as a JSON string and
// returns the index after it.
func copyString(out *bytes.Buffer, src []byte, i int) (int, error) {
	q := src[i]

	var s []byte

	for j := i + 1; j < len(src); j++ {
		switch src[j] {
		case '\\':
			if j+1 >= len(src) {
				return 0, errors.New("unterminated string")
			}

			if src[j+1] == '\'' {
				s = append(s, '\'')
			} else {
				s = append(s, src[j], src[j+1])
			}

			j++
		case q:
			if q == '"' {
				out.WriteByte('"')
				out.Write(s)
				out.WriteByte('"')
			} else {
				out.WriteByte('"')
				out.Write(bytes.ReplaceAll(s, []byte(`"`), []byte(`\"`)))
				out.WriteByte('"')
			}

			return j + 1, nil
		default:
			s = append(s, src[j])
		}
	}

	return 0, errors.New("unterminated string")
}

func skipComment(src []byte, i int) int {
	if src[i+1] == '/' {
		if end := bytes.IndexByte(src[i:], '\n'); end >= 0 {
			return i + end
		}

		return len(src)
	}

	if end := bytes.Index(src[i+2:], []byte("*/")); end >= 0 {
		return i + 2 + end + 2
	}

	return len(src)
}

// nextSignificant skips whitespace and comments.
func nextSignificant(src []byte, i int) int {
	for i < len(src) {
		switch {
		case src[i] == ' ' || src[i] == '\t' || src[i] == '\r' || src[i] == '\n':
			i++
		case src[i] == '/' && i+1 < len(src) && (src[i+1] == '/' || src[i+1] == '*'):
			i = skipComment(src, i)
		default:
			return i
		}
	}

	return i
}

func isIdentStart(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdent(c byte) bool { return isIdentStart(c) || (c >= '0' && c <= '9') }

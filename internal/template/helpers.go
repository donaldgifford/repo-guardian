package template

import (
	"fmt"
	"os"
	"strings"
	texttemplate "text/template"
	"unicode"
)

// funcMap assembles the curated helper set into the template.FuncMap
// passed to every parsed template. Helpers are unexported because the
// only stable API for callers is the FuncMap binding name; changing a
// Go-side signature without changing the template-side signature does
// not break callers.
func funcMap() texttemplate.FuncMap {
	return texttemplate.FuncMap{
		"env":     envHelper,
		"default": defaultHelper,
		"join":    joinHelper,
		"lower":   strings.ToLower,
		"upper":   strings.ToUpper,
		"title":   titleHelper,
		"propenv": propEnvHelper,
		"yamlq":   yamlQuoteHelper,
	}
}

// propEnvPrefix prefixes every workflow environment variable that
// carries a custom property value in set-custom-properties.tmpl.
const propEnvPrefix = "RG_PROP_"

// propEnvHelper is bound as `propenv` and maps a GitHub custom
// property name to the workflow environment variable carrying its
// value in the generated set-custom-properties workflow (IMPL-0020 A2
// env indirection). Characters outside [A-Za-z0-9_] are replaced with
// '_' so the result is always a valid shell identifier — GitHub
// property names may also contain '.' and '-'.
//
// Template-side signature: {{ propenv "JiraProject" }} -> "RG_PROP_JiraProject".
//
// The mapping is not injective: distinct property names such as "a.b"
// and "a_b" collapse to the same env name. Property names are
// operator-controlled policy config validated at load time, so a
// collision is a visible misconfiguration (duplicate env keys in the
// generated workflow), not an injection vector.
func propEnvHelper(name string) string {
	var b strings.Builder

	b.Grow(len(propEnvPrefix) + len(name))
	b.WriteString(propEnvPrefix)

	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}

	return b.String()
}

// yamlQuoteHelper is bound as `yamlq` and renders s as a double-quoted
// YAML flow scalar on a single line: quotes, backslashes, newlines,
// tabs, and every other control or line-separator character are
// escaped, so the value can neither terminate the scalar nor break the
// surrounding document's structure or indentation (IMPL-0020 A2
// YAML-safe emission).
//
// Values containing the GitHub Actions expression opener "${{" are
// rejected: the Actions runner evaluates expressions after YAML
// parsing, so no amount of YAML escaping can neutralize one. Refusing
// to render fails the reconcile loudly instead of baking a
// repo-controlled expression into the generated workflow.
//
// Template-side signature: {{ yamlq $value }} -> `"..."`.
func yamlQuoteHelper(s string) (string, error) {
	if strings.Contains(s, "${{") {
		return "", fmt.Errorf("yamlq: value %q contains a GitHub Actions expression opener and cannot be rendered safely", s)
	}

	var b strings.Builder

	b.Grow(len(s) + 2)
	b.WriteByte('"')

	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case unicode.IsControl(r), r == '\u2028', r == '\u2029':
			// C0/C1 controls (including NEL U+0085) and the Unicode
			// line separators are line breaks or invisibles in YAML;
			// escape them so they cannot fold or split the scalar.
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}

	b.WriteByte('"')

	return b.String(), nil
}

// envHelper is bound as `env` and reads a process environment variable
// by name, returning the empty string when the variable is unset.
//
// Template-side signature: {{ env "VAR_NAME" }} -> string.
//
// See the security-posture section in the package doc comment.
func envHelper(name string) string {
	return os.Getenv(name)
}

// defaultHelper is bound as `default` and returns fallback when value
// is the empty string; otherwise returns value unchanged.
//
// Template-side signature: {{ default "fallback" .Field }} -> string.
//
// Note that template helper argument order is reversed from the typical
// "value, fallback" Go convention so the helper composes naturally with
// pipelines: {{ .Field | default "fallback" }}.
func defaultHelper(fallback, value string) string {
	if value == "" {
		return fallback
	}

	return value
}

// joinHelper is bound as `join` and concatenates a string slice with
// the given separator.
//
// Template-side signature: {{ join ", " .Files }} -> string.
//
// Argument order is (sep, items), reversed from stdlib
// strings.Join(elems, sep) so the helper composes naturally with
// template pipelines: {{ .Files | join ", " }}.
//
// A nil or empty slice renders as the empty string.
func joinHelper(sep string, items []string) string {
	return strings.Join(items, sep)
}

// titleHelper is bound as `title` and uppercases the first ASCII letter
// of each whitespace-delimited word. Subsequent letters in each word are
// left unchanged so org names like "ACMECorp" do not get coerced to
// "Acmecorp".
//
// Template-side signature: {{ title "hello world" }} -> "Hello World".
//
// Inputs are assumed ASCII; repo and org names on GitHub fit that
// constraint. For non-ASCII inputs the helper degrades to passing the
// rune through unchanged (matching Go's deprecated strings.Title
// semantics for the cases templates encounter in practice).
func titleHelper(s string) string {
	if s == "" {
		return ""
	}

	words := strings.Fields(s)
	for i, w := range words {
		if w == "" {
			continue
		}

		runes := []rune(w)

		first := runes[0]
		if first >= 'a' && first <= 'z' {
			first -= 'a' - 'A'
		}

		runes[0] = first
		words[i] = string(runes)
	}

	return strings.Join(words, " ")
}

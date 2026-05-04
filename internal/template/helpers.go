package template

import (
	"os"
	"strings"
	texttemplate "text/template"
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
	}
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

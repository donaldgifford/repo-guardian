package monitoring

import (
	"fmt"
	"regexp"
	"strings"
)

// matcherList is a comma-separated list of PromQL label matchers
// without braces: `namespace="repo-guardian"`, `job=~"a|b", env!="dev"`.
var matcherList = regexp.MustCompile(
	`^\s*[a-zA-Z_][a-zA-Z0-9_]*\s*(=~|!~|!=|=)\s*"(?:[^"\\]|\\.)*"\s*` +
		`(,\s*[a-zA-Z_][a-zA-Z0-9_]*\s*(=~|!~|!=|=)\s*"(?:[^"\\]|\\.)*"\s*)*$`)

// ValidateMatchers refuses a selector ScopePromQL cannot splice in
// safely. Empty is valid and means "no scoping".
func ValidateMatchers(matchers string) error {
	if matchers == "" || matcherList.MatchString(matchers) {
		return nil
	}

	return fmt.Errorf(
		"prometheus selector %q is not a list of label matchers; want e.g. namespace=\"repo-guardian\" (no braces, double-quoted values)",
		matchers)
}

// labelListKeywords are followed by a parenthesised label list, whose
// names must not be mistaken for metric names.
var labelListKeywords = map[string]bool{
	"by": true, "without": true, "on": true, "ignoring": true, "group_left": true, "group_right": true,
}

// keywords are never metric names, whether or not a '(' follows. The
// aggregation operators belong here because `sum by (x) (...)` puts
// `by`, not '(', after the operator.
var keywords = map[string]bool{
	"and": true, "or": true, "unless": true, "bool": true, "offset": true, "inf": true, "nan": true,
	"sum": true, "min": true, "max": true, "avg": true, "count": true, "group": true,
	"stddev": true, "stdvar": true, "topk": true, "bottomk": true, "quantile": true,
	"count_values": true, "limitk": true, "limit_ratio": true,
}

// ScopePromQL adds matchers to every series selector in expr.
//
// A bare metric gains `{matchers}`; an existing matcher block has them
// prepended. Empty matchers return expr unchanged, byte for byte, which
// is what keeps the committed generated tier stable.
//
// A tokenizer rather than a PromQL parser: every expression it sees is
// authored in this repository, and prometheus/prometheus's parser would
// drag its whole module in for one tree walk. It understands exactly
// what the generated expressions use — strings, range and offset
// durations, label lists after by/without/on/ignoring/group_*, function
// calls and the aggregation keywords. Dashboard variables such as
// label_values() take a metric and a label name side by side, so they
// must scope the metric alone rather than pass through here.
func ScopePromQL(expr, matchers string) string {
	if matchers == "" {
		return expr
	}

	s := &scoper{expr: expr, matchers: strings.TrimSpace(matchers)}
	s.run()

	return s.out.String()
}

type scoper struct {
	expr     string
	matchers string
	out      strings.Builder
}

func (s *scoper) run() {
	for i := 0; i < len(s.expr); {
		c := s.expr[i]

		switch {
		case c == '"' || c == '\'' || c == '`':
			i = s.copyTo(i, stringEnd(s.expr, i))
		case c == '#':
			i = s.copyTo(i, indexFrom(s.expr, i, '\n'))
		case c == '[':
			i = s.copyTo(i, indexFrom(s.expr, i, ']'))
		case c == '{':
			i = s.braces(i)
		case isDigit(c) || (c == '.' && i+1 < len(s.expr) && isDigit(s.expr[i+1])):
			i = s.copyTo(i, numberEnd(s.expr, i))
		case isIdentStart(c):
			i = s.identifier(i)
		default:
			s.out.WriteByte(c)
			i++
		}
	}
}

// copyTo writes expr[i:j] unchanged and returns j.
func (s *scoper) copyTo(i, j int) int {
	s.out.WriteString(s.expr[i:j])

	return j
}

// numberEnd returns the index past the number or duration (0.99, 5m,
// 1h30m) starting at i.
func numberEnd(s string, i int) int {
	for i < len(s) && (isIdentChar(s[i]) || s[i] == '.') {
		i++
	}

	return i
}

// identifier handles a word starting at i and returns the next index.
func (s *scoper) identifier(i int) int {
	j := i
	for j < len(s.expr) && isIdentChar(s.expr[j]) {
		j++
	}

	word := s.expr[i:j]
	next := skipSpace(s.expr, j)
	s.out.WriteString(word)

	switch {
	case labelListKeywords[strings.ToLower(word)]:
		if next < len(s.expr) && s.expr[next] == '(' {
			end := indexFrom(s.expr, next, ')')
			s.out.WriteString(s.expr[j:end])

			return end
		}

		return j
	case keywords[strings.ToLower(word)]:
		return j
	case next < len(s.expr) && s.expr[next] == '(':
		// A function call.
		return j
	case next < len(s.expr) && s.expr[next] == '{':
		s.out.WriteString(s.expr[j:next])

		return s.braces(next)
	default:
		s.out.WriteString("{" + s.matchers + "}")

		return j
	}
}

// braces copies the matcher block opening at i with the scope matchers
// prepended, and returns the index past its closing brace.
func (s *scoper) braces(i int) int {
	s.out.WriteString("{" + s.matchers)

	j := i + 1
	if k := skipSpace(s.expr, j); k < len(s.expr) && s.expr[k] != '}' {
		s.out.WriteString(", ")
		j = k
	}

	for j < len(s.expr) {
		c := s.expr[j]

		switch c {
		case '"', '\'', '`':
			k := stringEnd(s.expr, j)
			s.out.WriteString(s.expr[j:k])
			j = k
		case '}':
			s.out.WriteByte(c)

			return j + 1
		default:
			s.out.WriteByte(c)
			j++
		}
	}

	return j
}

// stringEnd returns the index past the string literal opening at i.
func stringEnd(s string, i int) int {
	quote := s[i]

	for j := i + 1; j < len(s); j++ {
		switch {
		case s[j] == '\\' && quote != '`':
			j++
		case s[j] == quote:
			return j + 1
		}
	}

	return len(s)
}

// indexFrom returns the index past the first c at or after i, or
// len(s).
func indexFrom(s string, i int, c byte) int {
	if k := strings.IndexByte(s[i:], c); k >= 0 {
		return i + k + 1
	}

	return len(s)
}

func skipSpace(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
		i++
	}

	return i
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isIdentStart(c byte) bool {
	return c == '_' || c == ':' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentChar(c byte) bool { return isIdentStart(c) || isDigit(c) }

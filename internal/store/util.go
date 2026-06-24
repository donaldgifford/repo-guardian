// Package store utility helpers shared by callers writing into
// the persistent state. Currently exposes a single Truncate helper
// for clipping error strings before they enter the RepoState.LastError
// field (Postgres TEXT is unbounded but the operator-facing
// dashboards / log lines need predictable widths).
package store

// Truncate returns s clipped to at most maxRunes runes. When s is
// longer it is shortened to maxRunes-1 runes with a single '…'
// (Unicode HORIZONTAL ELLIPSIS) appended so callers can tell at a
// glance that the string was clipped. maxRunes ≤ 0 returns "".
//
// Operates on runes, not bytes, so multibyte UTF-8 sequences (e.g.
// non-ASCII paths in error messages) are never split mid-codepoint.
func Truncate(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}

	rs := []rune(s)
	if len(rs) <= maxRunes {
		return s
	}

	return string(rs[:maxRunes-1]) + "…"
}

package report

import (
	"fmt"
	"strings"
	"time"
)

// mdcell makes a string safe to place in a markdown table cell.
//
// Rule names come from operator-authored HCL and repository names come
// from the API, so neither is guaranteed free of the one character that
// breaks a table. A stray pipe silently shifts every column to its
// right, which turns a compliance report into a misleading one rather
// than an obviously broken one.
//
// This is table hygiene, not injection defence — the output is markdown
// read by humans, not a shell or a workflow file — but it is the same
// discipline as the IMPL-0020 A2 helpers and costs one function.
func mdcell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")

	return strings.TrimSpace(s)
}

// renderPercent formats a rule's compliance for display.
//
// "n/a" rather than a number when nothing was tracked: a rule evaluated
// against no repository is unmeasured, and printing 100% for it would
// be the most misleading cell in the document.
func renderPercent(r RuleLine) string { //nolint:gocritic // value receiver: text/template cannot address a range variable
	value, ok := r.CompliantPercent()
	if !ok {
		return "n/a"
	}

	return fmt.Sprintf("%.1f%%", value)
}

// renderSince formats the date a repository started failing.
func renderSince(t *time.Time) string {
	if t == nil {
		return "—"
	}

	return t.UTC().Format("2006-01-02")
}

// trendNewLabel is what a rule with no snapshot to compare against
// renders as. Never "0" or "no change" — those claim a measurement
// that was never taken.
const trendNewLabel = "new"

// renderTrend formats a rule's movement against its own last snapshot.
//
// Each cell carries the date it was compared against, because different
// rules can be compared against different runs: a rule that was
// disabled when the last snapshot was taken is compared against the
// last run that actually measured it. A delta with an unstated baseline
// invites the reader to assume they are all from yesterday.
func renderTrend(r RuleLine) string { //nolint:gocritic // value receiver: text/template cannot address a range variable
	if r.Trend == TrendUnknown {
		return trendNewLabel
	}

	when := r.ComparedAt.UTC().Format("2006-01-02")

	switch r.Trend {
	case TrendFlat:
		return fmt.Sprintf("no change since %s", when)
	case TrendImproved:
		return fmt.Sprintf("%d fewer since %s", -r.Delta, when)
	case TrendWorsened:
		return fmt.Sprintf("%d more since %s", r.Delta, when)
	case TrendUnknown:
		return trendNewLabel
	default:
		return trendNewLabel
	}
}

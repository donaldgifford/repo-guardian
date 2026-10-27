package report

import (
	"fmt"
	"strings"
)

// mdcell makes a string safe to place in a markdown table cell.
//
// Control slugs and exclusion reasons come from operator-authored HCL
// and repository names come from the API, so neither is guaranteed free of the characters that
// break a table: a pipe splits a cell, a backtick opens a code span
// that can swallow the rest of the row. A stray pipe silently shifts every column to its
// right, which turns a compliance report into a misleading one rather
// than an obviously broken one.
//
// This is table hygiene, not injection defence — the output is markdown
// read by humans, not a shell or a workflow file — but it is the same
// discipline as the IMPL-0020 A2 helpers and costs one function.
func mdcell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "`", "\\`")
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")

	return strings.TrimSpace(s)
}

// noData is what an empty denominator renders as. Never 100% and never
// 0%: a control that measures nothing is unmeasured, and printing a
// number for it would be the most misleading cell in the document.
const noData = "no data"

// renderPercent formats a percentage computed by the store.
func renderPercent(p *float64) string {
	if p == nil {
		return noData
	}

	return fmt.Sprintf("%.1f%%", *p)
}

// allControls is the control cell of an exclusion that removes the whole
// repository from the policy.
const allControls = "all controls"

// renderControl formats an exclusion's control cell.
func renderControl(e Exclusion) string { //nolint:gocritic // value receiver: text/template cannot address a range variable
	if e.Control == "" {
		return allControls
	}

	return mdcell(e.Control)
}

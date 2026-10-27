package policy

import (
	"maps"

	"github.com/donaldgifford/repo-guardian/internal/control"
)

// Decision is the policy decision behind an assignment (DESIGN-0030 D17).
type Decision string

// The decisions.
const (
	DecisionBaseline Decision = "baseline"
	DecisionReplaced Decision = "replaced"
	DecisionOrgAdded Decision = "org_added"
	DecisionExcluded Decision = "excluded"
)

// sourceEnterprise is the enterprise layer's source.
const sourceEnterprise = "enterprise"

// row is one slug's assignment while layers apply (algorithm steps 1–3).
type row struct {
	id       control.ID
	excluded bool
	decision Decision
	// baseline is the enterprise slug this row fulfils; empty when
	// org-added.
	baseline    string
	source      string
	sourceBlock string
	// replaced is the source of the replace that produced the row.
	replaced   string
	excludedBy string
	reason     string
	ordinal    int
}

// rows is the per-slug assignment set: one row per slug (step 3).
type rows struct {
	bySlug map[string]*row
	// baseline is the enterprise's slugs.
	baseline map[string]bool
	next     int
}

// newBaseline is step 1: every enterprise control active, decision
// baseline, baseline_control its own slug, ordinals in list order.
func newBaseline(ent *enterprisePolicy) *rows {
	rs := &rows{bySlug: make(map[string]*row), baseline: make(map[string]bool)}

	for _, r := range ent.controls {
		rs.baseline[r.id.Slug] = true
	}

	for _, r := range ent.controls {
		rs.add(r.id, sourceEnterprise, "")
	}

	return rs
}

// clone returns an independent copy.
func (rs *rows) clone() *rows {
	out := &rows{bySlug: make(map[string]*row, len(rs.bySlug)), baseline: maps.Clone(rs.baseline), next: rs.next}
	for slug, r := range rs.bySlug {
		c := *r
		out.bySlug[slug] = &c
	}

	return out
}

// issueKind is a layer operation that did nothing.
type issueKind int

const (
	// issueReplaceMissing is a replace whose source is not active.
	issueReplaceMissing issueKind = iota + 1
	// issueExcludeNoop is an exclude of a control not active.
	issueExcludeNoop
)

// issue is an operation that did nothing, for validation.
type issue struct {
	kind issueKind
	op   *op
}

// apply is step 2 for one layer: its operations in file order, an
// exclusion beating an addition of the same control within the layer.
// It returns the operations that did nothing.
func (rs *rows) apply(ly *layer, source, block string) []issue {
	excludedHere := make(map[control.ID]bool)

	for i := range ly.ops {
		if o := &ly.ops[i]; o.kind == opExclude {
			excludedHere[o.id] = true
		}
	}

	var issues []issue

	for i := range ly.ops {
		o := &ly.ops[i]

		switch o.kind {
		case opAdd:
			if !excludedHere[o.id] {
				rs.add(o.id, source, block)
			}
		case opReplace:
			if !rs.replace(o, source, block) {
				issues = append(issues, issue{kind: issueReplaceMissing, op: o})
			}
		case opExclude:
			if !rs.exclude(o, source, block) {
				issues = append(issues, issue{kind: issueExcludeNoop, op: o})
			}
		}
	}

	return issues
}

// active returns the row for id when it is the active row of its slug.
func (rs *rows) active(id control.ID) *row {
	r := rs.bySlug[id.Slug]
	if r == nil || r.excluded || r.id != id {
		return nil
	}

	return r
}

// add makes id the active row of its slug. A slug the enterprise names,
// or one whose row already carries lineage, is restored to the baseline;
// any other is org-added. An already active id is left untouched.
func (rs *rows) add(id control.ID, source, block string) {
	prev := rs.bySlug[id.Slug]
	if rs.active(id) != nil {
		return
	}

	r := &row{id: id, source: source, sourceBlock: block, decision: DecisionOrgAdded}

	switch {
	case prev != nil && prev.baseline != "":
		r.baseline = prev.baseline
	case rs.baseline[id.Slug]:
		r.baseline = id.Slug
	}

	if r.baseline != "" {
		r.decision = DecisionBaseline
	}

	if prev != nil {
		r.ordinal = prev.ordinal
	} else {
		rs.next++
		r.ordinal = rs.next
	}

	rs.bySlug[id.Slug] = r
}

// replace swaps an active source for its replacement, which inherits the
// source's lineage and ordinal. It reports false when the source is not
// active.
func (rs *rows) replace(o *op, source, block string) bool {
	src := rs.active(o.id)
	if src == nil {
		return false
	}

	delete(rs.bySlug, o.id.Slug)

	rs.bySlug[o.with.Slug] = &row{
		id: o.with, decision: DecisionReplaced, baseline: src.baseline,
		source: source, sourceBlock: block, replaced: o.id.String(), reason: o.reason, ordinal: src.ordinal,
	}

	return true
}

// exclude marks an active control excluded with its reason. It reports
// false when the control is not active.
func (rs *rows) exclude(o *op, source, block string) bool {
	r := rs.active(o.id)
	if r == nil {
		return false
	}

	r.excluded = true
	r.decision = DecisionExcluded
	r.excludedBy = source
	r.reason = o.reason
	r.source, r.sourceBlock = source, block

	return true
}

// orgSource is an org layer's source.
func orgSource(org string) string { return "org:" + org }

// blockSource is a repos block's source.
func blockSource(org, label string) string { return "org:" + org + "/repos[" + label + "]" }

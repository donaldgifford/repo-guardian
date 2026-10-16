package policy

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/hashicorp/hcl/v2"

	"github.com/donaldgifford/repo-guardian/internal/control"
)

// minReopenAfter is the shortest reopen_after DESIGN-0030 allows.
const minReopenAfter = time.Hour

// validator checks a decoded policy against DESIGN-0030's validation list.
// Errors fail load; warnings are logged once.
type validator struct {
	defs     map[control.ID]*definition
	ent      *enterprisePolicy
	orgs     []*orgPolicy
	errs     []error
	warnings []Warning
}

func (v *validator) errorf(r hcl.Range, format string, args ...any) {
	v.errs = append(v.errs, fmt.Errorf("%s: %s", at(r), fmt.Sprintf(format, args...)))
}

func (v *validator) warnf(r hcl.Range, format string, args ...any) {
	v.warnings = append(v.warnings, Warning{File: r.Filename, Line: r.Start.Line, Msg: fmt.Sprintf(format, args...)})
}

// validate runs every check and returns the joined errors.
func (v *validator) validate() error {
	v.checkRefs()
	v.checkModes()
	v.checkRemediation(&v.ent.remed)

	for _, org := range v.orgs {
		v.checkRemediation(&org.remed)
		v.checkReasons(org)
		v.checkRepos(org)
		v.checkLayers(org)
		v.checkOverrides(org)
		v.checkSameSpecificity(org)
		v.checkReAdds(org)
	}

	if len(v.errs) == 0 {
		v.checkOwnership()
	}

	return errors.Join(v.errs...)
}

// checkRefs: every control reference names a catalogue definition.
func (v *validator) checkRefs() {
	for _, r := range v.ent.controls {
		v.checkRef(r.id, r.pos)
	}

	for _, org := range v.orgs {
		for _, ly := range org.layers() {
			for i := range ly.ops {
				o := &ly.ops[i]
				v.checkRef(o.id, o.pos)

				if o.kind == opReplace {
					v.checkRef(o.with, o.pos)
				}
			}

			for i := range ly.overrides {
				v.checkRef(ly.overrides[i].id, ly.overrides[i].pos)
			}
		}
	}
}

func (v *validator) checkRef(id control.ID, r hcl.Range) {
	if _, ok := v.defs[id]; ok {
		return
	}

	var versions []string

	for d := range v.defs {
		if d.Slug == id.Slug {
			versions = append(versions, d.String())
		}
	}

	if len(versions) == 0 {
		v.errorf(r, "unknown control %s: no %q in the catalogue", id, id.Slug)

		return
	}

	slices.Sort(versions)
	v.errorf(r, "unknown control version %s: the catalogue has %s", id, strings.Join(versions, ", "))
}

// checkModes: every mode is evaluate or remediate.
func (v *validator) checkModes() {
	check := func(m *modeDecl) {
		if m != nil && m.mode != ModeEvaluate && m.mode != ModeRemediate {
			v.errorf(m.pos, "mode %q: want %q or %q", m.mode, ModeEvaluate, ModeRemediate)
		}
	}

	check(v.ent.mode)

	for _, org := range v.orgs {
		for _, ly := range org.layers() {
			check(ly.mode)

			for i := range ly.overrides {
				check(&ly.overrides[i].mode)
			}
		}
	}
}

// checkRemediation: max_open_prs at least 1, reopen_after a duration of
// at least 1h.
func (v *validator) checkRemediation(r *remediationDecl) {
	if r.maxOpenPRs != nil && *r.maxOpenPRs < 1 {
		v.errorf(r.maxOpenPRsPos, "remediation.max_open_prs = %d: must be at least 1", *r.maxOpenPRs)
	}

	if r.reopenAfter == nil {
		return
	}

	d, err := time.ParseDuration(*r.reopenAfter)
	if err != nil || d < minReopenAfter {
		v.errorf(r.reopenAfterPos, "remediation.reopen_after = %q: must be a duration of at least %s", *r.reopenAfter, minReopenAfter)
	}
}

// checkReasons: every exclude, replace and exclude_repos carries a
// non-empty reason (D2, D17).
func (v *validator) checkReasons(org *orgPolicy) {
	for _, ly := range org.layers() {
		for i := range ly.ops {
			if o := &ly.ops[i]; o.kind != opAdd && strings.TrimSpace(o.reason) == "" {
				v.errorf(o.pos, "%s %s needs a reason", opName(o.kind), o.id)
			}
		}
	}

	for i := range org.excludeRepos {
		if er := &org.excludeRepos[i]; strings.TrimSpace(er.reason) == "" {
			v.errorf(er.pos, "exclude_repos needs a reason")
		}
	}
}

func opName(k opKind) string {
	switch k {
	case opReplace:
		return blockReplace
	case opExclude:
		return blockExclude
	default:
		return attrControls
	}
}

// checkRepos: repos labels are unique within the org; every glob parses.
func (v *validator) checkRepos(org *orgPolicy) {
	seen := make(map[string]hcl.Range)

	for i := range org.repos {
		rb := &org.repos[i]
		if first, dup := seen[rb.label]; dup {
			v.errorf(rb.pos, "repos block %q is declared twice in org %s (first at %s)", rb.label, org.name, at(first))
		}

		seen[rb.label] = rb.pos
		v.checkGlobs(rb.match, rb.pos)
	}

	for i := range org.excludeRepos {
		v.checkGlobs(org.excludeRepos[i].match, org.excludeRepos[i].pos)
	}
}

func (v *validator) checkGlobs(patterns []string, r hcl.Range) {
	for _, p := range patterns {
		if _, err := path.Match(p, ""); err != nil {
			v.errorf(r, "match pattern %q: %v", p, err)
		}
	}
}

// checkLayers simulates the layers: a replace whose source is not in the
// current set, or whose controls share no owned resource, is an error;
// an exclusion of a control not assigned at that layer is a warning. For
// a repos block the current set is the org layer's result plus whatever
// an earlier block adds, since which earlier blocks a repository meets is
// unknown at load.
func (v *validator) checkLayers(org *orgPolicy) {
	orgRows := newBaseline(v.ent)
	v.report(orgRows.apply(&org.layer, orgSource(org.name), ""))

	for i := range org.repos {
		cur := orgRows.clone()

		for j := range i {
			cur.addOnly(&org.repos[j].layer, blockSource(org.name, org.repos[j].label), org.repos[j].label)
		}

		rb := &org.repos[i]
		v.report(cur.apply(&rb.layer, blockSource(org.name, rb.label), rb.label))
	}

	for _, ly := range org.layers() {
		for i := range ly.ops {
			if o := &ly.ops[i]; o.kind == opReplace {
				v.checkReplaceOwnership(o)
			}
		}
	}
}

func (v *validator) report(issues []issue) {
	for i := range issues {
		is := &issues[i]

		switch is.kind {
		case issueReplaceMissing:
			v.errorf(is.op.pos, "replace %s: the source is not assigned at this layer", is.op.id)
		case issueExcludeNoop:
			v.warnf(is.op.pos, "exclude %s: not assigned at this layer, so the exclusion does nothing", is.op.id)
		}
	}
}

// addOnly applies a layer's additions (and the controls its replaces
// bring in) without its removals.
func (rs *rows) addOnly(ly *layer, source, block string) {
	for i := range ly.ops {
		o := &ly.ops[i]

		switch o.kind {
		case opAdd:
			rs.add(o.id, source, block)
		case opReplace:
			if rs.active(o.id) == nil {
				rs.add(o.with, source, block)
			} else {
				rs.replace(o, source, block)
			}
		case opExclude:
		}
	}
}

// checkReplaceOwnership: a replace's two controls own a common resource.
func (v *validator) checkReplaceOwnership(o *op) {
	from, okFrom := v.defs[o.id]
	with, okWith := v.defs[o.with]

	if !okFrom || !okWith {
		return // checkRefs reported it
	}

	for _, r := range from.ctl.Owns() {
		if slices.Contains(with.ctl.Owns(), r) {
			return
		}
	}

	v.errorf(o.pos, "replace %s with %s: the two controls own no resource in common", o.id, o.with)
}

// checkOverrides: a control {} override names a control some layer of
// the org assigns.
func (v *validator) checkOverrides(org *orgPolicy) {
	assigned := make(map[control.ID]bool)

	for _, r := range v.ent.controls {
		assigned[r.id] = true
	}

	for _, ly := range org.layers() {
		for i := range ly.ops {
			switch o := &ly.ops[i]; o.kind {
			case opAdd:
				assigned[o.id] = true
			case opReplace:
				assigned[o.with] = true
			case opExclude:
			}
		}
	}

	for _, ly := range org.layers() {
		for i := range ly.overrides {
			if ov := &ly.overrides[i]; !assigned[ov.id] {
				v.errorf(ov.pos, "control %s override: no layer of org %s assigns %s", ov.id, org.name, ov.id)
			}
		}
	}
}

// checkSameSpecificity warns on two mode declarations at the same
// specificity with different values (algorithm step 4): a repeated
// override in one scope, or two overlapping repos blocks.
func (v *validator) checkSameSpecificity(org *orgPolicy) {
	for _, ly := range org.layers() {
		v.checkRepeatedOverrides(ly)
	}

	for i := range org.repos {
		for j := i + 1; j < len(org.repos); j++ {
			if a, b := &org.repos[i], &org.repos[j]; overlaps(a.match, b.match) {
				v.checkBlockModes(a, b)
			}
		}
	}
}

// checkRepeatedOverrides warns on one control's mode declared twice in a
// scope with different values.
func (v *validator) checkRepeatedOverrides(ly *layer) {
	for i := range ly.overrides {
		for j := i + 1; j < len(ly.overrides); j++ {
			a, b := &ly.overrides[i], &ly.overrides[j]
			if a.id == b.id && a.mode.mode != b.mode.mode {
				v.warnf(b.pos, "control %s mode is declared twice in one scope (%s at %s); the later one wins", b.id, a.mode.mode, at(a.pos))
			}
		}
	}
}

// checkBlockModes warns when two overlapping repos blocks declare
// different modes, block-wide or for one control.
func (v *validator) checkBlockModes(a, b *reposBlock) {
	if a.layer.mode != nil && b.layer.mode != nil && a.layer.mode.mode != b.layer.mode.mode {
		v.warnf(b.layer.mode.pos, "repos %q and %q may match one repository with different modes; the later one wins", a.label, b.label)
	}

	for i := range a.layer.overrides {
		for j := range b.layer.overrides {
			oa, ob := &a.layer.overrides[i], &b.layer.overrides[j]
			if oa.id == ob.id && oa.mode.mode != ob.mode.mode {
				v.warnf(ob.pos, "repos %q and %q may match one repository with different modes for %s; the later one wins", a.label, b.label, ob.id)
			}
		}
	}
}

// checkReAdds warns when a repos block re-adds a control an earlier
// block excluded for an overlapping match: swapping the blocks changes
// the result.
func (v *validator) checkReAdds(org *orgPolicy) {
	for i := range org.repos {
		for j := i + 1; j < len(org.repos); j++ {
			a, b := &org.repos[i], &org.repos[j]
			if !overlaps(a.match, b.match) {
				continue
			}

			for k := range a.layer.ops {
				ex := &a.layer.ops[k]
				if ex.kind != opExclude {
					continue
				}

				for m := range b.layer.ops {
					if add := &b.layer.ops[m]; add.kind == opAdd && ex.id == add.id {
						v.warnf(
							add.pos,
							"repos %q re-adds %s, which repos %q excluded for an overlapping match; block order decides",
							b.label,
							add.id,
							a.label,
						)
					}
				}
			}
		}
	}
}

// overlaps reports whether two glob lists may match one name: a shared
// pattern, or one pattern matching the other as a literal. It is a
// conservative guess, used only for warnings.
func overlaps(a, b []string) bool {
	for _, p := range a {
		for _, q := range b {
			if p == q || globMatch(p, q) || globMatch(q, p) {
				return true
			}
		}
	}

	return false
}

func globMatch(pattern, name string) bool {
	ok, err := path.Match(pattern, name)

	return err == nil && ok
}

// layers returns the org layer and then each repos block's layer.
func (o *orgPolicy) layers() []*layer {
	out := make([]*layer, 0, 1+len(o.repos))
	out = append(out, &o.layer)

	for i := range o.repos {
		out = append(out, &o.repos[i].layer)
	}

	return out
}

// UnmatchedReposBlocks is DESIGN-0030's after-discovery warning: the
// repos blocks of org that match none of names.
func (s *Snapshot) UnmatchedReposBlocks(org string, names []string) []Warning {
	var out []Warning

	for _, o := range s.orgs {
		if !strings.EqualFold(o.name, org) {
			continue
		}

		for i := range o.repos {
			rb := &o.repos[i]
			if !slices.ContainsFunc(names, func(n string) bool { return matchesAny(rb.match, n) }) {
				out = append(
					out,
					Warning{File: rb.pos.Filename, Line: rb.pos.Start.Line, Msg: fmt.Sprintf("repos %q matches no known repository", rb.label)},
				)
			}
		}
	}

	return out
}

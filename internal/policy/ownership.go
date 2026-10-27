package policy

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"

	"github.com/donaldgifford/repo-guardian/internal/control"
)

// checkOwnership is the conservative load-time write-set check: for every
// org it resolves the org layer alone, with each repos block, and with
// each pair of blocks in file order, and warns on two active controls
// owning one resource. An onboarded org with no file gets the baseline.
// These are warnings: which blocks a repository meets is unknown at load,
// and Resolve's runtime check (algorithm step 6) is the authority.
func (v *validator) checkOwnership() {
	seen := make(map[string]bool)

	for _, name := range v.ent.orgs {
		if slices.ContainsFunc(v.orgs, func(o *orgPolicy) bool { return strings.EqualFold(o.name, name) }) {
			continue
		}

		v.warnOverlaps(newBaseline(v.ent), name, v.ent.orgsPos, seen)
	}

	for _, org := range v.orgs {
		orgRows := newBaseline(v.ent)
		orgRows.apply(&org.layer, orgSource(org.name), "")
		v.warnOverlaps(orgRows, org.name, org.pos, seen)

		for i := range org.repos {
			one := orgRows.clone()
			one.applyBlock(org, i)
			v.warnOverlaps(one, org.name, org.repos[i].pos, seen)

			for j := i + 1; j < len(org.repos); j++ {
				two := one.clone()
				two.applyBlock(org, j)
				v.warnOverlaps(two, org.name, org.repos[j].pos, seen)
			}
		}
	}
}

// applyBlock applies org's i-th repos block.
func (rs *rows) applyBlock(org *orgPolicy, i int) {
	rb := &org.repos[i]
	rs.apply(&rb.layer, blockSource(org.name, rb.label), rb.label)
}

// warnOverlaps warns once per org, pair and resource on two active
// controls in rs owning one resource.
func (v *validator) warnOverlaps(rs *rows, org string, pos hcl.Range, seen map[string]bool) {
	var ids []control.ID

	for _, r := range rs.bySlug {
		if !r.excluded {
			ids = append(ids, r.id)
		}
	}

	slices.SortFunc(ids, func(a, b control.ID) int { return cmp.Compare(a.String(), b.String()) })

	for i, a := range ids {
		for _, b := range ids[i+1:] {
			for _, res := range sharedResources(v.defs[a], v.defs[b]) {
				key := strings.ToLower(org) + "|" + a.String() + "|" + b.String() + "|" + res
				if seen[key] {
					continue
				}

				seen[key] = true
				v.warnf(pos, "%s and %s both own %s in org %s; a repository assigned both is a resource conflict", a, b, res, org)
			}
		}
	}
}

// sharedResources is the resources both definitions own, as kind:key.
func sharedResources(a, b *definition) []string {
	if a == nil || b == nil {
		return nil
	}

	var out []string

	owned := b.ctl.Owns()

	for _, r := range a.ctl.Owns() {
		if slices.Contains(owned, r) {
			out = append(out, fmt.Sprintf("%s:%s", r.Kind, r.Key))
		}
	}

	return out
}

// Package shadow compares a v2 shadow run against v1 (DESIGN-0026 §
// Cutover, OQ15). It reads v1's rule_state and v2's findings, matches
// them per (repository, rule), and classifies every disagreement on
// v1's actionable verdict as either one of DESIGN-0025's documented
// reporting divergences or unexplained.
package shadow

import (
	"cmp"
	"slices"
	"strings"

	"github.com/donaldgifford/repo-guardian/internal/findings"
)

// V1Row is one v1 rule_state row on an active repository.
type V1Row struct {
	Org, Repo, RuleKind, RuleName string
	Actionable                    bool
}

// V2Row is one v2 finding on an active repository.
type V2Row struct {
	Org, Repo, RuleKind, RuleName string
	Status                        findings.Status
	Reason                        findings.Reason
	Remediation                   findings.Remediation
}

// Class is how one (repository, rule) pair compared.
type Class string

// Class values. Match and NotRechecked agree with v1; the divergence
// classes are DESIGN-0025 § Reporting divergences; Unexplained is
// anything else.
const (
	// Match means v2 records the same actionable verdict as v1.
	Match Class = "match"
	// NotRechecked is a finding v2 has not re-checked since the backfill
	// (migrated_from_v1): it agrees with v1 by construction, so it proves
	// nothing about parity. A high count means the shadow ran too short.
	NotRechecked Class = "not_rechecked"
	// ForeignPR: v1 counted a rule yielding to a human PR as compliant.
	ForeignPR Class = "foreign_pr"
	// BranchMissing: v1 counted a missing branch-protection target as
	// compliant.
	BranchMissing Class = "branch_missing"
	// NotApplicable: v1 wrote no row (out of scope, ignored, gate
	// closed, global ignore, empty repository).
	NotApplicable Class = "not_applicable"
	// Unknown: v1 wrote no row for an errored gate referee.
	Unknown Class = "unknown"
	// SharedName: a file and a setting rule share a name; v1 kept one
	// row (the last kind written), v2 keeps both.
	SharedName Class = "shared_name"
	// Unexplained is a disagreement no documented divergence covers.
	Unexplained Class = "unexplained"
)

// Pair is one compared (repository, rule). V1 is nil when v1 had no row
// and V2 when v2 had no finding.
type Pair struct {
	Org      string `json:"org"`
	Repo     string `json:"repo"`
	RuleKind string `json:"rule_kind"`
	RuleName string `json:"rule_name"`
	Class    Class  `json:"class"`
	V1       *V1Row `json:"v1,omitempty"`
	V2       *V2Row `json:"v2,omitempty"`
}

// Report is the result of a comparison.
type Report struct {
	// Repositories is how many repositories were active on both sides
	// and compared.
	Repositories int `json:"repositories"`
	// OnlyV1 and OnlyV2 count repositories active on one side only.
	// They are reported, not compared: parking and discovery can
	// legitimately differ between a live v1 and a shadow.
	OnlyV1 int `json:"only_v1"`
	OnlyV2 int `json:"only_v2"`
	// Counts is the number of pairs per class.
	Counts map[Class]int `json:"counts"`
	// Unexplained lists every unexplained pair, sorted.
	Unexplained []Pair `json:"unexplained,omitempty"`
}

// OK reports whether every disagreement is a documented divergence.
func (r *Report) OK() bool {
	return len(r.Unexplained) == 0
}

type repoKey struct{ org, repo string }

type ruleKey struct {
	repoKey
	kind, name string
}

func keyOf(org, repo string) repoKey {
	return repoKey{strings.ToLower(org), strings.ToLower(repo)}
}

// Compare classifies v2 against v1 over the repositories active on both
// sides (v1Repos and v2Repos, as "org/name"). Names compare
// case-insensitively, as GitHub's do.
func Compare(v1Repos, v2Repos []string, v1 []V1Row, v2 []V2Row) *Report {
	rep := &Report{Counts: map[Class]int{}}

	both := activeOnBoth(rep, v1Repos, v2Repos)

	v1ByKey := map[ruleKey]*V1Row{}
	v1Names := map[ruleKey]bool{} // kind left empty: any kind by name

	for i := range v1 {
		r := &v1[i]

		rk := keyOf(r.Org, r.Repo)
		if !both[rk] {
			continue
		}

		v1ByKey[ruleKey{rk, r.RuleKind, r.RuleName}] = r
		v1Names[ruleKey{rk, "", r.RuleName}] = true
	}

	seen := map[ruleKey]bool{}

	for i := range v2 {
		f := &v2[i]

		rk := keyOf(f.Org, f.Repo)
		if !both[rk] {
			continue
		}

		k := ruleKey{rk, f.RuleKind, f.RuleName}
		seen[k] = true
		old := v1ByKey[k]
		class := classify(old, f, v1Names[ruleKey{rk, "", f.RuleName}])
		rep.add(&Pair{Org: f.Org, Repo: f.Repo, RuleKind: f.RuleKind, RuleName: f.RuleName, Class: class, V1: old, V2: f})
	}

	for k, old := range v1ByKey {
		if seen[k] {
			continue
		}

		rep.add(&Pair{Org: old.Org, Repo: old.Repo, RuleKind: old.RuleKind, RuleName: old.RuleName, Class: Unexplained, V1: old})
	}

	slices.SortFunc(rep.Unexplained, func(a, b Pair) int {
		return cmp.Or(
			cmp.Compare(strings.ToLower(a.Org), strings.ToLower(b.Org)),
			cmp.Compare(strings.ToLower(a.Repo), strings.ToLower(b.Repo)),
			cmp.Compare(a.RuleKind, b.RuleKind),
			cmp.Compare(a.RuleName, b.RuleName),
		)
	})

	return rep
}

func activeOnBoth(rep *Report, v1Repos, v2Repos []string) map[repoKey]bool {
	split := func(s string) repoKey {
		org, repo, _ := strings.Cut(s, "/")

		return keyOf(org, repo)
	}

	in1 := map[repoKey]bool{}
	for _, s := range v1Repos {
		in1[split(s)] = true
	}

	both := map[repoKey]bool{}

	for _, s := range v2Repos {
		k := split(s)
		if in1[k] {
			both[k] = true
		} else {
			rep.OnlyV2++
		}
	}

	rep.OnlyV1 = len(in1) - len(both)
	rep.Repositories = len(both)

	return both
}

func (r *Report) add(p *Pair) {
	r.Counts[p.Class]++

	if p.Class == Unexplained {
		r.Unexplained = append(r.Unexplained, *p)
	}
}

// classify compares one v2 finding with v1's row for the same kind and
// name (nil when v1 had none). v1HasName reports whether v1 had a row
// of any kind with the finding's name.
func classify(old *V1Row, f *V2Row, v1HasName bool) Class {
	if f.Reason == findings.ReasonMigratedFromV1 {
		return NotRechecked
	}

	// The two divergences where v1 wrote "compliant" come first: v1's
	// verdict agrees, but the compliance number moves.
	v1Passed := old == nil || !old.Actionable

	switch {
	case f.Remediation == findings.RemediationForeignPR && v1Passed:
		return ForeignPR
	case f.Status == findings.StatusNotApplicable && f.Reason == findings.ReasonBranchMissing && v1Passed:
		return BranchMissing
	}

	if old != nil {
		if old.Actionable == (f.Status == findings.StatusNonCompliant) {
			return Match
		}

		return Unexplained
	}

	switch {
	case f.Status == findings.StatusNotApplicable:
		return NotApplicable
	case f.Status == findings.StatusUnknown:
		return Unknown
	case v1HasName:
		return SharedName
	default:
		return Unexplained
	}
}

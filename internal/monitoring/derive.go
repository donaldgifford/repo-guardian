package monitoring

import (
	"slices"
	"strings"

	"github.com/donaldgifford/repo-guardian/internal/control"
	"github.com/donaldgifford/repo-guardian/internal/policy"
)

// globMeta are the path.Match metacharacters. An org name containing
// one cannot be rendered as a fixed dashboard row.
const globMeta = `*?[`

// The control type names Derive keys mechanisms on. Redeclared, not
// imported: internal/controls registers metrics, and only activities and
// cmd/repo-guardian may import it (depguard controls-consumers).
const (
	typeCustomProperties = "custom_properties"
	typeRepoSettings     = "repo_settings"
	typeBranchRuleset    = "branch_ruleset"
	applyWorkflow        = "workflow"
)

// fileTypes are the control types that remediate by pull request.
var fileTypes = []string{"file", "codeowners", "catalog_info", "dependency_updates"}

// Options are the non-policy inputs to Derive.
type Options struct {
	// ConfigPath is recorded as provenance. Derive does not read it —
	// the caller has already loaded the policy.
	ConfigPath string

	// ExtraOrgs are orgs named on the command line, given a row even
	// when the policy names them nowhere.
	ExtraOrgs []string
}

// Derive projects a loaded controls policy onto the generation model,
// keyed on control id. A nil snapshot is the unconfigured static tier:
// no orgs, no controls, no mechanisms.
//
// Everything derived from a map is sorted here, at construction, so no
// emit path can introduce non-determinism into artifacts the CI drift
// gate compares byte for byte.
func Derive(snap *policy.Snapshot, opts Options) *Model {
	m := &Model{Mechanisms: make(Mechanisms), Source: Source{ConfigPath: opts.ConfigPath}}

	var sum policy.Summary
	if snap != nil {
		sum = snap.Summarize()
		m.Source.PolicyVersion = sum.Version
	}

	m.Orgs = deriveOrgs(&sum, opts.ExtraOrgs)
	m.Controls = deriveControls(snap, &sum)

	for i := range m.Controls {
		addControlMechanisms(&m.Controls[i], m.Mechanisms)
	}

	return m
}

// deriveOrgs merges the enterprise orgs, the orgs/ files and any named
// on the command line.
func deriveOrgs(sum *policy.Summary, extra []string) []Org {
	names := make(map[string]struct{})

	for _, o := range sum.Enterprise.Orgs {
		names[o] = struct{}{}
	}

	for i := range sum.Orgs {
		names[sum.Orgs[i].Org] = struct{}{}
	}

	for _, o := range extra {
		if o != "" {
			names[o] = struct{}{}
		}
	}

	out := make([]Org, 0, len(names))
	for name := range names {
		out = append(out, Org{Name: name, Pattern: strings.ContainsAny(name, globMeta)})
	}

	slices.SortFunc(out, func(a, b Org) int { return strings.Compare(a.Name, b.Name) })

	return out
}

// deriveControls lists every catalogue control with the orgs it is
// assigned in.
func deriveControls(snap *policy.Snapshot, sum *policy.Summary) []Control {
	if snap == nil {
		return nil
	}

	orgs := assignedOrgs(sum)
	out := make([]Control, 0, len(sum.Controls))

	for i := range sum.Controls {
		id := sum.Controls[i].Control
		def, _ := snap.Definition(id)

		c := Control{ID: id.String(), Type: def.Type, Apply: def.Apply, Orgs: sortedKeys(orgs[id])}
		for _, r := range def.Rules {
			c.Remediates = c.Remediates || r.Remediate
		}

		out = append(out, c)
	}

	slices.SortFunc(out, func(a, b Control) int { return strings.Compare(a.ID, b.ID) })

	return out
}

// assignedOrgs is each control's org set: the enterprise baseline in
// every enterprise org, minus an org-level replace or exclude, plus the
// replacement and every addition. A repos-block exclude or replace
// leaves the org assigned, since the rest of its repositories keep the
// control.
func assignedOrgs(sum *policy.Summary) map[control.ID]map[string]struct{} {
	out := make(map[control.ID]map[string]struct{})
	add := func(id control.ID, org string) {
		if out[id] == nil {
			out[id] = make(map[string]struct{})
		}

		out[id][org] = struct{}{}
	}

	for _, id := range sum.Enterprise.Controls {
		for _, org := range sum.Enterprise.Orgs {
			add(id, org)
		}
	}

	for i := range sum.Orgs {
		o := &sum.Orgs[i]

		for _, a := range o.Added {
			add(a.Control, o.Org)
		}

		for _, r := range o.Replaced {
			add(r.With, o.Org)

			if r.Block == "" {
				delete(out[r.Control], o.Org)
			}
		}

		for _, e := range o.Excluded {
			if e.Block == "" {
				delete(out[e.Control], o.Org)
			}
		}
	}

	return out
}

func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}

	slices.Sort(out)

	return out
}

// addControlMechanisms records the features a control turns on. Only an
// assigned control produces series.
func addControlMechanisms(c *Control, ms Mechanisms) {
	if len(c.Orgs) == 0 {
		return
	}

	switch c.Type {
	case typeCustomProperties:
		ms.add(MechanismCustomProperties)

		if c.Apply == applyWorkflow {
			ms.add(MechanismCustomPropertiesGHA)
		}
	case typeRepoSettings:
		ms.add(MechanismSettingRules)

		if c.Remediates {
			ms.add(MechanismSettingRemediation)
		}
	case typeBranchRuleset:
		ms.add(MechanismBranchProtectionRules)

		if c.Remediates {
			ms.add(MechanismBranchProtectionRemediation)
		}
	default:
		if slices.Contains(fileTypes, c.Type) {
			ms.add(MechanismFileRules)
		}
	}
}

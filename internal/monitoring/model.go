// Package monitoring derives the generation model that the dashboard
// and alert generators emit from. See DESIGN-0022 §Config-generated
// monitoring (Finding H, shape H1) and IMPL-0023 Phase 5.
//
// The premise is that a dashboard is a claim about what the running
// system measures, and the only honest source for that claim is the
// same policy the engine loads. A hand-maintained dashboard drifts from
// the config silently: a panel keeps rendering after its rule is
// deleted, and an alert keeps watching a series nothing produces —
// which is exactly how the BudgetTracker's alert survived for months
// watching a counter with no producer (INV-0012 finding A).
//
// # Import discipline
//
// This package imports internal/policy (the controls policy model),
// internal/control and the standard library. It
// must NOT import:
//
//   - internal/checker — the engine drags in the reconcilers and their
//     promauto registrations, which a read-only CLI has no business
//     performing. Same reasoning as cmd/repo-guardian/report.go.
//   - internal/metrics — for the same registration reason.
//   - the Grafana SDK — that lives in the dashboard sub-package, so
//     DESIGN-0022 OQ5's escape hatch (relocate the generator if the
//     binary-size delta is egregious) stays a package move rather than
//     an untangling. The alert half must keep working in the main
//     binary if that hatch ever fires.
//
// # Determinism
//
// Everything derived from a map is sorted at construction, never at
// emit. The task 5.5 CI drift gate diffs bytes, so one unsorted range
// in an emit path makes the gate flap on nothing.
package monitoring

import (
	"slices"
)

// Org is one org the policy names: in enterprise.hcl or as an orgs/
// file.
type Org struct {
	// Name is the org name, or a glob pattern verbatim.
	Name string

	// Pattern reports whether Name contains glob metacharacters. A
	// pattern cannot be enumerated without asking the API, so it cannot
	// carry the silent-org signal: a row that never appears and a row
	// that appears empty are indistinguishable when the row set itself
	// is discovered rather than declared.
	Pattern bool
}

// Control is one catalogue control, keyed on its id ("codeowners@1"),
// the label every controls series carries.
type Control struct {
	ID   string
	Type string

	// Apply is the control's apply mode ("recommend", "direct",
	// "workflow"); empty for types that remediate by pull request.
	Apply string

	// Remediates reports whether any rule is remediable.
	Remediates bool

	// Orgs are the orgs the control is assigned in, sorted: the
	// enterprise baseline minus org-level replaces and excludes, plus
	// org additions. Empty for a catalogue control nothing assigns.
	Orgs []string
}

// Source is provenance: when the drift gate fails, the first question
// is "generated from what?".
type Source struct {
	// ConfigPath is the --config policy root. Empty means the
	// unconfigured static tier.
	ConfigPath string

	// PolicyVersion is the snapshot's controls: version; empty for the
	// static tier.
	PolicyVersion string
}

// Model is the whole generation input.
type Model struct {
	// Orgs are the policy's orgs, sorted by name. Empty for the
	// unconfigured static tier.
	Orgs []Org

	// Controls are the catalogue's controls, sorted by id.
	Controls []Control

	// Mechanisms are the configured features that produce series.
	Mechanisms Mechanisms

	Source Source
}

// OrgNames returns the configured org names, sorted.
func (m *Model) OrgNames() []string {
	out := make([]string, 0, len(m.Orgs))
	for _, o := range m.Orgs {
		out = append(out, o.Name)
	}

	return out
}

// ControlsFor returns the controls assigned in org, in Controls order.
func (m *Model) ControlsFor(org string) []Control {
	var out []Control

	for i := range m.Controls {
		if slices.Contains(m.Controls[i].Orgs, org) {
			out = append(out, m.Controls[i])
		}
	}

	return out
}

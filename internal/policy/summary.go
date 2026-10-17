package policy

import "github.com/donaldgifford/repo-guardian/internal/control"

// Summary is the loaded policy as /policies and policy_versions.summary
// show it (DESIGN-0032 § API). It names controls, modes, exclusions and
// reasons; it never carries template bodies, PR text or rule parameters.
// The API filters Orgs to the principal's visible orgs and shows
// Enterprise only to a principal that sees every org.
type Summary struct {
	Version    string            `json:"version"`
	Enterprise EnterpriseSummary `json:"enterprise"`
	Orgs       []OrgSummary      `json:"orgs"`
	Controls   []ControlSummary  `json:"controls"`
}

// EnterpriseSummary is the baseline.
type EnterpriseSummary struct {
	Orgs     []string     `json:"orgs"`
	Mode     Mode         `json:"mode"`
	Controls []control.ID `json:"controls"`
}

// OrgSummary is one org policy. Block is the repos block label of an
// entry, empty at org level.
type OrgSummary struct {
	Org           string                 `json:"org"`
	Mode          Mode                   `json:"mode,omitempty"`
	Added         []AddedSummary         `json:"added,omitempty"`
	Replaced      []ReplacedSummary      `json:"replaced,omitempty"`
	Excluded      []ExcludedSummary      `json:"excluded,omitempty"`
	ExcludedRepos []ExcludedReposSummary `json:"excluded_repos,omitempty"`
	Repos         []ReposSummary         `json:"repos,omitempty"`
}

// AddedSummary is a control an org layer adds.
type AddedSummary struct {
	Control control.ID `json:"control"`
	Block   string     `json:"block,omitempty"`
}

// ReplacedSummary is a replace with its reason.
type ReplacedSummary struct {
	Control control.ID `json:"control"`
	With    control.ID `json:"with"`
	Reason  string     `json:"reason"`
	Block   string     `json:"block,omitempty"`
}

// ExcludedSummary is an exclude with its reason.
type ExcludedSummary struct {
	Control control.ID `json:"control"`
	Reason  string     `json:"reason"`
	Block   string     `json:"block,omitempty"`
}

// ExcludedReposSummary is an exclude_repos block.
type ExcludedReposSummary struct {
	Match  []string `json:"match"`
	Reason string   `json:"reason"`
}

// ReposSummary is a repos block's label, globs and mode.
type ReposSummary struct {
	Label string   `json:"label"`
	Match []string `json:"match"`
	Mode  Mode     `json:"mode,omitempty"`
}

// ControlSummary is one catalogue definition.
type ControlSummary struct {
	Control  control.ID `json:"control"`
	Title    string     `json:"title,omitempty"`
	Type     string     `json:"type"`
	Revision string     `json:"revision"`
}

// Summarize describes the snapshot.
func (s *Snapshot) Summarize() Summary {
	sum := Summary{
		Version: s.Version(),
		Enterprise: EnterpriseSummary{
			Orgs:     append([]string(nil), s.enterprise.orgs...),
			Mode:     ModeEvaluate,
			Controls: make([]control.ID, 0, len(s.enterprise.controls)),
		},
		Orgs:     make([]OrgSummary, 0, len(s.orgs)),
		Controls: make([]ControlSummary, 0, len(s.defs)),
	}

	if s.enterprise.mode != nil {
		sum.Enterprise.Mode = s.enterprise.mode.mode
	}

	for _, r := range s.enterprise.controls {
		sum.Enterprise.Controls = append(sum.Enterprise.Controls, r.id)
	}

	for _, o := range s.orgs {
		sum.Orgs = append(sum.Orgs, summarizeOrg(o))
	}

	for _, id := range s.ids() {
		d := s.defs[id]
		sum.Controls = append(sum.Controls, ControlSummary{Control: id, Title: d.def.Title, Type: d.def.Type, Revision: d.revision})
	}

	return sum
}

func summarizeOrg(o *orgPolicy) OrgSummary {
	os := OrgSummary{Org: o.name, Mode: declared(o.layer.mode)}
	os.summarizeLayer(&o.layer, "")

	for i := range o.excludeRepos {
		er := &o.excludeRepos[i]
		os.ExcludedRepos = append(os.ExcludedRepos, ExcludedReposSummary{Match: er.match, Reason: er.reason})
	}

	for i := range o.repos {
		rb := &o.repos[i]
		os.Repos = append(os.Repos, ReposSummary{Label: rb.label, Match: rb.match, Mode: declared(rb.layer.mode)})
		os.summarizeLayer(&rb.layer, rb.label)
	}

	return os
}

func (os *OrgSummary) summarizeLayer(ly *layer, block string) {
	for i := range ly.ops {
		switch o := &ly.ops[i]; o.kind {
		case opAdd:
			os.Added = append(os.Added, AddedSummary{Control: o.id, Block: block})
		case opReplace:
			os.Replaced = append(os.Replaced, ReplacedSummary{Control: o.id, With: o.with, Reason: o.reason, Block: block})
		case opExclude:
			os.Excluded = append(os.Excluded, ExcludedSummary{Control: o.id, Reason: o.reason, Block: block})
		}
	}
}

// declared is a declared mode, or "" when none.
func declared(m *modeDecl) Mode {
	if m == nil {
		return ""
	}

	return m.mode
}

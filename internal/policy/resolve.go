package policy

import (
	"cmp"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/control"
)

// Remediation defaults (DESIGN-0030 § Remediation settings).
const (
	DefaultMaxOpenPRs  = 3
	DefaultReopenAfter = 336 * time.Hour
)

// State is a repository's policy state.
type State string

// The repository policy states.
const (
	StateManaged   State = "managed"
	StateExcluded  State = "excluded"
	StateUnmanaged State = "unmanaged"
	StateParked    State = "parked"
)

// AssignmentState is an assignment's state.
type AssignmentState string

// The assignment states.
const (
	AssignmentActive   AssignmentState = "active"
	AssignmentExcluded AssignmentState = "excluded"
	AssignmentConflict AssignmentState = "conflict"
)

// The mode reasons: why the effective mode is not the requested one.
const (
	ModeReasonNoAccess  = "remediation_app_no_access"
	ModeReasonSuspended = "remediation_app_suspended"
)

// modeSourceDefault is the mode source when no layer declares a mode.
const modeSourceDefault = "default"

// Repository is resolution's repository input: the stored row, never an
// API read.
type Repository struct {
	Org      string `json:"org"`
	Name     string `json:"name"`
	Active   bool   `json:"active"`
	Archived bool   `json:"archived,omitempty"`
	Fork     bool   `json:"fork,omitempty"`
	// ParkReason is the stored park reason of an inactive repository.
	ParkReason string `json:"park_reason,omitempty"`
}

// AppAccess is the per-repository access input (D15). Remediable is the
// Remediation App's access through an installation that is not
// suspended; RemediationSuspended says a missing Remediable is due to
// suspension.
type AppAccess struct {
	Evaluable            bool `json:"evaluable"`
	Remediable           bool `json:"remediable"`
	RemediationSuspended bool `json:"remediation_suspended,omitempty"`
}

// Resolution is one repository's resolved policy: its state, its
// assignments in ordinal order and its effective remediation settings.
type Resolution struct {
	State       State         `json:"state"`
	Reason      string        `json:"reason,omitempty"`
	Assignments []Assignment  `json:"assignments,omitempty"`
	MaxOpenPRs  int           `json:"max_open_prs"`
	ReopenAfter time.Duration `json:"reopen_after"`
}

// Assignment is one control_assignments row, without its epoch, which
// the store assigns.
type Assignment struct {
	Control         control.ID      `json:"control"`
	Revision        string          `json:"revision"`
	State           AssignmentState `json:"state"`
	Decision        Decision        `json:"decision"`
	BaselineControl string          `json:"baseline_control,omitempty"`
	Source          string          `json:"source"`
	SourceBlock     string          `json:"source_block,omitempty"`
	Ordinal         int             `json:"ordinal"`
	Replaced        string          `json:"replaced,omitempty"`
	ExcludedBy      string          `json:"excluded_by,omitempty"`
	Reason          string          `json:"reason,omitempty"`
	Error           string          `json:"error,omitempty"`
	RequestedMode   Mode            `json:"requested_mode"`
	EffectiveMode   Mode            `json:"effective_mode"`
	ModeSource      string          `json:"mode_source"`
	ModeReason      string          `json:"mode_reason,omitempty"`
}

// Resolve is DESIGN-0030's resolution algorithm, steps 1–6: pure, with
// no API calls. Step 7 (epochs) belongs to the store.
func (s *Snapshot) Resolve(repo Repository, access AppAccess) Resolution {
	if !containsFold(s.enterprise.orgs, repo.Org) {
		maxPRs, reopen := s.remediation(nil)

		return Resolution{State: StateUnmanaged, MaxOpenPRs: maxPRs, ReopenAfter: reopen}
	}

	org := s.org(repo.Org)
	res := Resolution{State: StateManaged}
	res.MaxOpenPRs, res.ReopenAfter = s.remediation(org)

	if org != nil {
		for i := range org.excludeRepos {
			if er := &org.excludeRepos[i]; matchesAny(er.match, repo.Name) {
				res.State, res.Reason = StateExcluded, er.reason

				return res
			}
		}
	}

	blocks := matchingBlocks(org, repo.Name)
	rs := newBaseline(s.enterprise)

	if org != nil {
		rs.apply(&org.layer, orgSource(org.name), "")
	}

	for _, rb := range blocks {
		rs.apply(&rb.layer, blockSource(org.name, rb.label), rb.label)
	}

	res.Assignments = s.assignments(rs, org, blocks, access)

	if !repo.Active {
		res.State, res.Reason = StateParked, parkReason(&repo)
	}

	return res
}

// assignments turns the rows into assignments with modes (steps 4–5) and
// conflicts (step 6), in ordinal order.
func (s *Snapshot) assignments(rs *rows, org *orgPolicy, blocks []*reposBlock, access AppAccess) []Assignment {
	out := make([]Assignment, 0, len(rs.bySlug))

	for _, r := range rs.bySlug {
		a := Assignment{
			Control: r.id, State: AssignmentActive, Decision: r.decision, BaselineControl: r.baseline,
			Source: r.source, SourceBlock: r.sourceBlock, Ordinal: r.ordinal,
			Replaced: r.replaced, ExcludedBy: r.excludedBy, Reason: r.reason,
		}

		if d := s.defs[r.id]; d != nil {
			a.Revision = d.revision
		}

		if r.excluded {
			a.State = AssignmentExcluded
		}

		a.RequestedMode, a.ModeSource = s.requestedMode(r.id, org, blocks)
		a.EffectiveMode = a.RequestedMode

		if a.RequestedMode == ModeRemediate && !access.Remediable {
			a.EffectiveMode, a.ModeReason = ModeEvaluate, ModeReasonNoAccess
			if access.RemediationSuspended {
				a.ModeReason = ModeReasonSuspended
			}
		}

		out = append(out, a)
	}

	slices.SortFunc(out, func(a, b Assignment) int { return cmp.Compare(a.Ordinal, b.Ordinal) })
	s.markConflicts(out)

	return out
}

// requestedMode is step 4: a control override in a matching repos block,
// then that block's mode, then an org override, then the org mode, then
// the enterprise mode. Among matching blocks the later one wins.
func (s *Snapshot) requestedMode(id control.ID, org *orgPolicy, blocks []*reposBlock) (mode Mode, source string) {
	for _, rb := range slices.Backward(blocks) {
		if m := overrideFor(&rb.layer, id); m != nil {
			return m.mode, blockSource(org.name, rb.label)
		}
	}

	for _, rb := range slices.Backward(blocks) {
		if rb.layer.mode != nil {
			return rb.layer.mode.mode, blockSource(org.name, rb.label)
		}
	}

	if org != nil {
		if m := overrideFor(&org.layer, id); m != nil {
			return m.mode, orgSource(org.name)
		}

		if org.layer.mode != nil {
			return org.layer.mode.mode, orgSource(org.name)
		}
	}

	if s.enterprise.mode != nil {
		return s.enterprise.mode.mode, sourceEnterprise
	}

	return ModeEvaluate, modeSourceDefault
}

// overrideFor returns the layer's last override for id.
func overrideFor(ly *layer, id control.ID) *modeDecl {
	for i := len(ly.overrides) - 1; i >= 0; i-- {
		if ly.overrides[i].id == id {
			return &ly.overrides[i].mode
		}
	}

	return nil
}

// markConflicts is step 6: every active assignment claiming a resource
// another active assignment claims becomes a conflict, with an error
// naming the resource and the claimants.
func (s *Snapshot) markConflicts(as []Assignment) {
	claims := make(map[string][]int)

	var keys []string

	for i := range as {
		if as[i].State != AssignmentActive {
			continue
		}

		d := s.defs[as[i].Control]
		if d == nil {
			continue
		}

		for _, r := range d.ctl.Owns() {
			key := r.Kind + ":" + r.Key
			if _, ok := claims[key]; !ok {
				keys = append(keys, key)
			}

			claims[key] = append(claims[key], i)
		}
	}

	slices.Sort(keys)

	for _, key := range keys {
		idx := claims[key]
		if len(idx) < 2 {
			continue
		}

		names := make([]string, 0, len(idx))
		for _, i := range idx {
			names = append(names, as[i].Control.String())
		}

		slices.Sort(names)
		msg := "resource_conflict: " + key + " claimed by " + joinAnd(names)

		for _, i := range idx {
			if as[i].Error == "" {
				as[i].State, as[i].Error = AssignmentConflict, msg
			}
		}
	}
}

// joinAnd joins names as "a and b" or "a, b and c".
func joinAnd(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}

	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// remediation resolves max_open_prs and reopen_after: org override, then
// enterprise, then the default. Validation guarantees the values parse.
func (s *Snapshot) remediation(org *orgPolicy) (maxPRs int, reopen time.Duration) {
	maxPRs, reopen = DefaultMaxOpenPRs, DefaultReopenAfter

	for _, r := range []*remediationDecl{&s.enterprise.remed, orgRemed(org)} {
		if r == nil {
			continue
		}

		if r.maxOpenPRs != nil {
			maxPRs = *r.maxOpenPRs
		}

		if r.reopenAfter != nil {
			if d, err := time.ParseDuration(*r.reopenAfter); err == nil {
				reopen = d
			}
		}
	}

	return maxPRs, reopen
}

func orgRemed(org *orgPolicy) *remediationDecl {
	if org == nil {
		return nil
	}

	return &org.remed
}

// org returns the policy for name, or nil when the org has no file.
func (s *Snapshot) org(name string) *orgPolicy {
	for _, o := range s.orgs {
		if strings.EqualFold(o.name, name) {
			return o
		}
	}

	return nil
}

// matchingBlocks is the org's repos blocks matching name, in file order.
func matchingBlocks(org *orgPolicy, name string) []*reposBlock {
	if org == nil {
		return nil
	}

	var out []*reposBlock

	for i := range org.repos {
		if matchesAny(org.repos[i].match, name) {
			out = append(out, &org.repos[i])
		}
	}

	return out
}

// parkReason is the stored park reason, else what the row says.
func parkReason(repo *Repository) string {
	switch {
	case repo.ParkReason != "":
		return repo.ParkReason
	case repo.Archived:
		return "archived"
	case repo.Fork:
		return "fork"
	default:
		return ""
	}
}

// matchesAny reports whether name matches one of the globs. Repository
// names compare case-insensitively, as GitHub's do.
func matchesAny(patterns []string, name string) bool {
	name = strings.ToLower(name)

	return slices.ContainsFunc(patterns, func(p string) bool {
		ok, err := path.Match(strings.ToLower(p), name)

		return err == nil && ok
	})
}

func containsFold(list []string, s string) bool {
	return slices.ContainsFunc(list, func(x string) bool { return strings.EqualFold(x, s) })
}

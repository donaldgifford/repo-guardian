package policy

import (
	"github.com/hashicorp/hcl/v2"

	"github.com/donaldgifford/repo-guardian/internal/control"
)

// Mode is an assignment's mode (DESIGN-0030 § Modes).
type Mode string

// The modes.
const (
	ModeEvaluate  Mode = "evaluate"
	ModeRemediate Mode = "remediate"
)

// enterprisePolicy is enterprise.hcl decoded: the baseline, with no
// exclusions.
type enterprisePolicy struct {
	pos      hcl.Range
	orgs     []string
	orgsPos  hcl.Range
	mode     *modeDecl
	controls []ref
	remed    remediationDecl
}

// orgPolicy is one org block decoded.
type orgPolicy struct {
	name string
	pos  hcl.Range
	// layer is the org-wide operations, overrides and mode.
	layer        layer
	excludeRepos []excludeRepos
	repos        []reposBlock
	remed        remediationDecl
}

// layer is what both an org block and a repos block can declare.
type layer struct {
	mode      *modeDecl
	ops       []op
	overrides []override
}

// reposBlock is a labelled repos block.
type reposBlock struct {
	label string
	pos   hcl.Range
	match []string
	layer layer
}

// excludeRepos removes matching repositories from management.
type excludeRepos struct {
	pos    hcl.Range
	match  []string
	reason string
}

// opKind is what an op does.
type opKind int

const (
	opAdd opKind = iota + 1
	opReplace
	opExclude
)

// op is one controls entry, replace or exclude, in file order.
type op struct {
	kind opKind
	// id is the control added, replaced (the source) or excluded.
	id control.ID
	// with is a replace's replacement.
	with   control.ID
	reason string
	pos    hcl.Range
}

// override is a control "x@N" { mode } block.
type override struct {
	id   control.ID
	mode modeDecl
	pos  hcl.Range
}

// modeDecl is a declared mode with its location.
type modeDecl struct {
	mode Mode
	pos  hcl.Range
}

// remediationDecl is a remediation {} block; nil fields were not set.
type remediationDecl struct {
	maxOpenPRs     *int
	maxOpenPRsPos  hcl.Range
	reopenAfter    *string
	reopenAfterPos hcl.Range
}

// ref is a control reference with its location.
type ref struct {
	id  control.ID
	pos hcl.Range
}

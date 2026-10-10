package control

import (
	"slices"
	"strings"
)

// Permission is one GitHub App permission: a resource and the access
// the App needs on it.
type Permission struct {
	Resource string
	Access   Access
}

// Access is a permission level.
type Access string

// The access levels an App asks for.
const (
	AccessRead  Access = "read"
	AccessWrite Access = "write" // read and write
)

// The GitHub permission resources the two Apps use, as GitHub's App
// settings name them.
const (
	ResourceMetadata         = "Metadata"
	ResourceContents         = "Contents"
	ResourcePullRequests     = "Pull requests"
	ResourceAdministration   = "Administration"
	ResourceIssues           = "Issues"
	ResourceWorkflows        = "Workflows"
	ResourceCustomProperties = "Custom properties"
	ResourceOrgCustomProps   = "Custom properties (organization)"
)

// String renders p as the operator docs write it.
func (p Permission) String() string {
	return p.Resource + ": " + string(p.Access)
}

// EvaluationPermissions is the Evaluation App's fixed read-only set
// (DESIGN-0032 § Two Apps, amended by the INV-0022 Phase-0 results):
// merge-policy settings are read through GraphQL under it, Administration
// read brings security_and_analysis, and the organization's custom
// property schema needs its own read.
func EvaluationPermissions() []Permission {
	return []Permission{
		{ResourceMetadata, AccessRead},
		{ResourceContents, AccessRead},
		{ResourcePullRequests, AccessRead},
		{ResourceAdministration, AccessRead},
		{ResourceOrgCustomProps, AccessRead},
	}
}

// RemediationPermissions derives the Remediation App's set: Contents and
// Pull requests write always, plus write on every resource a registered
// control type remediates (resources) and Workflows write when a type
// declares workflow apply. GitHub gates every write under
// .github/workflows/ on Workflows in addition to Contents. With no
// registered types it is the base set.
func RemediationPermissions(resources []string, workflowApply bool) []Permission {
	set := map[string]Access{
		ResourceMetadata:     AccessRead,
		ResourceContents:     AccessWrite,
		ResourcePullRequests: AccessWrite,
	}

	for _, r := range resources {
		set[r] = AccessWrite
	}

	if workflowApply {
		set[ResourceWorkflows] = AccessWrite
	}

	out := make([]Permission, 0, len(set))
	for r, a := range set {
		out = append(out, Permission{Resource: r, Access: a})
	}

	slices.SortFunc(out, func(a, b Permission) int { return strings.Compare(a.Resource, b.Resource) })

	return out
}

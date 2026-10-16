package api

import (
	"context"
	"net/http"

	"github.com/donaldgifford/repo-guardian/internal/api/gen"
)

// TODO(IMPL-0029 8.3): serve the controls, org, summary and history
// views from the controls compliance query.

// GetSummary returns fleet totals over the visible orgs.
func (*server) GetSummary(context.Context, gen.GetSummaryRequestObject) (gen.GetSummaryResponseObject, error) {
	return nil, statusError(http.StatusNotImplemented, "")
}

// ListControls returns every control assigned in the visible orgs.
func (*server) ListControls(context.Context, gen.ListControlsRequestObject) (gen.ListControlsResponseObject, error) {
	return nil, statusError(http.StatusNotImplemented, "")
}

// GetControl returns one control's compliance by org.
func (*server) GetControl(context.Context, gen.GetControlRequestObject) (gen.GetControlResponseObject, error) {
	return nil, statusError(http.StatusNotImplemented, "")
}

// GetOrg returns one org's baseline-versus-org compliance.
func (*server) GetOrg(context.Context, gen.GetOrgRequestObject) (gen.GetOrgResponseObject, error) {
	return nil, statusError(http.StatusNotImplemented, "")
}

// GetComplianceHistory returns compliance snapshots.
func (*server) GetComplianceHistory(context.Context, gen.GetComplianceHistoryRequestObject) (gen.GetComplianceHistoryResponseObject, error) {
	return nil, statusError(http.StatusNotImplemented, "")
}

// GetPolicies returns the active policy scoped to the visible orgs.
func (*server) GetPolicies(context.Context, gen.GetPoliciesRequestObject) (gen.GetPoliciesResponseObject, error) {
	return nil, statusError(http.StatusNotImplemented, "")
}

// ListRepositoryControls returns a repository's assignments and results.
func (*server) ListRepositoryControls(context.Context, gen.ListRepositoryControlsRequestObject) (gen.ListRepositoryControlsResponseObject, error) {
	return nil, statusError(http.StatusNotImplemented, "")
}

// EvaluateRepository asks for a repository to be evaluated now.
func (*server) EvaluateRepository(context.Context, gen.EvaluateRepositoryRequestObject) (gen.EvaluateRepositoryResponseObject, error) {
	return nil, statusError(http.StatusNotImplemented, "")
}

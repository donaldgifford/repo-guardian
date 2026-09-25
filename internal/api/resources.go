package api

import (
	"context"
	"net/http"

	"github.com/donaldgifford/repo-guardian/internal/api/gen"
)

// errNotImplemented answers the endpoints whose readers land later in
// the phase.
var errNotImplemented = statusError(http.StatusNotImplemented, "")

// ListComplianceHistory is not implemented yet.
//
// TODO(IMPL-0025 P15): implement over the APIReader.
func (*server) ListComplianceHistory(context.Context, gen.ListComplianceHistoryRequestObject) (gen.ListComplianceHistoryResponseObject, error) {
	return nil, errNotImplemented
}

// ListFindings is not implemented yet.
//
// TODO(IMPL-0025 P15): implement over the APIReader.
func (*server) ListFindings(context.Context, gen.ListFindingsRequestObject) (gen.ListFindingsResponseObject, error) {
	return nil, errNotImplemented
}

// ListInstallations is not implemented yet.
//
// TODO(IMPL-0025 P15): implement over the APIReader.
func (*server) ListInstallations(context.Context, gen.ListInstallationsRequestObject) (gen.ListInstallationsResponseObject, error) {
	return nil, errNotImplemented
}

// ListOrgs is not implemented yet.
//
// TODO(IMPL-0025 P15): implement over the APIReader.
func (*server) ListOrgs(context.Context, gen.ListOrgsRequestObject) (gen.ListOrgsResponseObject, error) {
	return nil, errNotImplemented
}

// GetOrg is not implemented yet.
//
// TODO(IMPL-0025 P15): implement over the APIReader.
func (*server) GetOrg(context.Context, gen.GetOrgRequestObject) (gen.GetOrgResponseObject, error) {
	return nil, errNotImplemented
}

// GetPolicy is not implemented yet.
//
// TODO(IMPL-0025 P15): implement over the APIReader.
func (*server) GetPolicy(context.Context, gen.GetPolicyRequestObject) (gen.GetPolicyResponseObject, error) {
	return nil, errNotImplemented
}

// ListRepositories is not implemented yet.
//
// TODO(IMPL-0025 P15): implement over the APIReader.
func (*server) ListRepositories(context.Context, gen.ListRepositoriesRequestObject) (gen.ListRepositoriesResponseObject, error) {
	return nil, errNotImplemented
}

// GetRepository is not implemented yet.
//
// TODO(IMPL-0025 P15): implement over the APIReader.
func (*server) GetRepository(context.Context, gen.GetRepositoryRequestObject) (gen.GetRepositoryResponseObject, error) {
	return nil, errNotImplemented
}

// ListRepositoryChecks is not implemented yet.
//
// TODO(IMPL-0025 P15): implement over the APIReader.
func (*server) ListRepositoryChecks(context.Context, gen.ListRepositoryChecksRequestObject) (gen.ListRepositoryChecksResponseObject, error) {
	return nil, errNotImplemented
}

// ListRepositoryEvents is not implemented yet.
//
// TODO(IMPL-0025 P15): implement over the APIReader.
func (*server) ListRepositoryEvents(context.Context, gen.ListRepositoryEventsRequestObject) (gen.ListRepositoryEventsResponseObject, error) {
	return nil, errNotImplemented
}

// ListRules is not implemented yet.
//
// TODO(IMPL-0025 P15): implement over the APIReader.
func (*server) ListRules(context.Context, gen.ListRulesRequestObject) (gen.ListRulesResponseObject, error) {
	return nil, errNotImplemented
}

// GetRule is not implemented yet.
//
// TODO(IMPL-0025 P15): implement over the APIReader.
func (*server) GetRule(context.Context, gen.GetRuleRequestObject) (gen.GetRuleResponseObject, error) {
	return nil, errNotImplemented
}

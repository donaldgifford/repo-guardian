package github

import "context"

// GraphQLQuery runs q through the client's GraphQL endpoint, for
// external tests that exercise the transport chain with a GraphQL call.
func (c *GitHubClient) GraphQLQuery(ctx context.Context, q any, vars map[string]any) error {
	return c.graphQL().Query(ctx, q, vars)
}

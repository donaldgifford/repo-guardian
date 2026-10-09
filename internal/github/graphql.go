package github

import (
	"strings"

	gh "github.com/google/go-github/v68/github"
	"github.com/shurcooL/githubv4"
)

const (
	// dotComAPIHost is api.github.com, whose GraphQL endpoint is not
	// under the REST path the way an Enterprise Server's is.
	dotComAPIHost = "api.github.com"

	// graphQLPath ends every GraphQL endpoint path; the rate-limit
	// transport also uses it to name the bucket a request spends.
	graphQLPath = "/graphql"
)

// graphQLFor returns a GraphQL client that shares rest's *http.Client,
// so every GraphQL call goes through the same transport chain as REST:
// otelhttp outermost, then the rate-limit transport, then
// ghinstallation (IMPL-0028 task 2.4). A GraphQL request is therefore
// measured, throttled and authenticated exactly like a REST one.
func graphQLFor(rest *gh.Client) *githubv4.Client {
	return githubv4.NewEnterpriseClient(graphQLURL(rest), rest.Client())
}

// graphQLURL derives the GraphQL endpoint from the REST base URL:
// https://api.github.com/graphql on github.com, and
// https://<host>/api/graphql on an Enterprise Server, whose REST base is
// https://<host>/api/v3/.
func graphQLURL(rest *gh.Client) string {
	base := *rest.BaseURL
	if base.Host == dotComAPIHost {
		base.Path = graphQLPath

		return base.String()
	}

	base.Path = strings.TrimSuffix(strings.TrimSuffix(base.Path, "/"), "/v3") + graphQLPath

	return base.String()
}

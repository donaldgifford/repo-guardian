package activities

import (
	"context"
	"fmt"

	"github.com/donaldgifford/repo-guardian/internal/control"
	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/github/write"
)

// GitHubRemediationClients is RemediationClients over the Remediation
// App's installation clients. It is the one place the controls line
// constructs a Writer (depguard rule writer).
type GitHubRemediationClients struct {
	app ClientFactory
}

var _ RemediationClients = (*GitHubRemediationClients)(nil)

// NewGitHubRemediationClients returns RemediationClients over app, which
// must be the Remediation App's client.
func NewGitHubRemediationClients(app ClientFactory) *GitHubRemediationClients {
	return &GitHubRemediationClients{app: app}
}

// Repo opens owner/name with an installation token.
func (g *GitHubRemediationClients) Repo(ctx context.Context, installationID int64, owner, name string) (RemediationRepo, error) {
	c, err := g.app.CreateInstallationClient(ctx, installationID)
	if err != nil {
		return nil, fmt.Errorf("create installation client for %d: %w", installationID, err)
	}

	gc, ok := c.(*ghclient.GitHubClient)
	if !ok {
		return nil, fmt.Errorf("installation client for %d is a %T, not a *github.GitHubClient", installationID, c)
	}

	return &githubRemediationRepo{client: gc, owner: owner, name: name}, nil
}

type githubRemediationRepo struct {
	client *ghclient.GitHubClient
	owner  string
	name   string
}

func (r *githubRemediationRepo) Writer() control.Writer { return write.New(r.client, r.owner, r.name) }

func (r *githubRemediationRepo) Reader(sha string) control.Reader {
	return r.client.RepoReader(r.owner, r.name, sha)
}

func (r *githubRemediationRepo) Observer() control.PRObserver {
	return r.client.RepoObserver(r.owner, r.name)
}

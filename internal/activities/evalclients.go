package activities

import (
	"context"
	"fmt"

	"github.com/donaldgifford/repo-guardian/internal/control"
	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
)

// GitHubEvalClients is EvalClients over the Evaluation App's installation
// clients.
type GitHubEvalClients struct {
	app ClientFactory
}

var _ EvalClients = (*GitHubEvalClients)(nil)

// NewGitHubEvalClients returns EvalClients over app, which must be the
// Evaluation App's client.
func NewGitHubEvalClients(app ClientFactory) *GitHubEvalClients {
	return &GitHubEvalClients{app: app}
}

// Repo opens owner/name with an installation token.
func (g *GitHubEvalClients) Repo(ctx context.Context, installationID int64, owner, name string) (EvalRepo, error) {
	c, err := g.app.CreateInstallationClient(ctx, installationID)
	if err != nil {
		return nil, fmt.Errorf("create installation client for %d: %w", installationID, err)
	}

	gc, ok := c.(*ghclient.GitHubClient)
	if !ok {
		return nil, fmt.Errorf("installation client for %d is a %T, not a *github.GitHubClient", installationID, c)
	}

	return &githubEvalRepo{client: gc, owner: owner, name: name}, nil
}

type githubEvalRepo struct {
	client *ghclient.GitHubClient
	owner  string
	name   string
}

// Head reads the default branch over REST, whose 403 or 404 is the
// repository-level access loss IsAccessDenied recognises, then pins its
// head through PRObserver.GetRef.
func (r *githubEvalRepo) Head(ctx context.Context) (branch, sha string, err error) {
	repo, err := r.client.GetRepository(ctx, r.owner, r.name)
	if err != nil {
		return "", "", fmt.Errorf("pin head: %w", err)
	}

	if repo.DefaultRef == "" {
		return "", "", nil
	}

	sha, _, err = r.client.RepoObserver(r.owner, r.name).GetRef(ctx, repo.DefaultRef)
	if err != nil {
		return "", "", fmt.Errorf("pin head: %w", err)
	}

	return repo.DefaultRef, sha, nil
}

func (r *githubEvalRepo) Reader(sha string) control.Reader {
	return r.client.RepoReader(r.owner, r.name, sha)
}

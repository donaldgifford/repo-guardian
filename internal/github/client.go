package github

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"

	"github.com/bradleyfalzon/ghinstallation/v2"
	gh "github.com/google/go-github/v68/github"
)

// GitHubClient implements the Client interface using the go-github library
// and GitHub App installation authentication.
type GitHubClient struct {
	appTransport       *ghinstallation.AppsTransport
	appClient          *gh.Client
	logger             *slog.Logger
	rateLimitThreshold float64

	mu             sync.Mutex
	installClients map[int64]*gh.Client
	installationID int64 // Non-zero when this client is scoped to an installation.
	scopedGHClient *gh.Client
}

// NewClient creates a new GitHubClient configured as a GitHub App.
func NewClient(appID int64, privateKeyPath string, logger *slog.Logger, rateLimitThreshold float64) (*GitHubClient, error) {
	transport, err := ghinstallation.NewAppsTransportKeyFromFile(http.DefaultTransport, appID, privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("creating GitHub App transport: %w", err)
	}

	rlTransport := newRateLimitTransport(transport, logger.With("component", "ratelimit"), rateLimitThreshold)
	appClient := gh.NewClient(&http.Client{Transport: rlTransport})

	return &GitHubClient{
		appTransport:       transport,
		appClient:          appClient,
		logger:             logger,
		rateLimitThreshold: rateLimitThreshold,
		installClients:     make(map[int64]*gh.Client),
	}, nil
}

// ghClient returns the appropriate go-github client. If this GitHubClient
// is scoped to an installation, it returns the installation client;
// otherwise, it returns the app-level client.
func (c *GitHubClient) ghClient() *gh.Client {
	if c.scopedGHClient != nil {
		return c.scopedGHClient
	}

	return c.appClient
}

// GetContents checks whether a file exists at the given path in a repository.
func (c *GitHubClient) GetContents(ctx context.Context, owner, repo, path string) (bool, error) {
	_, _, resp, err := c.ghClient().Repositories.GetContents(ctx, owner, repo, path, nil)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return false, nil
		}

		return false, fmt.Errorf("getting contents %s/%s/%s: %w", owner, repo, path, err)
	}

	return true, nil
}

// ListOpenPullRequests returns all open pull requests for a repository.
func (c *GitHubClient) ListOpenPullRequests(ctx context.Context, owner, repo string) ([]*PullRequest, error) {
	opts := &gh.PullRequestListOptions{
		State: "open",
		ListOptions: gh.ListOptions{
			PerPage: 100,
		},
	}

	var allPRs []*PullRequest

	for {
		prs, resp, err := c.ghClient().PullRequests.List(ctx, owner, repo, opts)
		if err != nil {
			return nil, fmt.Errorf("listing pull requests for %s/%s: %w", owner, repo, err)
		}

		for _, pr := range prs {
			allPRs = append(allPRs, &PullRequest{
				Number: pr.GetNumber(),
				Title:  pr.GetTitle(),
				Head:   pr.GetHead().GetRef(),
				State:  pr.GetState(),
			})
		}

		if resp.NextPage == 0 {
			break
		}

		opts.Page = resp.NextPage
	}

	return allPRs, nil
}

// GetRepository returns repository metadata.
func (c *GitHubClient) GetRepository(ctx context.Context, owner, repo string) (*Repository, error) {
	r, _, err := c.ghClient().Repositories.Get(ctx, owner, repo)
	if err != nil {
		return nil, fmt.Errorf("getting repository %s/%s: %w", owner, repo, err)
	}

	return &Repository{
		Owner:      owner,
		Name:       repo,
		Archived:   r.GetArchived(),
		Fork:       r.GetFork(),
		HasBranch:  r.GetDefaultBranch() != "",
		DefaultRef: r.GetDefaultBranch(),
	}, nil
}

// GetBranchSHA returns the commit SHA of the given branch, or empty string if the branch does not exist.
func (c *GitHubClient) GetBranchSHA(ctx context.Context, owner, repo, branch string) (string, error) {
	ref, resp, err := c.ghClient().Git.GetRef(ctx, owner, repo, "refs/heads/"+branch)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return "", nil
		}

		return "", fmt.Errorf("getting branch %s for %s/%s: %w", branch, owner, repo, err)
	}

	return ref.GetObject().GetSHA(), nil
}

// CreateBranch creates a new branch from the given base SHA.
func (c *GitHubClient) CreateBranch(ctx context.Context, owner, repo, branch, baseSHA string) error {
	ref := &gh.Reference{
		Ref: gh.Ptr("refs/heads/" + branch),
		Object: &gh.GitObject{
			SHA: gh.Ptr(baseSHA),
		},
	}

	_, _, err := c.ghClient().Git.CreateRef(ctx, owner, repo, ref)
	if err != nil {
		return fmt.Errorf("creating branch %s for %s/%s: %w", branch, owner, repo, err)
	}

	return nil
}

// DeleteBranch deletes a branch from the repository.
func (c *GitHubClient) DeleteBranch(ctx context.Context, owner, repo, branch string) error {
	_, err := c.ghClient().Git.DeleteRef(ctx, owner, repo, "refs/heads/"+branch)
	if err != nil {
		return fmt.Errorf("deleting branch %s for %s/%s: %w", branch, owner, repo, err)
	}

	return nil
}

// CreateOrUpdateFile creates or updates a file on the given branch.
func (c *GitHubClient) CreateOrUpdateFile(
	ctx context.Context,
	owner, repo, branch, path, content, message string,
) error {
	opts := &gh.RepositoryContentFileOptions{
		Message: gh.Ptr(message),
		Content: []byte(content),
		Branch:  gh.Ptr(branch),
	}

	_, _, err := c.ghClient().Repositories.CreateFile(ctx, owner, repo, path, opts)
	if err != nil {
		return fmt.Errorf("creating file %s in %s/%s: %w", path, owner, repo, err)
	}

	return nil
}

// CreatePullRequest creates a new pull request and returns it.
func (c *GitHubClient) CreatePullRequest(
	ctx context.Context,
	owner, repo, title, body, head, base string,
) (*PullRequest, error) {
	pr, _, err := c.ghClient().PullRequests.Create(ctx, owner, repo, &gh.NewPullRequest{
		Title: gh.Ptr(title),
		Body:  gh.Ptr(body),
		Head:  gh.Ptr(head),
		Base:  gh.Ptr(base),
	})
	if err != nil {
		return nil, fmt.Errorf("creating PR for %s/%s: %w", owner, repo, err)
	}

	return &PullRequest{
		Number: pr.GetNumber(),
		Title:  pr.GetTitle(),
		Head:   pr.GetHead().GetRef(),
		State:  pr.GetState(),
	}, nil
}

// ListInstallations returns all installations for this GitHub App.
func (c *GitHubClient) ListInstallations(ctx context.Context) ([]*Installation, error) {
	opts := &gh.ListOptions{PerPage: 100}

	var allInstalls []*Installation

	for {
		installs, resp, err := c.appClient.Apps.ListInstallations(ctx, opts)
		if err != nil {
			return nil, fmt.Errorf("listing installations: %w", err)
		}

		for _, install := range installs {
			allInstalls = append(allInstalls, &Installation{
				ID:      install.GetID(),
				Account: install.GetAccount().GetLogin(),
			})
		}

		if resp.NextPage == 0 {
			break
		}

		opts.Page = resp.NextPage
	}

	return allInstalls, nil
}

// ListInstallationRepos returns all repositories accessible to the given installation.
func (c *GitHubClient) ListInstallationRepos(ctx context.Context, installationID int64) ([]*Repository, error) {
	installClient, err := c.getInstallClient(installationID)
	if err != nil {
		return nil, err
	}

	opts := &gh.ListOptions{PerPage: 100}

	var allRepos []*Repository

	for {
		result, resp, err := installClient.Apps.ListRepos(ctx, opts)
		if err != nil {
			return nil, fmt.Errorf("listing repos for installation %d: %w", installationID, err)
		}

		for _, repo := range result.Repositories {
			allRepos = append(allRepos, &Repository{
				Owner:      repo.GetOwner().GetLogin(),
				Name:       repo.GetName(),
				Archived:   repo.GetArchived(),
				Fork:       repo.GetFork(),
				HasBranch:  repo.GetDefaultBranch() != "",
				DefaultRef: repo.GetDefaultBranch(),
			})
		}

		if resp.NextPage == 0 {
			break
		}

		opts.Page = resp.NextPage
	}

	return allRepos, nil
}

// CreateInstallationClient returns a Client scoped to a specific installation.
func (c *GitHubClient) CreateInstallationClient(_ context.Context, installationID int64) (Client, error) {
	ghClient, err := c.getInstallClient(installationID)
	if err != nil {
		return nil, err
	}

	return &GitHubClient{
		appClient:      c.appClient,
		logger:         c.logger.With("installation_id", installationID),
		installationID: installationID,
		scopedGHClient: ghClient,
	}, nil
}

func (c *GitHubClient) getInstallClient(installationID int64) (*gh.Client, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if client, ok := c.installClients[installationID]; ok {
		return client, nil
	}

	transport := ghinstallation.NewFromAppsTransport(c.appTransport, installationID)
	rlTransport := newRateLimitTransport(
		transport,
		c.logger.With("component", "ratelimit", "installation_id", installationID),
		c.rateLimitThreshold,
	)
	client := gh.NewClient(&http.Client{Transport: rlTransport})
	c.installClients[installationID] = client

	return client, nil
}

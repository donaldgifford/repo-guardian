package report

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
)

// GitHubLinker resolves open repo-guardian PR URLs via the App.
type GitHubLinker struct {
	client     ghclient.Client
	branchName string
	logger     *slog.Logger

	mu      sync.Mutex
	scoped  map[int64]ghclient.Client
	failing map[int64]struct{}
}

// GitHubLinkerOptions bundles the GitHubLinker constructor inputs.
type GitHubLinkerOptions struct {
	Client ghclient.Client

	// BranchName is the reconcile branch repo-guardian opens PRs from.
	// Passed in rather than imported so this package does not depend on
	// internal/checker for a single constant.
	BranchName string

	Logger *slog.Logger
}

// NewGitHubLinker builds a GitHubLinker.
func NewGitHubLinker(opts GitHubLinkerOptions) *GitHubLinker {
	return &GitHubLinker{
		client:     opts.Client,
		branchName: opts.BranchName,
		logger:     opts.Logger,
		scoped:     make(map[int64]ghclient.Client),
		failing:    make(map[int64]struct{}),
	}
}

// PRURL returns the URL of the open repo-guardian PR for a repository,
// or "" when there is none.
//
// Installation clients are cached, so an org with 200 failing
// repositories costs one CreateInstallationClient rather than 200. An
// installation whose client could not be created is remembered as
// failing and skipped thereafter: retrying a credential problem once
// per repository would spend the whole API budget rediscovering the
// same error.
func (l *GitHubLinker) PRURL(ctx context.Context, installationID int64, owner, repo string) (string, error) {
	client, err := l.clientFor(ctx, installationID)
	if err != nil {
		return "", err
	}

	prs, err := client.ListOpenPullRequests(ctx, owner, repo)
	if err != nil {
		return "", fmt.Errorf("list open PRs for %s/%s: %w", owner, repo, err)
	}

	for _, pr := range prs {
		if pr.Head == l.branchName {
			return pr.HTMLURL, nil
		}
	}

	return "", nil
}

func (l *GitHubLinker) clientFor(ctx context.Context, installationID int64) (ghclient.Client, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if _, bad := l.failing[installationID]; bad {
		return nil, fmt.Errorf("installation %d client previously failed; not retrying", installationID)
	}

	if c, ok := l.scoped[installationID]; ok {
		return c, nil
	}

	c, err := l.client.CreateInstallationClient(ctx, installationID)
	if err != nil {
		l.failing[installationID] = struct{}{}

		return nil, fmt.Errorf("create installation %d client: %w", installationID, err)
	}

	l.scoped[installationID] = c

	return c, nil
}

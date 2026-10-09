package github

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	gh "github.com/google/go-github/v68/github"

	"github.com/donaldgifford/repo-guardian/internal/control"
)

// maxBranchCommits bounds ListCommits. Listing a branch's commits walks
// its whole ancestry, main's history included; the workflows only look
// at the commits a remediation branch adds, which is a handful. 250 is
// also GitHub's own cap on a pull request's commit list.
const maxBranchCommits = 250

// RepoObserver is control.PRObserver bound to one repository (IMPL-0028
// task 2.3). Unlike RepoReader it caches nothing: the workflows read PR
// and branch state to decide a write, so every call asks GitHub.
//
// Build one with GitHubClient.RepoObserver.
type RepoObserver struct {
	client *GitHubClient
	owner  string
	repo   string
}

var _ control.PRObserver = (*RepoObserver)(nil)

// RepoObserver returns a PRObserver for owner/repo.
func (c *GitHubClient) RepoObserver(owner, repo string) *RepoObserver {
	return &RepoObserver{client: c, owner: owner, repo: repo}
}

// ListPullRequests returns the open PRs whose head branch starts with
// headPrefix. GitHub's head filter matches one exact ref, so every open
// PR is listed and filtered here; a failed page fails the call.
func (o *RepoObserver) ListPullRequests(ctx context.Context, headPrefix string) ([]control.PullRequest, error) {
	opts := &gh.PullRequestListOptions{State: "open", ListOptions: gh.ListOptions{PerPage: 100}}

	var out []control.PullRequest

	for {
		prs, resp, err := o.client.ghClient().PullRequests.List(ctx, o.owner, o.repo, opts)
		if err != nil {
			return nil, fmt.Errorf("listing pull requests of %s/%s: %w", o.owner, o.repo, err)
		}

		for _, pr := range prs {
			if strings.HasPrefix(pr.GetHead().GetRef(), headPrefix) {
				out = append(out, ControlPullRequest(pr))
			}
		}

		if resp.NextPage == 0 {
			return out, nil
		}

		opts.Page = resp.NextPage
	}
}

// GetPullRequest returns PR number with its author and head repository.
func (o *RepoObserver) GetPullRequest(ctx context.Context, number int) (*control.PullRequest, error) {
	pr, _, err := o.client.ghClient().PullRequests.Get(ctx, o.owner, o.repo, number)
	if err != nil {
		return nil, fmt.Errorf("reading pull request %s/%s#%d: %w", o.owner, o.repo, number, err)
	}

	out := ControlPullRequest(pr)

	return &out, nil
}

// ListCommits returns branch's commits newest first, at most
// maxBranchCommits of them.
func (o *RepoObserver) ListCommits(ctx context.Context, branch string) ([]control.Commit, error) {
	opts := &gh.CommitsListOptions{SHA: branch, ListOptions: gh.ListOptions{PerPage: 100}}

	var out []control.Commit

	for len(out) < maxBranchCommits {
		commits, resp, err := o.client.ghClient().Repositories.ListCommits(ctx, o.owner, o.repo, opts)
		if err != nil {
			return nil, fmt.Errorf("listing commits of %s in %s/%s: %w", branch, o.owner, o.repo, err)
		}

		for _, c := range commits {
			out = append(out, control.Commit{
				SHA:            c.GetSHA(),
				Message:        c.GetCommit().GetMessage(),
				AuthorID:       c.GetAuthor().GetID(),
				AuthorLogin:    c.GetAuthor().GetLogin(),
				CommitterLogin: c.GetCommitter().GetLogin(),
				Verified:       c.GetCommit().GetVerification().GetVerified(),
			})
		}

		if resp.NextPage == 0 {
			break
		}

		opts.Page = resp.NextPage
	}

	if len(out) > maxBranchCommits {
		out = out[:maxBranchCommits]
	}

	return out, nil
}

// GetRef returns branch's head SHA; exists is false when the branch is
// absent.
func (o *RepoObserver) GetRef(ctx context.Context, branch string) (string, bool, error) {
	ref, resp, err := o.client.ghClient().Git.GetRef(ctx, o.owner, o.repo, "heads/"+branch)
	if err != nil {
		if isStatus(resp, http.StatusNotFound) {
			return "", false, nil
		}

		return "", false, fmt.Errorf("reading ref %s of %s/%s: %w", branch, o.owner, o.repo, err)
	}

	return ref.GetObject().GetSHA(), true, nil
}

// ControlPullRequest converts a go-github PR. It captures what adoption
// needs (DESIGN-0032 D16): the author's user id and type, which GitHub
// authenticates, and the head repository id, which is 0 when the fork
// was deleted.
func ControlPullRequest(pr *gh.PullRequest) control.PullRequest {
	return control.PullRequest{
		Number:      pr.GetNumber(),
		Title:       pr.GetTitle(),
		Body:        pr.GetBody(),
		URL:         pr.GetHTMLURL(),
		State:       pr.GetState(),
		Merged:      pr.GetMerged(),
		BaseRef:     pr.GetBase().GetRef(),
		HeadRef:     pr.GetHead().GetRef(),
		HeadSHA:     pr.GetHead().GetSHA(),
		HeadRepoID:  pr.GetHead().GetRepo().GetID(),
		AuthorID:    pr.GetUser().GetID(),
		AuthorLogin: pr.GetUser().GetLogin(),
		AuthorType:  pr.GetUser().GetType(),
		CreatedAt:   pr.GetCreatedAt().Time,
	}
}

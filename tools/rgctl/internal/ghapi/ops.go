package ghapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	gh "github.com/google/go-github/v75/github"
)

const (
	perPage = 100
	// searchCap is the most results GitHub search returns for one query.
	searchCap = 1000
)

// SearchOpenPRs searches org for open pull requests authored by the App
// whose head branch starts with headPrefix. Hits are leads, not evidence:
// read each back with [Client.GetPR].
func (c *Client) SearchOpenPRs(ctx context.Context, org, headPrefix string) ([]SearchHit, SearchStatus, error) {
	var status SearchStatus
	cl, err := c.clientFor(org)
	if err != nil {
		return nil, status, err
	}
	slug := strings.TrimSuffix(c.botLogin, "[bot]")
	query := fmt.Sprintf("is:pr is:open org:%s author:app/%s head:%s", org, slug, headPrefix)
	opts := &gh.SearchOptions{ListOptions: gh.ListOptions{PerPage: perPage}}

	var hits []SearchHit
	for {
		if err := c.paceSearch(ctx); err != nil {
			return nil, status, err
		}
		var res *gh.IssuesSearchResult
		var resp *gh.Response
		if err := c.call(ctx, "search pull requests", func() (err error) {
			res, resp, err = cl.Search.Issues(ctx, query, opts)
			return err
		}); err != nil {
			return nil, status, err
		}
		status.Pages++
		status.Total = res.GetTotal()
		status.Incomplete = status.Incomplete || res.GetIncompleteResults()
		for _, is := range res.Issues {
			hits = append(hits, SearchHit{Repo: repoFromURL(is.GetRepositoryURL()), Number: is.GetNumber()})
		}
		if resp.NextPage == 0 || len(hits) >= searchCap {
			break
		}
		opts.Page = resp.NextPage
	}
	status.Capped = status.Total > searchCap || len(hits) >= searchCap
	return hits, status, nil
}

// repoFromURL turns an API repository URL (…/repos/owner/name) into
// owner/name.
func repoFromURL(u string) string {
	_, rest, ok := strings.Cut(u, "/repos/")
	if !ok {
		return u
	}
	return rest
}

// GetPR reads one pull request.
func (c *Client) GetPR(ctx context.Context, repo string, number int) (*PullRequest, error) {
	cl, owner, name, err := c.repoClient(repo)
	if err != nil {
		return nil, err
	}
	var pr *gh.PullRequest
	if err := c.call(ctx, "get pull request", func() (err error) {
		pr, _, err = cl.PullRequests.Get(ctx, owner, name, number)
		return err
	}); err != nil {
		return nil, err
	}
	return toPR(repo, pr), nil
}

// ListOpenPRs lists every open pull request in repo.
func (c *Client) ListOpenPRs(ctx context.Context, repo string) ([]PullRequest, error) {
	cl, owner, name, err := c.repoClient(repo)
	if err != nil {
		return nil, err
	}
	opts := &gh.PullRequestListOptions{State: "open", ListOptions: gh.ListOptions{PerPage: perPage}}
	var out []PullRequest
	for {
		var page []*gh.PullRequest
		var resp *gh.Response
		if err := c.call(ctx, "list pull requests", func() (err error) {
			page, resp, err = cl.PullRequests.List(ctx, owner, name, opts)
			return err
		}); err != nil {
			return nil, err
		}
		for _, pr := range page {
			out = append(out, *toPR(repo, pr))
		}
		if resp.NextPage == 0 {
			return out, nil
		}
		opts.Page = resp.NextPage
	}
}

// ListPRCommits calls visit for each commit on a pull request, page by page,
// until visit returns false or the commits run out. A clean pull request
// therefore costs one page.
func (c *Client) ListPRCommits(ctx context.Context, repo string, number int, visit func(Commit) bool) error {
	cl, owner, name, err := c.repoClient(repo)
	if err != nil {
		return err
	}
	opts := &gh.ListOptions{PerPage: perPage}
	for {
		var page []*gh.RepositoryCommit
		var resp *gh.Response
		if err := c.call(ctx, "list pull request commits", func() (err error) {
			page, resp, err = cl.PullRequests.ListCommits(ctx, owner, name, number, opts)
			return err
		}); err != nil {
			return err
		}
		for _, rc := range page {
			if !visit(Commit{AuthorLogin: rc.GetAuthor().GetLogin(), CommitterLogin: rc.GetCommitter().GetLogin()}) {
				return nil
			}
		}
		if resp.NextPage == 0 {
			return nil
		}
		opts.Page = resp.NextPage
	}
}

// ListRepos lists org's repositories as owner/name: the installation's
// repositories under App auth (the set v1 could see), the org's under token
// auth.
func (c *Client) ListRepos(ctx context.Context, org string) ([]string, error) {
	cl, err := c.clientFor(org)
	if err != nil {
		return nil, err
	}
	var out []string
	add := func(rs []*gh.Repository) {
		for _, r := range rs {
			out = append(out, r.GetFullName())
		}
	}
	opts := gh.ListOptions{PerPage: perPage}
	for {
		var resp *gh.Response
		err := c.call(ctx, "list repositories", func() error {
			if c.mode == ModeApp {
				list, r, err := cl.Apps.ListRepos(ctx, &opts)
				if err == nil {
					add(list.Repositories)
				}
				resp = r
				return err
			}
			list, r, err := cl.Repositories.ListByOrg(ctx, org, &gh.RepositoryListByOrgOptions{ListOptions: opts})
			if err == nil {
				add(list)
			}
			resp = r
			return err
		})
		if err != nil {
			return nil, err
		}
		if resp.NextPage == 0 {
			return out, nil
		}
		opts.Page = resp.NextPage
	}
}

// ListComments lists the issue comments on a pull request.
func (c *Client) ListComments(ctx context.Context, repo string, number int) ([]Comment, error) {
	cl, owner, name, err := c.repoClient(repo)
	if err != nil {
		return nil, err
	}
	opts := &gh.IssueListCommentsOptions{ListOptions: gh.ListOptions{PerPage: perPage}}
	var out []Comment
	for {
		var page []*gh.IssueComment
		var resp *gh.Response
		if err := c.call(ctx, "list comments", func() (err error) {
			page, resp, err = cl.Issues.ListComments(ctx, owner, name, number, opts)
			return err
		}); err != nil {
			return nil, err
		}
		for _, cm := range page {
			out = append(out, Comment{ID: cm.GetID(), Author: cm.GetUser().GetLogin(), Body: cm.GetBody()})
		}
		if resp.NextPage == 0 {
			return out, nil
		}
		opts.Page = resp.NextPage
	}
}

// CreateComment posts an issue comment on a pull request.
func (c *Client) CreateComment(ctx context.Context, repo string, number int, body string) error {
	cl, owner, name, err := c.repoClient(repo)
	if err != nil {
		return err
	}
	return c.call(ctx, "create comment", func() error {
		_, _, err := cl.Issues.CreateComment(ctx, owner, name, number, &gh.IssueComment{Body: gh.Ptr(body)})
		return err
	})
}

// ClosePR sets a pull request's state to closed.
func (c *Client) ClosePR(ctx context.Context, repo string, number int) error {
	cl, owner, name, err := c.repoClient(repo)
	if err != nil {
		return err
	}
	return c.call(ctx, "close pull request", func() error {
		_, _, err := cl.PullRequests.Edit(ctx, owner, name, number, &gh.PullRequest{State: gh.Ptr("closed")})
		return err
	})
}

// BranchExists reports whether a branch is present.
func (c *Client) BranchExists(ctx context.Context, repo, branch string) (bool, error) {
	cl, owner, name, err := c.repoClient(repo)
	if err != nil {
		return false, err
	}
	var exists bool
	err = c.call(ctx, "get branch", func() error {
		_, _, err := cl.Git.GetRef(ctx, owner, name, "heads/"+branch)
		if er, ok := errors.AsType[*gh.ErrorResponse](err); ok && er.Response != nil && er.Response.StatusCode == http.StatusNotFound {
			exists = false
			return nil
		}
		exists = err == nil
		return err
	})
	return exists, err
}

// DeleteBranch deletes a branch. A branch that does not exist is not an
// error, so a re-run after a partial failure is safe.
func (c *Client) DeleteBranch(ctx context.Context, repo, branch string) error {
	cl, owner, name, err := c.repoClient(repo)
	if err != nil {
		return err
	}
	return c.call(ctx, "delete branch", func() error {
		_, err := cl.Git.DeleteRef(ctx, owner, name, "heads/"+branch)
		if missingRef(err) {
			return nil
		}
		return err
	})
}

// missingRef reports GitHub's answers for a branch that is already gone:
// 422 "Reference does not exist", or 404.
func missingRef(err error) bool {
	er, ok := errors.AsType[*gh.ErrorResponse](err)
	if !ok || er.Response == nil {
		return false
	}
	switch er.Response.StatusCode {
	case http.StatusNotFound:
		return true
	case http.StatusUnprocessableEntity:
		return strings.Contains(er.Message, "Reference does not exist")
	}
	return false
}

func (c *Client) repoClient(repo string) (cl *gh.Client, owner, name string, err error) {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return nil, "", "", fmt.Errorf("repository %q is not owner/name", repo)
	}
	cl, err = c.clientFor(owner)
	return cl, owner, name, err
}

func toPR(repo string, pr *gh.PullRequest) *PullRequest {
	return &PullRequest{
		Repo:       repo,
		Number:     pr.GetNumber(),
		URL:        pr.GetHTMLURL(),
		State:      pr.GetState(),
		Title:      pr.GetTitle(),
		Author:     pr.GetUser().GetLogin(),
		HeadRef:    pr.GetHead().GetRef(),
		HeadSHA:    pr.GetHead().GetSHA(),
		HeadRepoID: pr.GetHead().GetRepo().GetID(),
		BaseRepoID: pr.GetBase().GetRepo().GetID(),
		CreatedAt:  pr.GetCreatedAt().Time,
		UpdatedAt:  pr.GetUpdatedAt().Time,
	}
}

package github

import (
	"context"
	"encoding/json"
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

// NewClient creates a new GitHubClient configured as a GitHub App using a
// private key file on disk.
func NewClient(appID int64, privateKeyPath string, logger *slog.Logger, rateLimitThreshold float64) (*GitHubClient, error) {
	transport, err := ghinstallation.NewAppsTransportKeyFromFile(http.DefaultTransport, appID, privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("creating GitHub App transport: %w", err)
	}

	return newClientFromTransport(transport, logger, rateLimitThreshold), nil
}

// NewClientFromKeyBytes creates a new GitHubClient configured as a GitHub App
// using a PEM-encoded private key provided as raw bytes (e.g. from an env var
// or secret).
func NewClientFromKeyBytes(appID int64, privateKey []byte, logger *slog.Logger, rateLimitThreshold float64) (*GitHubClient, error) {
	transport, err := ghinstallation.NewAppsTransport(http.DefaultTransport, appID, privateKey)
	if err != nil {
		return nil, fmt.Errorf("creating GitHub App transport from key bytes: %w", err)
	}

	return newClientFromTransport(transport, logger, rateLimitThreshold), nil
}

func newClientFromTransport(transport *ghinstallation.AppsTransport, logger *slog.Logger, rateLimitThreshold float64) *GitHubClient {
	rlTransport := newRateLimitTransport(transport, logger.With("component", "ratelimit"), rateLimitThreshold)
	appClient := gh.NewClient(&http.Client{Transport: rlTransport})

	return &GitHubClient{
		appTransport:       transport,
		appClient:          appClient,
		logger:             logger,
		rateLimitThreshold: rateLimitThreshold,
		installClients:     make(map[int64]*gh.Client),
	}
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
//
// If the file already exists on the branch with identical content, it is a
// no-op. If it exists with different content, the existing blob sha is
// forwarded so GitHub treats the call as an update rather than a create
// (which would otherwise return 422 "sha wasn't supplied").
func (c *GitHubClient) CreateOrUpdateFile(
	ctx context.Context,
	owner, repo, branch, path, content, message string,
) error {
	existing, _, resp, err := c.ghClient().Repositories.GetContents(
		ctx, owner, repo, path,
		&gh.RepositoryContentGetOptions{Ref: branch},
	)
	if err != nil && (resp == nil || resp.StatusCode != http.StatusNotFound) {
		return fmt.Errorf("checking file %s on %s in %s/%s: %w", path, branch, owner, repo, err)
	}

	opts := &gh.RepositoryContentFileOptions{
		Message: gh.Ptr(message),
		Content: []byte(content),
		Branch:  gh.Ptr(branch),
	}

	if existing != nil {
		decoded, decodeErr := existing.GetContent()
		if decodeErr != nil {
			return fmt.Errorf("decoding existing %s on %s in %s/%s: %w", path, branch, owner, repo, decodeErr)
		}

		if decoded == content {
			return nil
		}

		opts.SHA = existing.SHA

		if _, _, err := c.ghClient().Repositories.UpdateFile(ctx, owner, repo, path, opts); err != nil {
			return fmt.Errorf("updating file %s on %s in %s/%s: %w", path, branch, owner, repo, err)
		}

		return nil
	}

	if _, _, err := c.ghClient().Repositories.CreateFile(ctx, owner, repo, path, opts); err != nil {
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

// AddLabelsToPR attaches the given label names to a PR. PRs are
// addressable through the issues endpoint, so this calls
// Issues.AddLabelsToIssue under the hood. A nil or empty labels slice
// is a no-op.
func (c *GitHubClient) AddLabelsToPR(
	ctx context.Context,
	owner, repo string,
	prNumber int,
	labels []string,
) error {
	if len(labels) == 0 {
		return nil
	}

	if _, _, err := c.ghClient().Issues.AddLabelsToIssue(ctx, owner, repo, prNumber, labels); err != nil {
		return fmt.Errorf("adding labels to PR %s/%s#%d: %w", owner, repo, prNumber, err)
	}

	return nil
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

// GetFileContent returns the decoded content of a file in a repository.
// Returns empty string and no error if the file does not exist.
func (c *GitHubClient) GetFileContent(ctx context.Context, owner, repo, path string) (string, error) {
	file, _, resp, err := c.ghClient().Repositories.GetContents(ctx, owner, repo, path, nil)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return "", nil
		}

		return "", fmt.Errorf("getting file content %s/%s/%s: %w", owner, repo, path, err)
	}

	if file == nil {
		return "", nil
	}

	content, err := file.GetContent()
	if err != nil {
		return "", fmt.Errorf("decoding file content %s/%s/%s: %w", owner, repo, path, err)
	}

	return content, nil
}

// GetCustomPropertyValues returns all custom property values set on a repository.
func (c *GitHubClient) GetCustomPropertyValues(
	ctx context.Context,
	owner, repo string,
) ([]*CustomPropertyValue, error) {
	ghProps, _, err := c.ghClient().Repositories.GetAllCustomPropertyValues(ctx, owner, repo)
	if err != nil {
		return nil, fmt.Errorf("getting custom properties for %s/%s: %w", owner, repo, err)
	}

	props := make([]*CustomPropertyValue, 0, len(ghProps))
	for _, p := range ghProps {
		value := ""
		if p.Value != nil {
			value = fmt.Sprintf("%v", p.Value)
		}

		props = append(props, &CustomPropertyValue{
			PropertyName: p.PropertyName,
			Value:        value,
		})
	}

	return props, nil
}

// SetCustomPropertyValues creates or updates custom property values on a repository.
func (c *GitHubClient) SetCustomPropertyValues(
	ctx context.Context,
	owner, repo string,
	properties []*CustomPropertyValue,
) error {
	ghProps := make([]*gh.CustomPropertyValue, 0, len(properties))
	for _, p := range properties {
		ghProps = append(ghProps, &gh.CustomPropertyValue{
			PropertyName: p.PropertyName,
			Value:        p.Value,
		})
	}

	_, err := c.ghClient().Repositories.CreateOrUpdateCustomProperties(ctx, owner, repo, ghProps)
	if err != nil {
		return fmt.Errorf("setting custom properties for %s/%s: %w", owner, repo, err)
	}

	return nil
}

// GetVulnerabilityAlertsEnabled checks if vulnerability alerts are enabled.
func (c *GitHubClient) GetVulnerabilityAlertsEnabled(ctx context.Context, owner, repo string) (bool, error) {
	enabled, _, err := c.ghClient().Repositories.GetVulnerabilityAlerts(ctx, owner, repo)
	if err != nil {
		return false, fmt.Errorf("getting vulnerability alerts for %s/%s: %w", owner, repo, err)
	}

	return enabled, nil
}

// EnableVulnerabilityAlerts enables vulnerability alerts for a repository.
func (c *GitHubClient) EnableVulnerabilityAlerts(ctx context.Context, owner, repo string) error {
	_, err := c.ghClient().Repositories.EnableVulnerabilityAlerts(ctx, owner, repo)
	if err != nil {
		return fmt.Errorf("enabling vulnerability alerts for %s/%s: %w", owner, repo, err)
	}

	return nil
}

// DisableVulnerabilityAlerts disables vulnerability alerts for a repository.
func (c *GitHubClient) DisableVulnerabilityAlerts(ctx context.Context, owner, repo string) error {
	_, err := c.ghClient().Repositories.DisableVulnerabilityAlerts(ctx, owner, repo)
	if err != nil {
		return fmt.Errorf("disabling vulnerability alerts for %s/%s: %w", owner, repo, err)
	}

	return nil
}

// GetRepoSettings returns the repository settings.
func (c *GitHubClient) GetRepoSettings(ctx context.Context, owner, repo string) (*RepoSettings, error) {
	r, _, err := c.ghClient().Repositories.Get(ctx, owner, repo)
	if err != nil {
		return nil, fmt.Errorf("getting repo settings for %s/%s: %w", owner, repo, err)
	}

	return &RepoSettings{
		DefaultBranch:       r.GetDefaultBranch(),
		HasIssues:           r.GetHasIssues(),
		HasWiki:             r.GetHasWiki(),
		DeleteBranchOnMerge: r.GetDeleteBranchOnMerge(),
		AllowMergeCommit:    r.GetAllowMergeCommit(),
		AllowSquashMerge:    r.GetAllowSquashMerge(),
		AllowRebaseMerge:    r.GetAllowRebaseMerge(),
	}, nil
}

// UpdateRepository updates repository settings.
func (c *GitHubClient) UpdateRepository(ctx context.Context, owner, repo string, opts *RepoUpdateOpts) error {
	update := &gh.Repository{}

	if opts.HasIssues != nil {
		update.HasIssues = opts.HasIssues
	}

	if opts.HasWiki != nil {
		update.HasWiki = opts.HasWiki
	}

	if opts.DeleteBranchOnMerge != nil {
		update.DeleteBranchOnMerge = opts.DeleteBranchOnMerge
	}

	if opts.AllowMergeCommit != nil {
		update.AllowMergeCommit = opts.AllowMergeCommit
	}

	if opts.AllowSquashMerge != nil {
		update.AllowSquashMerge = opts.AllowSquashMerge
	}

	if opts.AllowRebaseMerge != nil {
		update.AllowRebaseMerge = opts.AllowRebaseMerge
	}

	if opts.DefaultBranch != nil {
		update.DefaultBranch = opts.DefaultBranch
	}

	_, _, err := c.ghClient().Repositories.Edit(ctx, owner, repo, update)
	if err != nil {
		return fmt.Errorf("updating repository %s/%s: %w", owner, repo, err)
	}

	return nil
}

// ListRepositoryRulesets returns all rulesets for a repository.
func (c *GitHubClient) ListRepositoryRulesets(ctx context.Context, owner, repo string) ([]*Ruleset, error) {
	ghRulesets, _, err := c.ghClient().Repositories.GetAllRulesets(ctx, owner, repo, false)
	if err != nil {
		return nil, fmt.Errorf("listing rulesets for %s/%s: %w", owner, repo, err)
	}

	rulesets := make([]*Ruleset, 0, len(ghRulesets))
	for _, rs := range ghRulesets {
		rulesets = append(rulesets, convertRuleset(rs))
	}

	return rulesets, nil
}

// GetRepositoryRuleset returns a specific ruleset by ID.
func (c *GitHubClient) GetRepositoryRuleset(ctx context.Context, owner, repo string, rulesetID int64) (*Ruleset, error) {
	rs, _, err := c.ghClient().Repositories.GetRuleset(ctx, owner, repo, rulesetID, false)
	if err != nil {
		return nil, fmt.Errorf("getting ruleset %d for %s/%s: %w", rulesetID, owner, repo, err)
	}

	return convertRuleset(rs), nil
}

// CreateRepositoryRuleset creates a new repository ruleset.
func (c *GitHubClient) CreateRepositoryRuleset(ctx context.Context, owner, repo string, ruleset *Ruleset) (*Ruleset, error) {
	ghRuleset := buildGHRuleset(ruleset)

	created, _, err := c.ghClient().Repositories.CreateRuleset(ctx, owner, repo, ghRuleset)
	if err != nil {
		return nil, fmt.Errorf("creating ruleset for %s/%s: %w", owner, repo, err)
	}

	return convertRuleset(created), nil
}

// UpdateRepositoryRuleset updates an existing repository ruleset.
func (c *GitHubClient) UpdateRepositoryRuleset(
	ctx context.Context,
	owner, repo string,
	rulesetID int64,
	ruleset *Ruleset,
) (*Ruleset, error) {
	ghRuleset := buildGHRuleset(ruleset)

	updated, _, err := c.ghClient().Repositories.UpdateRuleset(ctx, owner, repo, rulesetID, ghRuleset)
	if err != nil {
		return nil, fmt.Errorf("updating ruleset %d for %s/%s: %w", rulesetID, owner, repo, err)
	}

	return convertRuleset(updated), nil
}

func convertRuleset(rs *gh.Ruleset) *Ruleset {
	r := &Ruleset{
		ID:          rs.GetID(),
		Name:        rs.Name,
		Enforcement: rs.Enforcement,
		Target:      rs.GetTarget(),
	}

	if rs.Conditions != nil && rs.Conditions.RefName != nil {
		r.Conditions = &RulesetConditions{
			IncludePatterns: rs.Conditions.RefName.Include,
			ExcludePatterns: rs.Conditions.RefName.Exclude,
		}
	}

	for _, rule := range rs.Rules {
		switch rule.Type {
		case "pull_request":
			if rule.Parameters != nil {
				var prParams gh.PullRequestRuleParameters
				if err := json.Unmarshal(*rule.Parameters, &prParams); err == nil {
					r.RequirePullRequest = &RulesetPullRequest{
						RequiredApprovals:      prParams.RequiredApprovingReviewCount,
						DismissStaleReviews:    prParams.DismissStaleReviewsOnPush,
						RequireCodeOwnerReview: prParams.RequireCodeOwnerReview,
					}
				}
			}
		case "required_linear_history":
			r.RequireLinearHistory = true
		case "required_status_checks":
			if rule.Parameters != nil {
				var scParams gh.RequiredStatusChecksRuleParameters
				if err := json.Unmarshal(*rule.Parameters, &scParams); err == nil {
					checks := make([]string, 0, len(scParams.RequiredStatusChecks))
					for _, sc := range scParams.RequiredStatusChecks {
						checks = append(checks, sc.Context)
					}

					r.RequireStatusChecks = &RulesetStatusChecks{
						RequiredChecks:     checks,
						StrictStatusChecks: scParams.StrictRequiredStatusChecksPolicy,
					}
				}
			}
		}
	}

	return r
}

func buildGHRuleset(ruleset *Ruleset) *gh.Ruleset {
	rs := &gh.Ruleset{
		Name:        ruleset.Name,
		Enforcement: ruleset.Enforcement,
		Target:      gh.Ptr(ruleset.Target),
	}

	if ruleset.Conditions != nil {
		rs.Conditions = &gh.RulesetConditions{
			RefName: &gh.RulesetRefConditionParameters{
				Include: ruleset.Conditions.IncludePatterns,
				Exclude: ruleset.Conditions.ExcludePatterns,
			},
		}
	}

	var rules []*gh.RepositoryRule

	if ruleset.RequirePullRequest != nil {
		pr := ruleset.RequirePullRequest
		rules = append(rules, gh.NewPullRequestRule(&gh.PullRequestRuleParameters{
			RequiredApprovingReviewCount: pr.RequiredApprovals,
			DismissStaleReviewsOnPush:    pr.DismissStaleReviews,
			RequireCodeOwnerReview:       pr.RequireCodeOwnerReview,
		}))
	}

	if ruleset.RequireLinearHistory {
		rules = append(rules, gh.NewRequiredLinearHistoryRule())
	}

	if ruleset.RequireStatusChecks != nil {
		sc := ruleset.RequireStatusChecks
		checks := make([]gh.RuleRequiredStatusChecks, 0, len(sc.RequiredChecks))

		for _, check := range sc.RequiredChecks {
			checks = append(checks, gh.RuleRequiredStatusChecks{Context: check})
		}

		rules = append(rules, gh.NewRequiredStatusChecksRule(&gh.RequiredStatusChecksRuleParameters{
			RequiredStatusChecks:             checks,
			StrictRequiredStatusChecksPolicy: sc.StrictStatusChecks,
		}))
	}

	rs.Rules = rules

	return rs
}

// ListLabels returns all labels for a repository.
func (c *GitHubClient) ListLabels(ctx context.Context, owner, repo string) ([]*Label, error) {
	opts := &gh.ListOptions{PerPage: 100}

	var allLabels []*Label

	for {
		labels, resp, err := c.ghClient().Issues.ListLabels(ctx, owner, repo, opts)
		if err != nil {
			return nil, fmt.Errorf("listing labels for %s/%s: %w", owner, repo, err)
		}

		for _, l := range labels {
			allLabels = append(allLabels, &Label{
				Name:        l.GetName(),
				Color:       l.GetColor(),
				Description: l.GetDescription(),
			})
		}

		if resp.NextPage == 0 {
			break
		}

		opts.Page = resp.NextPage
	}

	return allLabels, nil
}

// CreateLabel creates a new label in the repository.
func (c *GitHubClient) CreateLabel(ctx context.Context, owner, repo string, label *Label) error {
	_, _, err := c.ghClient().Issues.CreateLabel(ctx, owner, repo, &gh.Label{
		Name:        gh.Ptr(label.Name),
		Color:       gh.Ptr(label.Color),
		Description: gh.Ptr(label.Description),
	})
	if err != nil {
		return fmt.Errorf("creating label %q for %s/%s: %w", label.Name, owner, repo, err)
	}

	return nil
}

// UpdateLabel updates an existing label.
func (c *GitHubClient) UpdateLabel(ctx context.Context, owner, repo, name string, label *Label) error {
	_, _, err := c.ghClient().Issues.EditLabel(ctx, owner, repo, name, &gh.Label{
		Name:        gh.Ptr(label.Name),
		Color:       gh.Ptr(label.Color),
		Description: gh.Ptr(label.Description),
	})
	if err != nil {
		return fmt.Errorf("updating label %q for %s/%s: %w", name, owner, repo, err)
	}

	return nil
}

// DeleteLabel deletes a label from the repository.
func (c *GitHubClient) DeleteLabel(ctx context.Context, owner, repo, name string) error {
	_, err := c.ghClient().Issues.DeleteLabel(ctx, owner, repo, name)
	if err != nil {
		return fmt.Errorf("deleting label %q for %s/%s: %w", name, owner, repo, err)
	}

	return nil
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

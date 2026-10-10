// Package write is control.Writer over GitHub: the only code that
// changes a repository on behalf of a control (DESIGN-0031, IMPL-0028
// task 2.6). It is a package of its own so depguard can keep it out of
// every evaluator-side package (OQ5): an evaluation that can import the
// writer can write, whatever its interfaces say.
//
// The Remediation App's installation token backs it. There is no branch
// delete (D30).
package write

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	gh "github.com/google/go-github/v68/github"
	"github.com/shurcooL/githubv4"

	"github.com/donaldgifford/repo-guardian/internal/control"
	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
)

// Change-set bounds. createCommitOnBranch accepted more in the Phase 0
// spike; these are repo-guardian's own bound, kept because no control
// needs more (DESIGN-0031 AR-0031-09, amended 2026-10-09).
const (
	maxCommitFiles = 100
	maxFileBytes   = 1 << 20
)

// staleData is the GraphQL error type of a createCommitOnBranch whose
// expectedHeadOid is not the branch head (INV-0022 spike 1).
const staleData = "STALE_DATA"

// Writer is control.Writer bound to one repository.
type Writer struct {
	client *ghclient.GitHubClient
	owner  string
	repo   string
}

var _ control.Writer = (*Writer)(nil)

// New returns a Writer for owner/repo on client, which must be scoped to
// the Remediation App's installation.
func New(client *ghclient.GitHubClient, owner, repo string) *Writer {
	return &Writer{client: client, owner: owner, repo: repo}
}

// rest returns the go-github client on the shared transport chain.
func (w *Writer) rest() *gh.Client { return w.client.RESTClient() }

// Commit publishes changes as one commit on branch with expectedHeadOid
// = baseSHA. A STALE_DATA refusal is control.ErrExpectedHeadMismatch.
func (w *Writer) Commit(ctx context.Context, branch, baseSHA string, changes []control.FileChange, message string) (string, error) {
	fileChanges, err := toFileChanges(changes)
	if err != nil {
		return "", fmt.Errorf("committing to %s in %s/%s: %w", branch, w.owner, w.repo, err)
	}

	headline, body, _ := strings.Cut(message, "\n")

	input := githubv4.CreateCommitOnBranchInput{
		Branch: githubv4.CommittableBranch{
			RepositoryNameWithOwner: githubv4.NewString(githubv4.String(w.owner + "/" + w.repo)),
			BranchName:              githubv4.NewString(githubv4.String(branch)),
		},
		Message:         githubv4.CommitMessage{Headline: githubv4.String(headline)},
		ExpectedHeadOid: githubv4.GitObjectID(baseSHA),
		FileChanges:     fileChanges,
	}

	if body = strings.TrimSpace(body); body != "" {
		input.Message.Body = githubv4.NewString(githubv4.String(body))
	}

	var m struct {
		CreateCommitOnBranch struct {
			Commit struct {
				Oid githubv4.GitObjectID
			}
		} `graphql:"createCommitOnBranch(input: $input)"`
	}

	ctx, types := ghclient.WithGraphQLErrorTypes(ctx)

	if err := w.client.GraphQLClient().Mutate(ctx, &m, input, nil); err != nil {
		if types.Has(staleData) {
			return "", fmt.Errorf("committing to %s in %s/%s: %w: %w", branch, w.owner, w.repo, control.ErrExpectedHeadMismatch, err)
		}

		return "", fmt.Errorf("committing to %s in %s/%s: %w", branch, w.owner, w.repo, err)
	}

	return string(m.CreateCommitOnBranch.Commit.Oid), nil
}

// toFileChanges converts and bounds a change set. createCommitOnBranch
// writes regular files only and needs unique paths.
func toFileChanges(changes []control.FileChange) (*githubv4.FileChanges, error) {
	if len(changes) == 0 {
		return nil, errors.New("empty change set")
	}

	if len(changes) > maxCommitFiles {
		return nil, fmt.Errorf("change set has %d files, over the bound of %d", len(changes), maxCommitFiles)
	}

	var (
		additions []githubv4.FileAddition
		deletions []githubv4.FileDeletion
		seen      = make(map[string]bool, len(changes))
	)

	for _, c := range changes {
		if seen[c.Path] {
			return nil, fmt.Errorf("path %s appears twice in the change set", c.Path)
		}

		seen[c.Path] = true

		if c.Delete {
			deletions = append(deletions, githubv4.FileDeletion{Path: githubv4.String(c.Path)})
			continue
		}

		if len(c.Content) > maxFileBytes {
			return nil, fmt.Errorf("%s is %d bytes, over the bound of %d", c.Path, len(c.Content), maxFileBytes)
		}

		additions = append(additions, githubv4.FileAddition{
			Path:     githubv4.String(c.Path),
			Contents: githubv4.Base64String(base64.StdEncoding.EncodeToString(c.Content)),
		})
	}

	var fc githubv4.FileChanges
	if additions != nil {
		fc.Additions = &additions
	}

	if deletions != nil {
		fc.Deletions = &deletions
	}

	return &fc, nil
}

// CreateRef creates branch at sha. GitHub refuses (422) when the branch
// exists, and that refusal is returned as an error.
func (w *Writer) CreateRef(ctx context.Context, branch, sha string) error {
	ref := &gh.Reference{Ref: gh.Ptr("refs/heads/" + branch), Object: &gh.GitObject{SHA: gh.Ptr(sha)}}

	if _, _, err := w.rest().Git.CreateRef(ctx, w.owner, w.repo, ref); err != nil {
		return fmt.Errorf("creating branch %s in %s/%s: %w", branch, w.owner, w.repo, err)
	}

	return nil
}

// UpdateBranch merges the base into PR number's head through GitHub's
// update-branch with expected_head_sha. A 202 (and an undocumented 200)
// is control.ErrUpdateBranchPending.
//
// Three different outcomes share 422, told apart only by free text
// (INV-0022 spike 2), so a 422 is classified from state instead: the PR
// is re-read, and a head that is no longer expectedHeadSHA is
// ErrExpectedHeadMismatch, a head not behind its base is a no-op (nil),
// and a head still behind is ErrMergeConflict. The message is carried in
// the error, never matched.
func (w *Writer) UpdateBranch(ctx context.Context, number int, expectedHeadSHA string) error {
	_, resp, err := w.rest().PullRequests.UpdateBranch(ctx, w.owner, w.repo, number,
		&gh.PullRequestBranchUpdateOptions{ExpectedHeadSHA: gh.Ptr(expectedHeadSHA)})

	var accepted *gh.AcceptedError

	switch {
	case err == nil, errors.As(err, &accepted):
		return fmt.Errorf("updating branch of %s/%s#%d: %w", w.owner, w.repo, number, control.ErrUpdateBranchPending)
	case resp != nil && resp.StatusCode == http.StatusUnprocessableEntity:
		return w.classifyUpdateBranch(ctx, number, expectedHeadSHA, err)
	default:
		return fmt.Errorf("updating branch of %s/%s#%d: %w", w.owner, w.repo, number, err)
	}
}

// classifyUpdateBranch turns a 422 from update-branch into an outcome
// from the PR's current state; cause is carried, never matched.
func (w *Writer) classifyUpdateBranch(ctx context.Context, number int, expectedHeadSHA string, cause error) error {
	pr, _, err := w.rest().PullRequests.Get(ctx, w.owner, w.repo, number)
	if err != nil {
		return fmt.Errorf("re-reading %s/%s#%d after update-branch refused (%w): %w", w.owner, w.repo, number, cause, err)
	}

	if head := pr.GetHead().GetSHA(); head != expectedHeadSHA {
		return fmt.Errorf("updating branch of %s/%s#%d: head is %s: %w: %w",
			w.owner, w.repo, number, head, control.ErrExpectedHeadMismatch, cause)
	}

	cmp, _, err := w.rest().Repositories.CompareCommits(ctx, w.owner, w.repo, pr.GetBase().GetRef(), expectedHeadSHA, nil)
	if err != nil {
		return fmt.Errorf("comparing %s/%s#%d after update-branch refused (%w): %w", w.owner, w.repo, number, cause, err)
	}

	if cmp.GetBehindBy() == 0 {
		return nil
	}

	return fmt.Errorf("updating branch of %s/%s#%d: %d commits behind: %w: %w",
		w.owner, w.repo, number, cmp.GetBehindBy(), control.ErrMergeConflict, cause)
}

// UpdatePullRequestBase points PR number at base.
func (w *Writer) UpdatePullRequestBase(ctx context.Context, number int, base string) error {
	patch := &gh.PullRequest{Base: &gh.PullRequestBranch{Ref: gh.Ptr(base)}}

	if _, _, err := w.rest().PullRequests.Edit(ctx, w.owner, w.repo, number, patch); err != nil {
		return fmt.Errorf("re-basing %s/%s#%d onto %s: %w", w.owner, w.repo, number, base, err)
	}

	return nil
}

// CreatePullRequest opens a PR from head into base.
func (w *Writer) CreatePullRequest(ctx context.Context, head, base, title, body string) (*control.PullRequest, error) {
	pr, _, err := w.rest().PullRequests.Create(ctx, w.owner, w.repo, &gh.NewPullRequest{
		Title: gh.Ptr(title),
		Head:  gh.Ptr(head),
		Base:  gh.Ptr(base),
		Body:  gh.Ptr(body),
	})
	if err != nil {
		return nil, fmt.Errorf("opening a PR from %s in %s/%s: %w", head, w.owner, w.repo, err)
	}

	out := ghclient.ControlPullRequest(pr)

	return &out, nil
}

// UpdatePullRequest replaces PR number's title and body.
func (w *Writer) UpdatePullRequest(ctx context.Context, number int, title, body string) error {
	return w.client.UpdatePullRequest(ctx, w.owner, w.repo, number, title, body)
}

// ClosePullRequest closes PR number without merging.
func (w *Writer) ClosePullRequest(ctx context.Context, number int) error {
	return w.client.ClosePullRequest(ctx, w.owner, w.repo, number)
}

// UpsertPRComment edits the comment on PR number whose first line is
// marker, or creates one.
func (w *Writer) UpsertPRComment(ctx context.Context, number int, marker, body string) error {
	return w.client.UpsertPRComment(ctx, w.owner, w.repo, number, marker, body)
}

// UpdateRepository writes the non-nil fields of settings. A
// security_and_analysis feature is written as its status.
func (w *Writer) UpdateRepository(ctx context.Context, s control.RepositorySettings) error { //nolint:gocritic // control.Writer's signature
	patch := &gh.Repository{
		DefaultBranch:            s.DefaultBranch,
		HasWiki:                  s.HasWiki,
		HasIssues:                s.HasIssues,
		HasProjects:              s.HasProjects,
		AllowMergeCommit:         s.AllowMergeCommit,
		AllowSquashMerge:         s.AllowSquashMerge,
		AllowRebaseMerge:         s.AllowRebaseMerge,
		AllowAutoMerge:           s.AllowAutoMerge,
		AllowUpdateBranch:        s.AllowUpdateBranch,
		DeleteBranchOnMerge:      s.DeleteBranchOnMerge,
		WebCommitSignoffRequired: s.WebCommitSignoffRequired,
		SquashMergeCommitTitle:   s.SquashMergeCommitTitle,
		SquashMergeCommitMessage: s.SquashMergeCommitMessage,
		MergeCommitTitle:         s.MergeCommitTitle,
		MergeCommitMessage:       s.MergeCommitMessage,
		SecurityAndAnalysis:      toSecurityAndAnalysis(s.SecurityAndAnalysis),
	}

	if _, _, err := w.rest().Repositories.Edit(ctx, w.owner, w.repo, patch); err != nil {
		return fmt.Errorf("updating %s/%s: %w", w.owner, w.repo, err)
	}

	return nil
}

// toSecurityAndAnalysis is the inverse of the reader's flattening.
// Unknown feature names are an error at policy load, not here; they are
// dropped.
func toSecurityAndAnalysis(m map[string]string) *gh.SecurityAndAnalysis {
	if len(m) == 0 {
		return nil
	}

	status := func(feature string) *string {
		if v, ok := m[feature]; ok {
			return gh.Ptr(v)
		}

		return nil
	}

	var s gh.SecurityAndAnalysis

	if v := status("advanced_security"); v != nil {
		s.AdvancedSecurity = &gh.AdvancedSecurity{Status: v}
	}

	if v := status("secret_scanning"); v != nil {
		s.SecretScanning = &gh.SecretScanning{Status: v}
	}

	if v := status("secret_scanning_push_protection"); v != nil {
		s.SecretScanningPushProtection = &gh.SecretScanningPushProtection{Status: v}
	}

	if v := status("dependabot_security_updates"); v != nil {
		s.DependabotSecurityUpdates = &gh.DependabotSecurityUpdates{Status: v}
	}

	if v := status("secret_scanning_validity_checks"); v != nil {
		s.SecretScanningValidityChecks = &gh.SecretScanningValidityChecks{Status: v}
	}

	return &s
}

// rulesetBody is the create/update payload; conditions, rules and
// bypass actors pass through as the policy wrote them.
type rulesetBody struct {
	Name         string          `json:"name"`
	Target       string          `json:"target,omitempty"`
	Enforcement  string          `json:"enforcement"`
	Conditions   json.RawMessage `json:"conditions,omitempty"`
	Rules        json.RawMessage `json:"rules,omitempty"`
	BypassActors json.RawMessage `json:"bypass_actors,omitempty"`
}

// UpsertRuleset creates rs when its ID is 0 and updates it by ID
// otherwise. Only a repository-sourced ruleset may be updated: an
// inherited org or enterprise ruleset is refused before any request.
func (w *Writer) UpsertRuleset(ctx context.Context, rs control.Ruleset) error { //nolint:gocritic // control.Writer's signature
	method, u := http.MethodPost, fmt.Sprintf("repos/%s/%s/rulesets", w.owner, w.repo)

	if rs.ID != 0 {
		if rs.Source != control.RulesetSourceRepository {
			return fmt.Errorf("ruleset %d in %s/%s has source %q; only repository rulesets are writable", rs.ID, w.owner, w.repo, rs.Source)
		}

		method, u = http.MethodPut, fmt.Sprintf("%s/%d", u, rs.ID)
	}

	req, err := w.rest().NewRequest(method, u, rulesetBody{
		Name:         rs.Name,
		Target:       rs.Target,
		Enforcement:  rs.Enforcement,
		Conditions:   rs.Conditions,
		Rules:        rs.Rules,
		BypassActors: rs.BypassActors,
	})
	if err != nil {
		return fmt.Errorf("building ruleset %q request: %w", rs.Name, err)
	}

	if _, err := w.rest().Do(ctx, req, nil); err != nil {
		return fmt.Errorf("writing ruleset %q in %s/%s: %w", rs.Name, w.owner, w.repo, err)
	}

	return nil
}

// SetCustomProperties writes props; a nil value clears the property.
func (w *Writer) SetCustomProperties(ctx context.Context, props map[string]*string) error {
	values := make([]*ghclient.CustomPropertyValue, 0, len(props))
	for name, v := range props {
		values = append(values, &ghclient.CustomPropertyValue{PropertyName: name, Value: v})
	}

	return w.client.SetCustomPropertyValues(ctx, w.owner, w.repo, values)
}

// UpsertLabel updates the label whose name matches l.Name ignoring case,
// as GitHub compares label names, or creates l.
func (w *Writer) UpsertLabel(ctx context.Context, l control.Label) error {
	existing, err := w.client.ListLabels(ctx, w.owner, w.repo)
	if err != nil {
		return err
	}

	label := &ghclient.Label{Name: l.Name, Color: l.Color, Description: l.Description}

	for _, e := range existing {
		if strings.EqualFold(e.Name, l.Name) {
			return w.client.UpdateLabel(ctx, w.owner, w.repo, e.Name, label)
		}
	}

	return w.client.CreateLabel(ctx, w.owner, w.repo, label)
}

// DeleteLabel deletes the label name.
func (w *Writer) DeleteLabel(ctx context.Context, name string) error {
	return w.client.DeleteLabel(ctx, w.owner, w.repo, name)
}

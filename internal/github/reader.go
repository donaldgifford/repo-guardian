package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"

	gh "github.com/google/go-github/v68/github"
	"github.com/shurcooL/githubv4"

	"github.com/donaldgifford/repo-guardian/internal/control"
)

// rulesetsPerPage is the page size for ruleset listing. GitHub's default
// is 30; the adapter asks for the maximum and still follows every page.
const rulesetsPerPage = 100

// RepoReader is control.Reader bound to one repository and one ref
// (IMPL-0028 task 2.2). File reads are pinned to ref, so every control
// in one evaluation sees the same tree; every successful read is cached
// for the reader's lifetime, which is one evaluation. A failed read is
// not cached, so a retry within the evaluation asks GitHub again.
//
// Build one per evaluation with GitHubClient.RepoReader.
type RepoReader struct {
	client *GitHubClient
	owner  string
	repo   string
	ref    string

	mu    sync.Mutex
	cache map[string]any
}

var _ control.Reader = (*RepoReader)(nil)

// RepoReader returns a Reader for owner/repo with file reads pinned to
// ref, a commit SHA. An empty ref reads the default branch, which only
// tests and previews should do: an evaluation pins its commit.
func (c *GitHubClient) RepoReader(owner, repo, ref string) *RepoReader {
	return &RepoReader{client: c, owner: owner, repo: repo, ref: ref, cache: make(map[string]any)}
}

// memo returns the cached result for key, or runs fetch and caches a
// successful result. Two concurrent misses may both fetch; the reads
// are idempotent, so that costs a request, not correctness.
func memo[T any](r *RepoReader, key string, fetch func() (T, error)) (T, error) {
	r.mu.Lock()
	v, ok := r.cache[key]
	r.mu.Unlock()

	if got, isT := v.(T); ok && isT {
		return got, nil
	}

	got, err := fetch()
	if err != nil {
		return got, err
	}

	r.mu.Lock()
	r.cache[key] = got
	r.mu.Unlock()

	return got, nil
}

// fileRead is one cached GetContents result.
type fileRead struct {
	content []byte
	sha     string
	found   bool
}

// GetContents reads path at the reader's ref. A missing path is found
// false with a nil error. A directory is an error: the caller asked for
// a file.
func (r *RepoReader) GetContents(ctx context.Context, path string) ([]byte, string, bool, error) {
	f, err := memo(r, "file:"+path, func() (fileRead, error) {
		fc, _, resp, err := r.client.ghClient().Repositories.GetContents(ctx, r.owner, r.repo, path, r.contentOpts())
		if err != nil {
			if isStatus(resp, http.StatusNotFound) {
				return fileRead{}, nil
			}

			return fileRead{}, fmt.Errorf("reading %s in %s/%s: %w", path, r.owner, r.repo, err)
		}

		if fc == nil {
			return fileRead{}, fmt.Errorf("reading %s in %s/%s: path is a directory", path, r.owner, r.repo)
		}

		content, err := fc.GetContent()
		if err != nil {
			return fileRead{}, fmt.Errorf("decoding %s in %s/%s: %w", path, r.owner, r.repo, err)
		}

		return fileRead{content: []byte(content), sha: fc.GetSHA(), found: true}, nil
	})
	if err != nil {
		return nil, "", false, err
	}

	return slices.Clone(f.content), f.sha, f.found, nil
}

// ListDirectory returns the entry names under path at the reader's ref,
// sorted; empty when the directory does not exist.
func (r *RepoReader) ListDirectory(ctx context.Context, path string) ([]string, error) {
	names, err := memo(r, "dir:"+path, func() ([]string, error) {
		_, dir, resp, err := r.client.ghClient().Repositories.GetContents(ctx, r.owner, r.repo, path, r.contentOpts())
		if err != nil {
			if isStatus(resp, http.StatusNotFound) {
				return []string{}, nil
			}

			return nil, fmt.Errorf("listing %s in %s/%s: %w", path, r.owner, r.repo, err)
		}

		names := make([]string, 0, len(dir))
		for _, e := range dir {
			names = append(names, e.GetName())
		}

		slices.Sort(names)

		return names, nil
	})

	return slices.Clone(names), err
}

func (r *RepoReader) contentOpts() *gh.RepositoryContentGetOptions {
	if r.ref == "" {
		return nil
	}

	return &gh.RepositoryContentGetOptions{Ref: r.ref}
}

// settingsQuery reads the merge-policy settings REST omits unless the
// caller holds a write permission; GraphQL returns them under the
// Evaluation App's read-only set (INV-0022 spike 3). Every field is a
// pointer so a null or missing field stays nil, never false.
type settingsQuery struct {
	Repository *struct {
		MergeCommitAllowed       *bool
		SquashMergeAllowed       *bool
		RebaseMergeAllowed       *bool
		AutoMergeAllowed         *bool
		AllowUpdateBranch        *bool
		DeleteBranchOnMerge      *bool
		WebCommitSignoffRequired *bool
		HasWikiEnabled           *bool
		HasIssuesEnabled         *bool
		HasProjectsEnabled       *bool
		SquashMergeCommitTitle   *string
		SquashMergeCommitMessage *string
		MergeCommitTitle         *string
		MergeCommitMessage       *string
	} `graphql:"repository(owner: $owner, name: $name)"`
}

// GetRepository reads the managed settings: the merge policy from one
// GraphQL query, the default branch and security_and_analysis from REST
// (Administration read returns the latter; without it the block is
// absent and the map stays nil).
//
// A GraphQL error that still returned the repository is a field-level
// refusal: the fields it covers are nil, and the read succeeds.
func (r *RepoReader) GetRepository(ctx context.Context) (control.RepositorySettings, error) {
	return memo(r, "repository", func() (control.RepositorySettings, error) {
		var q settingsQuery

		err := r.client.graphQL().Query(ctx, &q, map[string]any{
			"owner": githubv4.String(r.owner),
			"name":  githubv4.String(r.repo), //nolint:goconst // a GraphQL variable name, not shared vocabulary
		})
		if q.Repository == nil {
			if err == nil {
				err = errors.New("no repository in the response")
			}

			return control.RepositorySettings{}, fmt.Errorf("reading settings of %s/%s: %w", r.owner, r.repo, err)
		}

		rest, _, err := r.client.ghClient().Repositories.Get(ctx, r.owner, r.repo)
		if err != nil {
			return control.RepositorySettings{}, fmt.Errorf("reading %s/%s: %w", r.owner, r.repo, err)
		}

		g := q.Repository

		return control.RepositorySettings{
			DefaultBranch:            rest.DefaultBranch,
			HasWiki:                  g.HasWikiEnabled,
			HasIssues:                g.HasIssuesEnabled,
			HasProjects:              g.HasProjectsEnabled,
			AllowMergeCommit:         g.MergeCommitAllowed,
			AllowSquashMerge:         g.SquashMergeAllowed,
			AllowRebaseMerge:         g.RebaseMergeAllowed,
			AllowAutoMerge:           g.AutoMergeAllowed,
			AllowUpdateBranch:        g.AllowUpdateBranch,
			DeleteBranchOnMerge:      g.DeleteBranchOnMerge,
			WebCommitSignoffRequired: g.WebCommitSignoffRequired,
			SquashMergeCommitTitle:   g.SquashMergeCommitTitle,
			SquashMergeCommitMessage: g.SquashMergeCommitMessage,
			MergeCommitTitle:         g.MergeCommitTitle,
			MergeCommitMessage:       g.MergeCommitMessage,
			SecurityAndAnalysis:      securityAndAnalysis(rest.GetSecurityAndAnalysis()),
		}, nil
	})
}

// securityAndAnalysis flattens the REST block to feature → status. A nil
// block is a nil map: the caller could not see it.
func securityAndAnalysis(s *gh.SecurityAndAnalysis) map[string]string {
	if s == nil {
		return nil
	}

	out := make(map[string]string)

	set := func(feature string, status *string) {
		if status != nil {
			out[feature] = *status
		}
	}

	if s.AdvancedSecurity != nil {
		set("advanced_security", s.AdvancedSecurity.Status)
	}

	if s.SecretScanning != nil {
		set("secret_scanning", s.SecretScanning.Status)
	}

	if s.SecretScanningPushProtection != nil {
		set("secret_scanning_push_protection", s.SecretScanningPushProtection.Status)
	}

	if s.DependabotSecurityUpdates != nil {
		set("dependabot_security_updates", s.DependabotSecurityUpdates.Status)
	}

	if s.SecretScanningValidityChecks != nil {
		set("secret_scanning_validity_checks", s.SecretScanningValidityChecks.Status)
	}

	return out
}

// rulesetJSON is a ruleset in the API's shape, with conditions, rules
// and bypass actors kept raw so control needs no GitHub types.
type rulesetJSON struct {
	ID           int64           `json:"id"`
	Name         string          `json:"name"`
	Target       string          `json:"target"`
	SourceType   string          `json:"source_type"`
	Enforcement  string          `json:"enforcement"`
	Conditions   json.RawMessage `json:"conditions"`
	Rules        json.RawMessage `json:"rules"`
	BypassActors json.RawMessage `json:"bypass_actors"`
}

// ListRulesets returns every ruleset that applies to the repository,
// inherited org and enterprise ones included (includes_parents=true),
// following every page. The list omits rules, so each is then fetched
// by id. Source is source_type lower-cased; a ruleset without one has
// an empty Source and is never mistaken for a writable repository
// ruleset.
func (r *RepoReader) ListRulesets(ctx context.Context) ([]control.Ruleset, error) {
	rulesets, err := memo(r, "rulesets", func() ([]control.Ruleset, error) {
		ids, err := r.listRulesetIDs(ctx)
		if err != nil {
			return nil, err
		}

		out := make([]control.Ruleset, 0, len(ids))

		for _, id := range ids {
			var rs rulesetJSON
			if err := r.get(ctx, fmt.Sprintf("repos/%s/%s/rulesets/%d?includes_parents=true", r.owner, r.repo, id), &rs); err != nil {
				return nil, fmt.Errorf("reading ruleset %d of %s/%s: %w", id, r.owner, r.repo, err)
			}

			out = append(out, control.Ruleset{
				ID:           rs.ID,
				Name:         rs.Name,
				Source:       control.RulesetSource(strings.ToLower(rs.SourceType)),
				Enforcement:  rs.Enforcement,
				Target:       rs.Target,
				Conditions:   rs.Conditions,
				Rules:        rs.Rules,
				BypassActors: rs.BypassActors,
			})
		}

		return out, nil
	})

	return slices.Clone(rulesets), err
}

func (r *RepoReader) listRulesetIDs(ctx context.Context) ([]int64, error) {
	var ids []int64

	for page := 1; page != 0; {
		var batch []rulesetJSON

		u := fmt.Sprintf("repos/%s/%s/rulesets?includes_parents=true&per_page=%d&page=%d", r.owner, r.repo, rulesetsPerPage, page)

		resp, err := r.getResp(ctx, u, &batch)
		if err != nil {
			return nil, fmt.Errorf("listing rulesets of %s/%s: %w", r.owner, r.repo, err)
		}

		for i := range batch {
			ids = append(ids, batch[i].ID)
		}

		page = resp.NextPage
	}

	return ids, nil
}

// GetCustomProperties returns the repository's property values. A
// multi-select value is its options joined with commas.
func (r *RepoReader) GetCustomProperties(ctx context.Context) (map[string]*string, error) {
	return memo(r, "properties", func() (map[string]*string, error) {
		props, err := r.client.GetCustomPropertyValues(ctx, r.owner, r.repo)
		if err != nil {
			return nil, err
		}

		out := make(map[string]*string, len(props))
		for _, p := range props {
			out[p.PropertyName] = p.Value
		}

		return out, nil
	})
}

// OrgPropertySchema returns the property names the owner org defines.
func (r *RepoReader) OrgPropertySchema(ctx context.Context) ([]string, error) {
	names, err := memo(r, "schema", func() ([]string, error) {
		return r.client.GetOrgPropertySchema(ctx, r.owner)
	})

	return slices.Clone(names), err
}

// ListLabels returns every repository label.
func (r *RepoReader) ListLabels(ctx context.Context) ([]control.Label, error) {
	labels, err := memo(r, "labels", func() ([]control.Label, error) {
		got, err := r.client.ListLabels(ctx, r.owner, r.repo)
		if err != nil {
			return nil, err
		}

		out := make([]control.Label, 0, len(got))
		for _, l := range got {
			out = append(out, control.Label{Name: l.Name, Color: l.Color, Description: l.Description})
		}

		return out, nil
	})

	return slices.Clone(labels), err
}

// get issues a REST GET of u (relative to the API base) into v.
func (r *RepoReader) get(ctx context.Context, u string, v any) error {
	_, err := r.getResp(ctx, u, v)

	return err
}

func (r *RepoReader) getResp(ctx context.Context, u string, v any) (*gh.Response, error) {
	c := r.client.ghClient()

	req, err := c.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}

	return c.Do(ctx, req, v)
}

// isStatus reports whether resp carries status.
func isStatus(resp *gh.Response, status int) bool {
	return resp != nil && resp.StatusCode == status
}

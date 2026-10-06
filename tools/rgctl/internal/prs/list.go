package prs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/donaldgifford/repo-guardian/tools/rgctl/internal/ghapi"
)

// Lister finds repo-guardian pull requests. Search finds, the Pull Requests
// API decides (DESIGN-0035 D2): every hit is read back before it is listed.
type Lister struct {
	GH  *ghapi.Client
	ID  Identity
	Log *slog.Logger
	// Now stamps the record; nil means time.Now.
	Now func() time.Time
}

// tally accumulates one run's entries and counts.
type tally struct {
	entries []Entry
	sum     Summary
	seen    map[string]bool
}

// List runs sel. Repo wins over orgs; orgs are scanned by search, or by
// walking every repository when sel.Exhaustive. An org that fails is logged
// and skipped, and the joined errors are returned with the partial record.
func (l *Lister) List(ctx context.Context, sel *Selection) (*Record, error) {
	t := &tally{seen: make(map[string]bool)}
	var errs []error
	if sel.Repo != "" {
		if err := l.repo(ctx, t, sel.Repo); err != nil {
			errs = append(errs, err)
		}
	}
	for _, org := range sel.Orgs {
		if err := l.org(ctx, t, org, sel.Exhaustive); err != nil {
			l.Log.Error("org failed, continuing", "org", org, "err", err)
			errs = append(errs, fmt.Errorf("org %s: %w", org, err))
		}
	}
	now := time.Now
	if l.Now != nil {
		now = l.Now
	}
	slices.SortFunc(t.entries, func(a, b Entry) int {
		if c := strings.Compare(a.Repository, b.Repository); c != 0 {
			return c
		}
		return a.Number - b.Number
	})
	rec := &Record{
		SchemaVersion: SchemaVersion,
		GeneratedAt:   now().UTC().Truncate(time.Second),
		Selection:     *sel,
		BotLogin:      l.GH.BotLogin(),
		PRs:           t.entries,
		Summary:       t.sum,
	}
	if rec.PRs == nil {
		rec.PRs = []Entry{}
	}
	return rec, errors.Join(errs...)
}

func (l *Lister) repo(ctx context.Context, t *tally, repo string) error {
	open, err := l.GH.ListOpenPRs(ctx, repo)
	if err != nil {
		return fmt.Errorf("repo %s: %w", repo, err)
	}
	t.sum.Hits += len(open)
	t.sum.Verified += len(open)
	for i := range open {
		if err := l.consider(ctx, t, &open[i]); err != nil {
			return fmt.Errorf("repo %s: %w", repo, err)
		}
	}
	return nil
}

func (l *Lister) org(ctx context.Context, t *tally, org string, exhaustive bool) error {
	before := t.sum
	walk, unit := l.search, "pages"
	if exhaustive {
		walk, unit = l.walk, "repositories"
	}
	pages, err := walk(ctx, t, org)
	if err != nil {
		return err
	}
	l.Log.Info(fmt.Sprintf("org %s: %d %s, %d hits, %d verified, %d dropped", org, pages, unit,
		t.sum.Hits-before.Hits, t.sum.Verified-before.Verified, t.sum.Dropped-before.Dropped),
		"org", org, "exhaustive", exhaustive)
	return nil
}

// walk lists every repository in org and reads its open pull requests.
func (l *Lister) walk(ctx context.Context, t *tally, org string) (int, error) {
	repos, err := l.GH.ListRepos(ctx, org)
	if err != nil {
		return 0, err
	}
	for _, r := range repos {
		if err := l.repo(ctx, t, r); err != nil {
			return 0, err
		}
	}
	return len(repos), nil
}

// search runs one search per head qualifier, then reads every hit back.
func (l *Lister) search(ctx context.Context, t *tally, org string) (pages int, err error) {
	for _, head := range l.ID.SearchHeads() {
		hits, status, err := l.GH.SearchOpenPRs(ctx, org, head)
		if err != nil {
			return pages, err
		}
		pages += status.Pages
		if status.Incomplete || status.Capped {
			t.sum.Incomplete++
			l.Log.Warn("search results are incomplete; run again with --exhaustive for the authoritative list",
				"org", org, "incomplete_results", status.Incomplete, "capped_at_1000", status.Capped, "total", status.Total)
		}
		t.sum.Hits += len(hits)
		for _, h := range hits {
			pr, err := l.GH.GetPR(ctx, h.Repo, h.Number)
			if err != nil {
				return pages, err
			}
			t.sum.Verified++
			if err := l.consider(ctx, t, pr); err != nil {
				return pages, err
			}
		}
	}
	return pages, nil
}

// consider applies the identity rule to a verified pull request and, when
// it is repo-guardian's, classifies and records it.
func (l *Lister) consider(ctx context.Context, t *tally, pr *ghapi.PullRequest) error {
	key := fmt.Sprintf("%s#%d", strings.ToLower(pr.Repo), pr.Number)
	if t.seen[key] {
		return nil
	}
	t.seen[key] = true
	if ok, reason := l.ID.Match(pr); !ok {
		t.sum.Dropped++
		l.Log.Debug("not repo-guardian's", "pr", key, "reason", reason)
		return nil
	}
	e, err := Classify(ctx, l.GH, &l.ID, pr)
	if err != nil {
		return err
	}
	if e.Edited {
		t.sum.Edited++
	} else {
		t.sum.Clean++
	}
	t.entries = append(t.entries, e)
	return nil
}

// Classify reads a repo-guardian pull request's commits and comments: edited
// when any commit is by someone other than the bot (reading pages only until
// the first such commit), reconcile_log when a comment starts with v1's
// sticky-comment marker.
func Classify(ctx context.Context, gh *ghapi.Client, id *Identity, pr *ghapi.PullRequest) (Entry, error) {
	e := Entry{
		Repository: pr.Repo,
		Number:     pr.Number,
		URL:        pr.URL,
		HeadBranch: pr.HeadRef,
		HeadSHA:    pr.HeadSHA,
		Title:      pr.Title,
		CreatedAt:  pr.CreatedAt,
		UpdatedAt:  pr.UpdatedAt,
	}
	err := gh.ListPRCommits(ctx, pr.Repo, pr.Number, func(c ghapi.Commit) bool {
		foreign := id.foreignLogins(c)
		if len(foreign) == 0 {
			return true
		}
		e.Edited = true
		e.EditedBy = foreign
		return false
	})
	if err != nil {
		return e, err
	}
	comments, err := gh.ListComments(ctx, pr.Repo, pr.Number)
	if err != nil {
		return e, err
	}
	e.ReconcileLog = slices.ContainsFunc(comments, func(c ghapi.Comment) bool {
		return firstLine(c.Body) == ReconcileLogMarker
	})
	return e, nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return strings.TrimSpace(line)
}

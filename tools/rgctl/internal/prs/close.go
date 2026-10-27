package prs

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/donaldgifford/repo-guardian/tools/rgctl/internal/ghapi"
)

// ClosedMarker is the first line of every comment rgctl posts, so a re-run
// never posts twice.
const ClosedMarker = "<!-- rgctl:closed-by-migration:v1 -->"

// DefaultComment is the pointer comment (DESIGN-0035 D14), without the
// marker line.
func DefaultComment(now time.Time) string {
	return fmt.Sprintf("Closed by `rgctl` on %s while moving this repository to repo-guardian v2.\n\n"+
		"repo-guardian v2 manages this repository from now on and opens its own pull request per control, "+
		"so this pull request from repo-guardian v1 is no longer needed.", now.UTC().Format(time.DateOnly))
}

// Closer closes recorded pull requests. Nothing is written unless Yes; every
// write is preceded by a fresh read (DESIGN-0035 D3).
type Closer struct {
	GH  *ghapi.Client
	ID  Identity
	Log *slog.Logger
	// Yes acts; without it the plan is logged and nothing is written.
	Yes bool
	// Force also closes pull requests with commits by someone other than
	// the bot. Their branch is never deleted.
	Force bool
	// DeleteBranch deletes the head branch after closing.
	DeleteBranch bool
	// Comment replaces DefaultComment's text; the marker line stays.
	Comment string
	// Now dates the default comment; nil means time.Now.
	Now func() time.Time
}

// Close processes every entry of rec in order and returns a copy of rec with
// each entry's Result (and Error) set. A failure on one pull request is
// recorded and the run continues.
func (c *Closer) Close(ctx context.Context, rec *Record) *Record {
	out := *rec
	out.PRs = slices.Clone(rec.PRs)
	for i := range out.PRs {
		e := &out.PRs[i]
		e.Result, e.Error = "", ""
		if err := c.one(ctx, e); err != nil {
			e.Result, e.Error = ResultError, err.Error()
			c.Log.Error("failed", "pr", ref(e), "err", err)
		}
	}
	return &out
}

func (c *Closer) one(ctx context.Context, e *Entry) error {
	pr, err := c.GH.GetPR(ctx, e.Repository, e.Number)
	if err != nil {
		return err
	}
	if pr.State != stateOpen {
		return c.alreadyClosed(ctx, e, pr)
	}
	if ok, reason := c.ID.Match(pr); !ok {
		e.Result = ResultSkippedNotOurs
		c.Log.Warn("not repo-guardian's, skipped", "pr", ref(e), "reason", reason)
		return nil
	}
	if e.HeadSHA != "" && pr.HeadSHA != e.HeadSHA {
		c.Log.Warn("head moved since the record was written", "pr", ref(e), "recorded", e.HeadSHA, "now", pr.HeadSHA)
	}
	fresh, err := Classify(ctx, c.GH, &c.ID, pr)
	if err != nil {
		return err
	}
	e.HeadSHA, e.Edited, e.EditedBy, e.ReconcileLog = fresh.HeadSHA, fresh.Edited, fresh.EditedBy, fresh.ReconcileLog
	if e.Edited && !c.Force {
		e.Result = ResultSkippedEdited
		c.Log.Warn(
			"has commits by someone other than the App, skipped (use --force to close it)",
			"pr",
			ref(e),
			"edited_by",
			strings.Join(e.EditedBy, ","),
		)
		return nil
	}
	deleteBranch := c.DeleteBranch && !e.Edited
	if !c.Yes {
		e.Result = ResultPlanned
		c.Log.Info("would comment and close", "pr", ref(e), "delete_branch", deleteBranch, "url", e.URL)
		return nil
	}
	return c.act(ctx, e, deleteBranch)
}

// act comments, closes, then deletes the branch: a failure part-way leaves a
// pull request that still explains itself, and each step is idempotent.
func (c *Closer) act(ctx context.Context, e *Entry, deleteBranch bool) error {
	comments, err := c.GH.ListComments(ctx, e.Repository, e.Number)
	if err != nil {
		return fmt.Errorf("list comments: %w", err)
	}
	if slices.ContainsFunc(comments, isClosedMarker) {
		c.Log.Info("pointer comment already posted", "pr", ref(e))
	} else {
		if err := c.GH.CreateComment(ctx, e.Repository, e.Number, c.commentBody()); err != nil {
			return err
		}
		c.Log.Info("commented", "pr", ref(e))
	}
	e.Result = ResultCommented

	if err := c.GH.ClosePR(ctx, e.Repository, e.Number); err != nil {
		return err
	}
	e.Result = ResultClosed
	c.Log.Info("closed", "pr", ref(e), "url", e.URL)

	if !deleteBranch {
		return nil
	}
	if err := c.GH.DeleteBranch(ctx, e.Repository, e.HeadBranch); err != nil {
		return err
	}
	e.Result = ResultBranchDeleted
	c.Log.Info("branch deleted", "pr", ref(e), "branch", e.HeadBranch)
	return nil
}

// alreadyClosed handles a pull request that is closed when re-read. When
// rgctl itself closed it (its marker comment is there) and a previous run
// stopped before deleting the branch, --delete-branch finishes the job.
// A pull request someone else closed is left exactly as it is.
func (c *Closer) alreadyClosed(ctx context.Context, e *Entry, pr *ghapi.PullRequest) error {
	e.Result = ResultAlreadyClosed
	if !c.Yes || !c.DeleteBranch {
		c.Log.Info("already closed", "pr", ref(e))
		return nil
	}
	comments, err := c.GH.ListComments(ctx, e.Repository, e.Number)
	if err != nil {
		return fmt.Errorf("list comments: %w", err)
	}
	if !slices.ContainsFunc(comments, isClosedMarker) {
		c.Log.Info("already closed, not by rgctl; branch left alone", "pr", ref(e))
		return nil
	}
	open := *pr
	open.State = stateOpen // the identity rule, minus the state rgctl itself changed
	if ok, _ := c.ID.Match(&open); !ok {
		return nil
	}
	fresh, err := Classify(ctx, c.GH, &c.ID, pr)
	if err != nil {
		return err
	}
	if fresh.Edited {
		c.Log.Info("already closed; branch has commits by someone other than the App, left alone", "pr", ref(e))
		return nil
	}
	exists, err := c.GH.BranchExists(ctx, e.Repository, pr.HeadRef)
	if err != nil || !exists {
		return err
	}
	if err := c.GH.DeleteBranch(ctx, e.Repository, pr.HeadRef); err != nil {
		return err
	}
	e.Result = ResultBranchDeleted
	c.Log.Info("already closed by rgctl; branch deleted", "pr", ref(e), "branch", pr.HeadRef)
	return nil
}

func isClosedMarker(cm ghapi.Comment) bool { return firstLine(cm.Body) == ClosedMarker }

func (c *Closer) commentBody() string {
	text := c.Comment
	if text == "" {
		now := time.Now
		if c.Now != nil {
			now = c.Now
		}
		text = DefaultComment(now())
	}
	return ClosedMarker + "\n" + text
}

func ref(e *Entry) string { return fmt.Sprintf("%s#%d", e.Repository, e.Number) }

// Results counts the results of a close run.
func Results(rec *Record) map[string]int {
	counts := make(map[string]int)
	for i := range rec.PRs {
		counts[rec.PRs[i].Result]++
	}
	return counts
}

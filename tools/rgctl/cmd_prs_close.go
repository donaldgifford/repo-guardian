package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/donaldgifford/repo-guardian/tools/rgctl/internal/ghapi"
	"github.com/donaldgifford/repo-guardian/tools/rgctl/internal/prs"
)

type closeFlags struct {
	from         string
	yes          bool
	force        bool
	deleteBranch bool
	comment      string
	format       string
	out          string
}

func (a *app) prsCloseCommand() *cobra.Command {
	var (
		sel selectFlags
		f   closeFlags
	)
	cmd := &cobra.Command{
		Use:   cmdClose,
		Short: "Close repo-guardian pull requests with a pointer comment (dry run unless --yes)",
		Long: "close re-reads every pull request, then comments, closes and (with --delete-branch) deletes its branch.\n" +
			"Input is a record from prs list (--from) or the same selection flags as list. Without --yes nothing is\n" +
			"written and the plan is printed. PRs with commits by anyone but the App are skipped unless --force,\n" +
			"and their branch is never deleted.\n" +
			"Exit 0: done or nothing to do. Exit 1: PRs skipped as edited. Exit 2: usage. Exit 3: a PR failed.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runClose(cmd.Context(), &sel, &f)
		},
	}
	addSelectFlags(cmd, &sel)
	fl := cmd.Flags()
	fl.StringVar(&f.from, "from", "", "record written by prs list --format json")
	fl.BoolVar(&f.yes, "yes", false, "act; without it the plan is printed and nothing is written")
	fl.BoolVar(&f.force, "force", false, "also close PRs with commits by someone other than the App (their branch is never deleted)")
	fl.BoolVar(&f.deleteBranch, "delete-branch", false, "delete the head branch after closing")
	fl.StringVar(&f.comment, "comment", "", "comment text replacing the default pointer comment")
	fl.StringVar(&f.format, "format", formatTable, "output: table or json (the result record)")
	fl.StringVar(&f.out, "out", "", "also write the result record to this file")
	return cmd
}

func (a *app) runClose(ctx context.Context, sel *selectFlags, f *closeFlags) error {
	if err := a.checkClose(sel, f); err != nil {
		return err
	}
	var rec *prs.Record
	if f.from != "" {
		var err error
		if rec, err = prs.Load(f.from); err != nil {
			return usageError(err)
		}
	}
	gh, err := a.github(ctx)
	if err != nil {
		return err
	}
	id := identity(gh, sel)
	if rec == nil {
		if rec, err = a.listForClose(ctx, gh, &id, sel); err != nil {
			return err
		}
	}
	if !f.yes {
		a.logger().Info("dry run: nothing will be written; pass --yes to act")
	}
	closer := &prs.Closer{
		GH: gh, ID: id, Log: a.logger(), Yes: f.yes, Force: f.force,
		DeleteBranch: f.deleteBranch, Comment: f.comment, Now: a.now,
	}
	result := closer.Close(ctx, rec)
	if err := a.emitRecord(result, f.format, f.out, writeCloseTable); err != nil {
		return opError(err)
	}
	return a.closeExit(prs.Results(result))
}

// checkClose validates the flags before anything touches GitHub, and moves
// the log to stderr when the result record owns stdout.
func (a *app) checkClose(sel *selectFlags, f *closeFlags) error {
	if err := checkFormat(f.format); err != nil {
		return err
	}
	hasSelection := sel.repo != "" || sel.org != "" || sel.config != ""
	if (f.from == "") == !hasSelection {
		return usageError(errors.New("give either --from <record.json> or one of --repo, --org or --config"))
	}
	if f.format == formatJSON && f.out == "" {
		return a.setupLogger(a.stderr)
	}
	return nil
}

// closeExit logs the summary and maps the results to the exit code.
func (a *app) closeExit(counts map[string]int) error {
	a.logger().Info("summary", "planned", counts[prs.ResultPlanned], "closed", counts[prs.ResultClosed]+counts[prs.ResultBranchDeleted],
		"branch_deleted", counts[prs.ResultBranchDeleted], "already_closed", counts[prs.ResultAlreadyClosed],
		"skipped_edited", counts[prs.ResultSkippedEdited], "skipped_not_ours", counts[prs.ResultSkippedNotOurs], "error", counts[prs.ResultError])
	switch {
	case counts[prs.ResultError] > 0:
		return &exitError{code: exitOperational}
	case counts[prs.ResultSkippedEdited] > 0:
		return &exitError{code: exitFound}
	}
	return nil
}

// listForClose runs the same listing as prs list for the selection flags.
func (a *app) listForClose(ctx context.Context, gh *ghapi.Client, id *prs.Identity, sel *selectFlags) (*prs.Record, error) {
	s, err := selection(gh, sel)
	if err != nil {
		return nil, err
	}
	lister := &prs.Lister{GH: gh, ID: *id, Log: a.logger(), Now: a.now}
	rec, err := lister.List(ctx, &s)
	if err != nil {
		return nil, opError(err)
	}
	return rec, nil
}

func writeCloseTable(w io.Writer, rec *prs.Record, _ time.Time) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "REPOSITORY\tPR\tBRANCH\tRESULT\tURL") //nolint:errcheck // reported by Flush
	for i := range rec.PRs {
		e := &rec.PRs[i]
		result := e.Result
		if e.Error != "" {
			result += ": " + e.Error
		}
		_, _ = fmt.Fprintf(tw, "%s\t#%d\t%s\t%s\t%s\n", e.Repository, e.Number, e.HeadBranch, result, e.URL) //nolint:errcheck // reported by Flush
	}
	return tw.Flush()
}

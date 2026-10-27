package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/donaldgifford/repo-guardian/tools/rgctl/internal/ghapi"
	"github.com/donaldgifford/repo-guardian/tools/rgctl/internal/prs"
	"github.com/donaldgifford/repo-guardian/tools/rgctl/internal/v1config"
)

// Environment variables the prs commands read. GH_TOKEN is deliberately not
// a flag: a token on a command line lands in shell history.
// Command names.
const (
	cmdPRs   = "prs"
	cmdList  = "list"
	cmdClose = "close"
)

const (
	envAppID      = "RGCTL_APP_ID"
	envKeyFile    = "RGCTL_PRIVATE_KEY_FILE"
	envBotLogin   = "RGCTL_BOT_LOGIN"
	envGitHubHost = "RGCTL_GITHUB_HOST"
	envToken      = "GH_TOKEN"
)

// authFlags are the credential flags shared by list and close.
type authFlags struct {
	appID      string
	keyFile    string
	botLogin   string
	githubHost string
}

// selectFlags are the selection flags shared by list and close.
type selectFlags struct {
	repo       string
	org        string
	config     string
	branches   []string
	exhaustive bool
}

func (a *app) prsCommand() *cobra.Command {
	cmd := &cobra.Command{Use: cmdPRs, Short: "Find or close repo-guardian pull requests"}
	f := cmd.PersistentFlags()
	f.StringVar(&a.auth.appID, "app-id", "", "GitHub App id (env "+envAppID+")")
	f.StringVar(&a.auth.keyFile, "private-key-file", "", "path to the App's private key PEM (env "+envKeyFile+")")
	f.StringVar(&a.auth.botLogin, "bot-login", "", "the App's bot login, <slug>[bot]; required with "+envToken+" (env "+envBotLogin+")")
	f.StringVar(&a.auth.githubHost, "github-host", "", "GitHub Enterprise Server host or URL; default github.com (env "+envGitHubHost+")")
	cmd.AddCommand(a.prsListCommand(), a.prsCloseCommand())
	return cmd
}

func addSelectFlags(cmd *cobra.Command, s *selectFlags) {
	f := cmd.Flags()
	f.StringVar(&s.repo, "repo", "", "one repository, org/name")
	f.StringVar(&s.org, "org", "", "one org, searched then verified")
	f.StringVar(&s.config, "config", "", "every org in a v1 guardian.hcl's scope (every installation in legacy mode)")
	f.StringSliceVar(&s.branches, "branch", nil, "exact head branch to match, repeatable; default is the "+prs.DefaultPrefix+" prefix")
	f.BoolVar(&s.exhaustive, "exhaustive", false, "list every repository's open PRs instead of searching")
}

// credentials merges flags over the environment.
func (a *app) credentials() (*ghapi.Credentials, error) {
	pick := func(flag, env string) string {
		if flag != "" {
			return flag
		}
		return a.getenv(env)
	}
	creds := &ghapi.Credentials{
		PrivateKeyFile: pick(a.auth.keyFile, envKeyFile),
		Token:          a.getenv(envToken),
		BotLogin:       pick(a.auth.botLogin, envBotLogin),
		Host:           pick(a.auth.githubHost, envGitHubHost),
	}
	if id := pick(a.auth.appID, envAppID); id != "" {
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil || n <= 0 {
			return nil, usageError(fmt.Errorf("app id %q is not a positive integer", id))
		}
		creds.AppID = n
	}
	return creds, nil
}

// github builds the client; credential mistakes are usage errors, anything
// GitHub says is operational.
func (a *app) github(ctx context.Context) (*ghapi.Client, error) {
	creds, err := a.credentials()
	if err != nil {
		return nil, err
	}
	gh, err := ghapi.New(ctx, creds, ghapi.Options{
		Logger:    a.logger(),
		UserAgent: "rgctl/" + moduleVersion(),
		Sleep:     a.sleep,
	})
	switch {
	case err == nil:
		return gh, nil
	case errors.Is(err, ghapi.ErrNoCredentials), errors.Is(err, ghapi.ErrAppIDWithoutKey),
		errors.Is(err, ghapi.ErrKeyWithoutAppID), errors.Is(err, ghapi.ErrTokenNeedsLogin):
		return nil, usageError(err)
	default:
		return nil, opError(err)
	}
}

// selection resolves the selection flags into the orgs to scan.
func selection(gh *ghapi.Client, s *selectFlags) (prs.Selection, error) {
	sel := prs.Selection{Repo: s.repo, Org: s.org, Config: s.config, Branches: s.branches, Exhaustive: s.exhaustive}
	set := 0
	for _, v := range []string{s.repo, s.org, s.config} {
		if v != "" {
			set++
		}
	}
	if set != 1 {
		return sel, usageError(errors.New("give exactly one of --repo, --org or --config"))
	}
	switch {
	case s.org != "":
		sel.Orgs = []string{s.org}
	case s.config != "":
		orgs, legacy, err := v1config.Orgs(s.config)
		if err != nil {
			return sel, usageError(err)
		}
		if !legacy {
			sel.Orgs = orgs
			break
		}
		if gh.Mode() != ghapi.ModeApp {
			return sel, usageError(fmt.Errorf("%s has no scope block: legacy mode means every installed org, "+
				"which needs App credentials (--app-id and --private-key-file) or an explicit --org", s.config))
		}
		ins, err := gh.Installations()
		if err != nil {
			return sel, opError(err)
		}
		for _, in := range ins {
			sel.Orgs = append(sel.Orgs, in.Account)
		}
	}
	return sel, nil
}

func identity(gh *ghapi.Client, s *selectFlags) prs.Identity {
	return prs.Identity{BotLogin: gh.BotLogin(), Prefix: prs.DefaultPrefix, Branches: s.branches}
}

func (a *app) prsListCommand() *cobra.Command {
	var (
		sel    selectFlags
		format string
		out    string
	)
	cmd := &cobra.Command{
		Use:   cmdList,
		Short: "Find open repo-guardian pull requests",
		Long: "list finds open pull requests authored by the App on " + prs.DefaultPrefix + " branches. Org scans search, then\n" +
			"read every hit back through the Pull Requests API; --exhaustive walks every repository instead.\n" +
			"Exit 0: none found. Exit 1: found. Exit 2: usage. Exit 3: an org or repository failed.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := checkFormat(format); err != nil {
				return err
			}
			if format == formatJSON && out == "" {
				// The record owns stdout; the log moves aside (D15).
				if err := a.setupLogger(a.stderr); err != nil {
					return err
				}
			}
			gh, err := a.github(cmd.Context())
			if err != nil {
				return err
			}
			s, err := selection(gh, &sel)
			if err != nil {
				return err
			}
			lister := &prs.Lister{GH: gh, ID: identity(gh, &sel), Log: a.logger(), Now: a.now}
			rec, listErr := lister.List(cmd.Context(), &s)
			if err := a.emitRecord(rec, format, out, writeListTable); err != nil {
				return opError(err)
			}
			logSummary(a, rec)
			switch {
			case listErr != nil:
				return opError(listErr)
			case len(rec.PRs) > 0:
				return &exitError{code: exitFound}
			}
			return nil
		},
	}
	addSelectFlags(cmd, &sel)
	cmd.Flags().StringVar(&format, "format", formatTable, "output: table or json")
	cmd.Flags().StringVar(&out, "out", "", "also write the JSON record to this file")
	return cmd
}

func checkFormat(format string) error {
	if format != formatTable && format != formatJSON {
		return usageError(fmt.Errorf("--format must be %s or %s, got %q", formatTable, formatJSON, format))
	}
	return nil
}

// emitRecord writes the table or the JSON record to stdout, and the record
// to out when given.
func (a *app) emitRecord(rec *prs.Record, format, out string, table func(io.Writer, *prs.Record, time.Time) error) error {
	if out != "" {
		if err := rec.Save(out); err != nil {
			return err
		}
		a.logger().Info("record written", "path", out)
	}
	if format == formatJSON {
		if out != "" {
			return nil
		}
		return rec.Encode(a.out)
	}
	return table(a.out, rec, a.now())
}

func logSummary(a *app, rec *prs.Record) {
	s := rec.Summary
	a.logger().Info("summary", "found", len(rec.PRs), "hits", s.Hits, "verified", s.Verified,
		"dropped", s.Dropped, "clean", s.Clean, "edited", s.Edited, "incomplete_orgs", s.Incomplete)
}

func writeListTable(w io.Writer, rec *prs.Record, now time.Time) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "REPOSITORY\tPR\tBRANCH\tAGE\tEDITED\tURL") //nolint:errcheck // reported by Flush
	for i := range rec.PRs {
		e := &rec.PRs[i]
		edited := "no"
		if e.Edited {
			edited = "yes (" + strings.Join(e.EditedBy, ", ") + ")"
		}
		_, _ = fmt.Fprintf(tw, "%s\t#%d\t%s\t%s\t%s\t%s\n", //nolint:errcheck // reported by Flush
			e.Repository, e.Number, e.HeadBranch, age(now, e.CreatedAt), edited, e.URL)
	}
	return tw.Flush()
}

// age renders how long ago t was in whole days, or hours under a day.
func age(now, t time.Time) string {
	d := now.Sub(t)
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

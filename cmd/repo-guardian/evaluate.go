package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/donaldgifford/repo-guardian/internal/activities"
	"github.com/donaldgifford/repo-guardian/internal/config"
	"github.com/donaldgifford/repo-guardian/internal/control"
	"github.com/donaldgifford/repo-guardian/internal/controls/builtin"
	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/policy"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

const cmdEvaluate = "evaluate"

// evaluate's exit codes (IMPL-0029 task 5.9).
const (
	exitCompliant    = 0
	exitNonCompliant = 1
	exitUsage        = 2
	exitOperational  = 3
)

// evaluate's output formats.
const (
	evalFormatTable = "table"
	evalFormatJSON  = "json"
)

// evaluateSchemaVersion versions evaluate's JSON output. Bump it on any
// change a consumer could notice.
const evaluateSchemaVersion = 1

// exitError carries a process exit code out of dispatch. A nil err exits
// quietly: a non-compliant repository is an answer, not a failure.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string {
	if e.err == nil {
		return fmt.Sprintf("exit %d", e.code)
	}

	return e.err.Error()
}

func (e *exitError) Unwrap() error { return e.err }

// errNoAccess is the Evaluation App not reaching the repository.
var errNoAccess = errors.New("the Evaluation App cannot reach the repository")

// evalTarget is one repository opened as the Evaluation App sees it.
type evalTarget struct {
	repo        activities.EvalRepo
	accountType string
	archived    bool
	fork        bool
}

// evalOpener opens org/name as the Evaluation App; evaluate's GitHub
// seam.
type evalOpener func(ctx context.Context, org, name string) (*evalTarget, error)

// evaluateCmd is one evaluate run.
type evaluateCmd struct {
	stdout io.Writer
	// open is nil until the credentials are read, so a usage error never
	// needs them.
	open func() (evalOpener, error)
}

// runEvaluate implements `repo-guardian evaluate --repo <org>/<name>`:
// load the policy root, open the repository as the Evaluation App,
// resolve and evaluate it with no store, and print the result.
func runEvaluate(args []string) error {
	cmd := &evaluateCmd{stdout: os.Stdout, open: githubOpener}

	return cmd.run(context.Background(), args)
}

func (c *evaluateCmd) run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet(cmdEvaluate, flag.ContinueOnError)
	repoFlag := fs.String("repo", "", "repository to evaluate, as <org>/<name>")
	format := fs.String("format", evalFormatTable, "output format: table or json")
	root := fs.String("policy", os.Getenv("GUARDIAN_CONFIG"), "controls policy root (defaults to $GUARDIAN_CONFIG)")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}

		return &exitError{code: exitUsage, err: fmt.Errorf("evaluate: %w", err)}
	}

	org, name, ok := strings.Cut(*repoFlag, "/")
	if !ok || org == "" || name == "" || strings.Contains(name, "/") {
		return &exitError{code: exitUsage, err: errors.New("evaluate: --repo must be <org>/<name>")}
	}

	if *format != evalFormatTable && *format != evalFormatJSON {
		return &exitError{code: exitUsage, err: fmt.Errorf("evaluate: unknown --format %q, want table or json", *format)}
	}

	if *root == "" {
		return &exitError{code: exitUsage, err: errors.New("evaluate: no policy root; pass --policy or set GUARDIAN_CONFIG")}
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	snap, err := policy.Load(*root, builtin.Registry(), logger)
	if err != nil {
		return &exitError{code: exitUsage, err: fmt.Errorf("evaluate: load policy %s: %w", *root, err)}
	}

	open, err := c.open()
	if err != nil {
		return &exitError{code: exitUsage, err: fmt.Errorf("evaluate: %w", err)}
	}

	t, err := open(ctx, org, name)
	if err != nil {
		return &exitError{code: exitOperational, err: fmt.Errorf("evaluate %s/%s: %w", org, name, err)}
	}

	res := snap.Resolve(policy.Repository{Org: org, Name: name, Active: true, Archived: t.archived, Fork: t.fork}, policy.AppAccess{Evaluable: true})

	ev, err := activities.NewEvaluator(nil, snap, nil, logger).EvaluateResolved(ctx, org, name, t.accountType, &res, t.repo)
	if err != nil {
		return &exitError{code: exitOperational, err: fmt.Errorf("evaluate %s/%s: %w", org, name, err)}
	}

	out := newEvaluateOutput(org, name, snap.Version(), &res, ev)

	if err := out.write(c.stdout, *format); err != nil {
		return &exitError{code: exitOperational, err: err}
	}

	return out.exit()
}

// evaluateOutput is evaluate's result, rendered as a table or as JSON.
type evaluateOutput struct {
	SchemaVersion int             `json:"schema_version"`
	Repository    string          `json:"repository"`
	PolicyVersion string          `json:"policy_version"`
	State         policy.State    `json:"state"`
	Reason        string          `json:"reason,omitempty"`
	Result        string          `json:"result"`
	Cause         string          `json:"cause,omitempty"`
	Branch        string          `json:"branch,omitempty"`
	Commit        string          `json:"commit,omitempty"`
	Controls      []controlOutput `json:"controls"`
}

type controlOutput struct {
	Control string       `json:"control"`
	Status  string       `json:"status"`
	Rules   []ruleOutput `json:"rules"`
}

type ruleOutput struct {
	Rule     string          `json:"rule"`
	Status   string          `json:"status"`
	Evidence json.RawMessage `json:"evidence"`
}

func newEvaluateOutput(org, name, version string, res *policy.Resolution, ev *activities.RepositoryEvaluation) *evaluateOutput {
	out := &evaluateOutput{
		SchemaVersion: evaluateSchemaVersion, Repository: org + "/" + name, PolicyVersion: version,
		State: res.State, Reason: res.Reason, Result: string(ev.Kind), Cause: ev.Cause, Branch: ev.Branch, Commit: ev.SHA,
		Controls: make([]controlOutput, 0, len(ev.Controls)),
	}

	for i := range ev.Controls {
		ce := &ev.Controls[i]
		id := control.ID{Slug: ce.ControlID, Version: ce.ControlVersion}
		co := controlOutput{Control: id.String(), Status: ce.Status, Rules: make([]ruleOutput, 0, len(ce.Rules))}

		for _, r := range ce.Rules {
			co.Rules = append(co.Rules, ruleOutput{Rule: r.RuleID, Status: r.Status, Evidence: r.Evidence})
		}

		out.Controls = append(out.Controls, co)
	}

	return out
}

func (o *evaluateOutput) write(w io.Writer, format string) error {
	if format == evalFormatJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")

		if err := enc.Encode(o); err != nil {
			return fmt.Errorf("write json: %w", err)
		}

		return nil
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "repository\t%s\nstate\t%s\nresult\t%s\n", o.Repository, o.State, o.Result) //nolint:errcheck // Flush reports it

	if o.Commit != "" {
		fmt.Fprintf(tw, "commit\t%s@%s\n", o.Branch, o.Commit) //nolint:errcheck // Flush reports it
	}

	if len(o.Controls) > 0 {
		fmt.Fprint(tw, "\nCONTROL\tSTATUS\tRULE\tRULE STATUS\n") //nolint:errcheck // Flush reports it
	}

	for _, c := range o.Controls {
		for i, r := range c.Rules {
			id, status := c.Control, c.Status
			if i > 0 {
				id, status = "", ""
			}

			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", id, status, r.Rule, r.Status) //nolint:errcheck // Flush reports it
		}
	}

	if err := tw.Flush(); err != nil {
		return fmt.Errorf("write table: %w", err)
	}

	return nil
}

// exit maps the result to evaluate's exit code: 0 compliant or nothing
// measured, 1 non-compliant, 3 when the repository could not be measured.
func (o *evaluateOutput) exit() error {
	switch workflows.CheckKind(o.Result) {
	case workflows.CheckParked:
		return &exitError{code: exitOperational, err: fmt.Errorf("evaluate %s: %w: %s", o.Repository, errNoAccess, o.Cause)}
	case workflows.CheckDeferred:
		return &exitError{code: exitOperational, err: fmt.Errorf("evaluate %s: rate limited; try again after the reset", o.Repository)}
	}

	for _, c := range o.Controls {
		if c.Status == string(control.StatusNonCompliant) {
			return &exitError{code: exitNonCompliant}
		}
	}

	return nil
}

// githubOpener reads the Evaluation App's credentials (EVAL_GITHUB_APP_ID,
// EVAL_GITHUB_PRIVATE_KEY_PATH: a path, never a key) and returns an
// opener that finds the org's installation.
func githubOpener() (evalOpener, error) {
	creds, err := config.LoadAppCredentials(config.AppEval)
	if err != nil {
		return nil, err //nolint:wrapcheck // names the variable
	}

	if creds.AppID == 0 || creds.PrivateKeyPath == "" {
		return nil, errors.New("set EVAL_GITHUB_APP_ID and EVAL_GITHUB_PRIVATE_KEY_PATH to the Evaluation App's id and key file")
	}

	app, err := ghclient.NewClient(creds.AppID, creds.PrivateKeyPath, slog.New(slog.DiscardHandler), 0)
	if err != nil {
		return nil, fmt.Errorf("evaluation app client: %w", err)
	}

	return func(ctx context.Context, org, name string) (*evalTarget, error) {
		return openWithApp(ctx, app, org, name)
	}, nil
}

func openWithApp(ctx context.Context, app *ghclient.GitHubClient, org, name string) (*evalTarget, error) {
	installs, err := app.ListInstallations(ctx)
	if err != nil {
		return nil, fmt.Errorf("list installations: %w", err)
	}

	for _, in := range installs {
		if !strings.EqualFold(in.Account, org) {
			continue
		}

		c, err := app.CreateInstallationClient(ctx, in.ID)
		if err != nil {
			return nil, fmt.Errorf("installation %d: %w", in.ID, err)
		}

		r, err := c.GetRepository(ctx, org, name)
		if ghclient.IsAccessDenied(err) {
			return nil, fmt.Errorf("%w: %w", errNoAccess, err)
		}

		if err != nil {
			return nil, err //nolint:wrapcheck // GetRepository names the repository
		}

		repo, err := activities.NewGitHubEvalClients(app).Repo(ctx, in.ID, org, name)
		if err != nil {
			return nil, err //nolint:wrapcheck // Repo names the installation
		}

		t := &evalTarget{repo: repo, accountType: control.AccountOrganization, archived: r.Archived, fork: r.Fork}
		if in.Personal {
			t.accountType = control.AccountUser
		}

		return t, nil
	}

	return nil, fmt.Errorf("%w: no installation on %s", errNoAccess, org)
}

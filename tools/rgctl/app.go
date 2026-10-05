package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/donaldgifford/repo-guardian/tools/rgctl/internal/clog"
)

// app is the state shared by every command: where output and the log go, and
// the logger built from the persistent flags.
type app struct {
	// out receives tables and JSON records.
	out io.Writer
	// stderr is where the log moves while a JSON record is written to out.
	stderr io.Writer

	logOpts clog.Options
	log     *slog.Logger

	auth authFlags

	// getenv, now and sleep are os.Getenv, time.Now and nil (a real sleep)
	// outside tests.
	getenv func(string) string
	now    func() time.Time
	sleep  func(context.Context, time.Duration) error
}

func newApp(stdout, stderr io.Writer) *app {
	return &app{out: stdout, stderr: stderr, getenv: os.Getenv, now: time.Now}
}

// logger returns the configured logger. Before the persistent flags are
// parsed (a cobra usage error, a bad argument count) there is none yet, so it
// falls back to a default logger on stderr.
func (a *app) logger() *slog.Logger {
	if a.log == nil {
		a.log, _ = clog.New(a.stderr, clog.Options{}) //nolint:errcheck // empty options cannot fail
	}
	return a.log
}

// setupLogger builds the logger on w from the persistent flags. The log goes
// to stdout beside the table, except while a JSON record is written to stdout
// (DESIGN-0035 D15), when commands call it again with stderr.
func (a *app) setupLogger(w io.Writer) error {
	lg, err := clog.New(w, a.logOpts)
	if err != nil {
		return usageError(err)
	}
	a.log = lg
	return nil
}

func (a *app) rootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "rgctl",
		Short: "Inspect a repo-guardian v1 policy and find or close repo-guardian pull requests",
		Long: "rgctl reads a repo-guardian v1 guardian.hcl without the v1 loader, finds the open pull requests\n" +
			"repo-guardian opened (App author plus a repo-guardian/ head branch, verified through the Pull Requests API),\n" +
			"and closes exactly the recorded ones with a pointer comment. Nothing is written without --yes.",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(*cobra.Command, []string) error {
			return a.setupLogger(a.out)
		},
	}
	root.PersistentFlags().StringVar(&a.logOpts.Level, "log-level", "info", "log level: debug, info, warn, error")
	root.PersistentFlags().BoolVar(&a.logOpts.NoColor, "no-color", false, "disable coloured output (NO_COLOR is also honoured)")
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageError(err) })
	root.SetOut(a.out)
	root.SetErr(a.stderr)

	root.AddCommand(
		a.versionCommand(),
		a.configCommand(),
		a.prsCommand(),
	)
	return root
}

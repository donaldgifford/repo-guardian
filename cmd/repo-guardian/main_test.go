package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDispatch_UnknownSubcommand pins that a typo is an error rather
// than a silently-started server.
//
// This is the bug the dispatch switch exists to fix: before it,
// `repo-guardian report --out ./x` booted the HTTP server, because
// flag.CommandLine stops at the first non-flag argument and nothing
// inspected flag.Args(). A mistyped subcommand must not start a
// long-running process.
func TestDispatch_UnknownSubcommand(t *testing.T) {
	t.Parallel()

	err := dispatch([]string{"repo-guardian", "reprot"})
	if err == nil {
		t.Fatal("dispatch(reprot) = nil, want an error rather than a started server")
	}

	if !strings.Contains(err.Error(), "reprot") {
		t.Errorf("dispatch() error = %q, want it to name the unknown subcommand", err)
	}
}

// TestDispatch_ReportReachesTheSubcommand pins that `report` routes to
// runReport and not to run().
//
// Asserted through runReport's own no-DSN guard: reaching that error
// proves the argument vector was routed and parsed as a report
// invocation. The alternative — reaching run() — would try to load the
// server config and bind ports.
func TestDispatch_ReportReachesTheSubcommand(t *testing.T) {
	t.Setenv("STORE_DSN", "")

	err := dispatch([]string{"repo-guardian", "report", "--out", t.TempDir()})
	if err == nil {
		t.Fatal("dispatch(report) = nil, want the no-DSN error")
	}

	if !strings.Contains(err.Error(), "no database given") {
		t.Errorf("dispatch(report) = %q, want runReport's no-DSN error", err)
	}
}

// TestDispatch_HelpIsNotAnError pins that `help` succeeds and prints
// the subcommand list.
func TestDispatch_HelpIsNotAnError(t *testing.T) {
	t.Parallel()

	if err := dispatch([]string{"repo-guardian", "help"}); err != nil {
		t.Errorf("dispatch(help) = %v, want nil", err)
	}

	var buf bytes.Buffer

	usage(&buf)

	for _, want := range []string{"repo-guardian report", "repo-guardian help"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("usage() omits %q; the subcommand is undiscoverable\n%s", want, buf.String())
		}
	}
}

// TestReport_HelpFlagIsNotAFailure pins that `report -h` exits clean.
//
// flag.ContinueOnError returns ErrHelp for -h. Propagating it would
// make the binary print usage and then log "exited with error" over
// the top of it.
func TestReport_HelpFlagIsNotAFailure(t *testing.T) {
	t.Parallel()

	if err := runReport([]string{"-h"}); err != nil {
		t.Errorf("runReport(-h) = %v, want nil; -h is a request, not a failure", err)
	}
}

// TestReport_RequiresADSN pins that the command refuses rather than
// guessing at a local Postgres.
func TestReport_RequiresADSN(t *testing.T) {
	t.Setenv("STORE_DSN", "")

	err := runReport([]string{"--out", t.TempDir()})
	if err == nil {
		t.Fatal("runReport() = nil with no DSN, want an error")
	}

	if !strings.Contains(err.Error(), "--dsn") {
		t.Errorf("runReport() = %q, want the error to name the flag that fixes it", err)
	}
}

// TestInitLoggerTo_WritesToTheGivenWriter is the seam the report
// subcommand uses to keep its log records off stdout.
//
// The report prints its written paths to stdout so a shell pipeline can
// consume them; a JSON log record interleaved into that list would make
// `report | xargs` treat a log line as a filename.
func TestInitLoggerTo_WritesToTheGivenWriter(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	initLoggerTo(&buf, "info").Error("boom", "error", errors.New("x"))

	if !strings.Contains(buf.String(), "boom") {
		t.Errorf("initLoggerTo() wrote nothing to the given writer: %q", buf.String())
	}
}

// TestInitLogger_UsesStdout pins the SERVER's log sink.
//
// Stdout here is not an accident to be tidied up: it is what collects
// repo-guardian's logs in every existing deployment. A well-meaning
// "CLIs log to stderr" refactor that changed this would silently empty
// operators' log pipelines, which is why the assertion swaps the real
// os.Stdout rather than trusting the writer argument.
//
// Not parallel: it replaces a process-global.
func TestInitLogger_UsesStdout(t *testing.T) {
	saved := os.Stdout

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() = %v, want nil", err)
	}

	os.Stdout = w

	initLogger("info").Error("server line")

	os.Stdout = saved

	if err := w.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll() = %v, want nil", err)
	}

	if !strings.Contains(string(got), "server line") {
		t.Errorf("initLogger() did not write to os.Stdout; got %q", got)
	}
}

// TestDispatch_MonitoringRequiresAVerb pins that `monitoring` alone is
// an error rather than a started server.
func TestDispatch_MonitoringRequiresAVerb(t *testing.T) {
	t.Parallel()

	err := dispatch([]string{"repo-guardian", "monitoring"})
	if err == nil {
		t.Fatal("dispatch(monitoring) = nil, want an error naming the missing verb")
	}

	if !strings.Contains(err.Error(), "generate") {
		t.Errorf("dispatch(monitoring) = %q, want it to suggest the generate verb", err)
	}
}

// TestMonitoringGenerate_RejectsAMissingConfig pins the fix for
// policy.Load's silent fallback.
//
// Load treats a missing file as "use built-in defaults" and returns a
// nil error — right for the server, wrong here. Without this guard
// `monitoring generate --config guardain.hcl` emits the DEFAULT
// artifacts with exit 0, so a typo silently produces a dashboard for a
// policy nobody runs, and the generator-as-validation property of task
// 5.6 evaporates on exactly the invocation that most needs it.
func TestMonitoringGenerate_RejectsAMissingConfig(t *testing.T) {
	t.Parallel()

	err := runMonitoring([]string{"generate", "--config", filepath.Join(t.TempDir(), "absent.hcl")})
	if err == nil {
		t.Fatal("runMonitoring() = nil for a missing --config, want an error rather than default artifacts")
	}

	if !strings.Contains(err.Error(), "absent.hcl") {
		t.Errorf("runMonitoring() = %q, want the error to name the path", err)
	}
}

// TestMonitoringGenerate_EmptyConfigMeansDefaults pins that the
// missing-file guard does not break the built-in-defaults path, which
// is how the static tier is generated.
func TestMonitoringGenerate_EmptyConfigMeansDefaults(t *testing.T) {
	t.Setenv("GUARDIAN_CONFIG", "")

	if err := runMonitoring([]string{"generate", "--out", t.TempDir()}); err != nil {
		t.Errorf("runMonitoring() = %v with no --config, want nil (built-in defaults)", err)
	}
}

// TestMonitoringGenerate_RejectsAnUnknownFormat pins that an
// unsupported --format fails rather than silently emitting json.
func TestMonitoringGenerate_RejectsAnUnknownFormat(t *testing.T) {
	t.Parallel()

	err := runMonitoring([]string{"generate", "--format", "yaml"})
	if err == nil {
		t.Fatal("runMonitoring(--format yaml) = nil, want an error")
	}

	for _, want := range []string{"json", "k8s"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name the %q format", err, want)
		}
	}
}

// TestMonitoringGenerate_HelpFlagIsNotAFailure mirrors the report
// subcommand: -h is a request.
func TestMonitoringGenerate_HelpFlagIsNotAFailure(t *testing.T) {
	t.Parallel()

	if err := runMonitoring([]string{"generate", "-h"}); err != nil {
		t.Errorf("runMonitoring(generate -h) = %v, want nil", err)
	}
}

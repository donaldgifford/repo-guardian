package main

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestExitCode(t *testing.T) {
	t.Parallel()

	cause := errors.New("boom")
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "nil", err: nil, want: exitOK},
		{name: "found", err: &exitError{code: exitFound}, want: exitFound},
		{name: "usage", err: usageError(cause), want: exitUsage},
		{name: "operational", err: opError(cause), want: exitOperational},
		{name: "wrapped operational", err: fmt.Errorf("list: %w", opError(cause)), want: exitOperational},
		{name: "cobra error", err: cause, want: exitUsage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := exitCode(tt.err); got != tt.want {
				t.Errorf("exitCode(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}

func TestRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		args     []string
		want     int
		wantOut  string
		wantLogs string
	}{
		{
			name:    "version",
			args:    []string{cmdVersion, "--no-color"},
			want:    exitOK,
			wantOut: "rgctl github.com/donaldgifford/repo-guardian/tools/rgctl",
		},
		{name: "unknown command", args: []string{"nosuch"}, want: exitUsage, wantLogs: `unknown command "nosuch"`},
		{name: "unknown flag", args: []string{cmdVersion, "--bogus"}, want: exitUsage, wantLogs: "unknown flag: --bogus"},
		{name: "extra argument", args: []string{cmdVersion, "extra", "--no-color"}, want: exitUsage, wantLogs: `unknown command "extra"`},
		{name: "bad log level", args: []string{cmdVersion, "--log-level", "loud"}, want: exitUsage, wantLogs: `log level "loud"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			got := run(tt.args, &stdout, &stderr)
			if got != tt.want {
				t.Errorf("run(%q) = %d, want %d; stdout=%q stderr=%q", tt.args, got, tt.want, stdout.String(), stderr.String())
			}
			if tt.wantOut != "" && !strings.Contains(stdout.String(), tt.wantOut) {
				t.Errorf("run(%q) stdout = %q, want it to contain %q", tt.args, stdout.String(), tt.wantOut)
			}
			if tt.wantLogs != "" && !strings.Contains(stdout.String()+stderr.String(), tt.wantLogs) {
				t.Errorf("run(%q) output = %q, want it to contain %q", tt.args, stdout.String()+stderr.String(), tt.wantLogs)
			}
		})
	}
}

package main

import (
	"strings"
	"testing"
	"time"
)

// TestParseMigrateFlags_Chain is IMPL-0028 task 6.1: the controls chain
// is opt-in by flag, and an unknown chain is refused.
func TestParseMigrateFlags_Chain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		args    []string
		want    string
		wantErr string
	}{
		{args: []string{"--dsn", "postgres://x"}, want: chainRC},
		{args: []string{"--dsn", "postgres://x", "--chain", "controls"}, want: chainControls},
		{args: []string{"--dsn", "postgres://x", "--chain", "v1"}, wantErr: `unknown --chain "v1"`},
	}

	for _, tt := range tests {
		opts, err := parseMigrateFlags(tt.args)
		if tt.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("parseMigrateFlags(%v) = %v, want an error containing %q", tt.args, err, tt.wantErr)
			}

			continue
		}

		if err != nil || opts.chain != tt.want {
			t.Errorf("parseMigrateFlags(%v) = %q, %v; want %q", tt.args, opts.chain, err, tt.want)
		}
	}
}

// TestParseMigrateFlags_Wait is IMPL-0028 task 8.2: migrate waits 5m for
// the database by default, MIGRATE_WAIT sets it, and --wait wins.
func TestParseMigrateFlags_Wait(t *testing.T) {
	tests := []struct {
		name    string
		env     string
		args    []string
		want    time.Duration
		wantErr string
	}{
		{name: "default", want: defaultMigrateWait},
		{name: "env", env: "90s", want: 90 * time.Second},
		{name: "flag wins", env: "90s", args: []string{"--wait", "0"}, want: 0},
		{name: "bad env", env: "soon", wantErr: `invalid MIGRATE_WAIT "soon"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("MIGRATE_WAIT", tt.env)

			opts, err := parseMigrateFlags(append([]string{"--dsn", "postgres://x"}, tt.args...))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parseMigrateFlags() = %v, want an error containing %q", err, tt.wantErr)
				}

				return
			}

			if err != nil || opts.wait != tt.want {
				t.Errorf("parseMigrateFlags() wait = %s, %v; want %s", opts.wait, err, tt.want)
			}
		})
	}
}

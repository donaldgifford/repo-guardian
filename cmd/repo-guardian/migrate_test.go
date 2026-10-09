package main

import (
	"strings"
	"testing"
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

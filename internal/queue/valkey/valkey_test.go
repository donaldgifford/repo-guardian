package valkey_test

import (
	"strings"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/queue/valkey"
)

// JobID determinism is part of the contract — same triple → same
// hex output, different triples → different output. The hex length
// is fixed at 32 (16 bytes from a 32-byte SHA-256 truncated for
// readability in logs and dashboards).
func TestJobID_Deterministic(t *testing.T) {
	t.Parallel()

	a := valkey.JobID(42, "octo", "alpha")
	b := valkey.JobID(42, "octo", "alpha")

	if a != b {
		t.Fatalf("expected stable hash, got %q vs %q", a, b)
	}

	if len(a) != 32 {
		t.Fatalf("expected 32-char hex, got %d chars: %q", len(a), a)
	}

	if strings.ContainsAny(a, "ghijklmnopqrstuvwxyz") {
		t.Fatalf("expected lowercase hex, got %q", a)
	}
}

func TestJobID_DistinctsTriples(t *testing.T) {
	t.Parallel()

	cases := []struct {
		install int64
		owner   string
		repo    string
	}{
		{42, "octo", "alpha"},
		{43, "octo", "alpha"}, // different installation
		{42, "Octo", "alpha"}, // case-sensitive owner
		{42, "octo", "beta"},
		{0, "", ""},
	}

	seen := map[string]struct{}{}

	for _, c := range cases {
		id := valkey.JobID(c.install, c.owner, c.repo)
		if _, dup := seen[id]; dup {
			t.Fatalf("collision for triple %+v → %q", c, id)
		}

		seen[id] = struct{}{}
	}
}

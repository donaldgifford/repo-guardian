package reconciler_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/reconciler"
)

// The catalog-parse log line, verbatim.
//
// This is a CONTRACT, not an implementation detail. It is the only
// evidence that a repository's custom properties were left alone
// because its catalog-info.yaml could not be parsed — the counter
// (catalog_parse_failed_total{org}) says how many, and only the log
// says which repository and why. The E4 Loki dashboard and the LogQL
// recipes in contrib/ match on this exact string and these exact keys.
//
// Changing either is a breaking change for anyone whose alerting reads
// it, and it breaks silently: a LogQL matcher that stops matching
// returns no rows, which renders identically to "no repository has a
// broken catalog". Same failure shape as the
// custom-properties schema-preflight warning locked by
// TestAPIMode_FiltersUndefinedMappedProperty.
const (
	catalogParseFailedMsg = "catalog-info parse failed; skipping reconcile to avoid clearing properties"
	notComponentMsg       = "catalog-info is not a Backstage Component entity; skipping custom-properties reconcile"
)

// logRecord is one captured JSON log line.
type logRecord map[string]any

// captureLogs runs fn with a JSON logger and returns the records.
func captureLogs(t *testing.T, fn func(*slog.Logger)) []logRecord {
	t.Helper()

	var buf bytes.Buffer

	fn(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))

	var out []logRecord

	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}

		var rec logRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line is not JSON: %v\n%s", err, line)
		}

		out = append(out, rec)
	}

	return out
}

// findRecord returns the record with the given message.
func findRecord(t *testing.T, records []logRecord, msg string) logRecord {
	t.Helper()

	for _, rec := range records {
		if rec["msg"] == msg {
			return rec
		}
	}

	var seen []string
	for _, rec := range records {
		if m, ok := rec["msg"].(string); ok {
			seen = append(seen, m)
		}
	}

	t.Fatalf("no log record with msg %q; got %v", msg, seen)

	return nil
}

// TestCatalogParseFailure_LogContract locks the message and keys.
func TestCatalogParseFailure_LogContract(t *testing.T) {
	t.Parallel()

	client := newMockClient()

	records := captureLogs(t, func(logger *slog.Logger) {
		params := newParams(client, "::: not yaml at all :::", false, nil)
		params.Logger = logger
		params.Outcome = &reconciler.Outcome{}

		r := newTestReconciler(t, "api")
		if err := r.Reconcile(t.Context(), params); err != nil {
			t.Fatalf("Reconcile() = %v, want nil; a broken catalog is skipped, not failed", err)
		}
	})

	rec := findRecord(t, records, catalogParseFailedMsg)

	if rec["level"] != "WARN" {
		t.Errorf("level = %v, want WARN; the LogQL recipes filter on it", rec["level"])
	}

	// The keys a Loki query groups and filters by. `err` is what makes
	// the line actionable at all — without it an operator knows a
	// catalog is broken but not how.
	for _, key := range []string{"reconciler", "mode", "err"} {
		if _, ok := rec[key]; !ok {
			t.Errorf("log record has no %q key; got %v", key, keysOf(rec))
		}
	}

	if rec["reconciler"] != "custom_properties" {
		t.Errorf("reconciler = %v, want custom_properties", rec["reconciler"])
	}
}

// TestNotComponent_LogContract locks the sibling line.
//
// A valid non-Component entity is a different condition from a broken
// one: it is Info, it increments no counter, and an operator paging on
// it would be paging on a repository that is working as intended. The
// two lines must stay distinguishable by message.
func TestNotComponent_LogContract(t *testing.T) {
	t.Parallel()

	client := newMockClient()

	records := captureLogs(t, func(logger *slog.Logger) {
		params := newParams(client, "apiVersion: backstage.io/v1alpha1\nkind: System\nmetadata:\n  name: platform\n", false, nil)
		params.Logger = logger
		params.Outcome = &reconciler.Outcome{}

		r := newTestReconciler(t, "api")
		if err := r.Reconcile(t.Context(), params); err != nil {
			t.Fatalf("Reconcile() = %v, want nil", err)
		}
	})

	rec := findRecord(t, records, notComponentMsg)

	if rec["level"] != "INFO" {
		t.Errorf("level = %v, want INFO; a non-Component entity is not a fault", rec["level"])
	}

	// The parse-failure line must NOT also appear: a Loki query
	// matching it would otherwise count healthy repositories.
	for _, other := range records {
		if other["msg"] == catalogParseFailedMsg {
			t.Errorf("a non-Component entity also logged the parse-failure line: %v", other)
		}
	}
}

// TestCatalogParseFailure_CarriesCallerFields pins that the caller's
// attributes survive onto the record.
//
// The reconciler's own logger adds `reconciler` and `mode`; `owner` and
// `repo` come from the engine's per-repo logger
// (checker/engine.go: e.logger.With("owner", owner, "repo", repo)) and
// `rule` from the rule loop. Those are the fields a Loki panel groups
// by to answer "which repositories have a broken catalog", so the
// pass-through is part of the same contract even though this package
// does not set them.
func TestCatalogParseFailure_CarriesCallerFields(t *testing.T) {
	t.Parallel()

	client := newMockClient()

	records := captureLogs(t, func(logger *slog.Logger) {
		params := newParams(client, "{{{", false, nil)
		params.Logger = logger.With("owner", "acme", "repo", "widget", "rule", "catalog_info")
		params.Outcome = &reconciler.Outcome{}

		r := newTestReconciler(t, "api")
		if err := r.Reconcile(t.Context(), params); err != nil {
			t.Fatalf("Reconcile() = %v, want nil", err)
		}
	})

	rec := findRecord(t, records, catalogParseFailedMsg)

	for key, want := range map[string]string{"owner": "acme", "repo": "widget", "rule": "catalog_info"} {
		if rec[key] != want {
			t.Errorf("%s = %v, want %q; the Loki panels group by it", key, rec[key], want)
		}
	}
}

func keysOf(rec logRecord) []string {
	out := make([]string, 0, len(rec))
	for k := range rec {
		out = append(out, k)
	}

	return out
}

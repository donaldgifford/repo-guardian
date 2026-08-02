package queue_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/queue"
)

// TestJob_OldPayloadDecodesWithZeroRetryFields locks the upgrade
// contract from IMPL-0022 task 1.6: a Job serialised by a binary
// that predates Attempts/AvailableAt decodes with zero values for
// both — which are the correct semantics ("never retried", "runnable
// now"). No queue drain is required on upgrade.
func TestJob_OldPayloadDecodesWithZeroRetryFields(t *testing.T) {
	t.Parallel()

	// Exactly the field set the pre-IMPL-0022 binary marshalled.
	oldPayload := `{
		"ID": "a1b2c3",
		"InstallationID": 42,
		"Owner": "donaldgifford",
		"Repo": "logpush",
		"Trigger": "scheduler",
		"EnqueuedAt": "2026-08-01T00:00:00Z"
	}`

	var j queue.Job
	if err := json.Unmarshal([]byte(oldPayload), &j); err != nil {
		t.Fatalf("Unmarshal(old payload) = %v, want nil", err)
	}

	if j.ID != "a1b2c3" || j.InstallationID != 42 || j.Owner != "donaldgifford" ||
		j.Repo != "logpush" || j.Trigger != "scheduler" {
		t.Errorf("Unmarshal(old payload) = %+v, original fields not preserved", j)
	}

	if j.Attempts != 0 {
		t.Errorf("Attempts = %d, want 0 (never retried)", j.Attempts)
	}

	if !j.AvailableAt.IsZero() {
		t.Errorf("AvailableAt = %v, want zero (runnable now)", j.AvailableAt)
	}
}

// TestJob_RetryFieldsRoundTrip confirms the new fields survive the
// serialise/decode cycle the durable queue uses.
func TestJob_RetryFieldsRoundTrip(t *testing.T) {
	t.Parallel()

	due := time.Date(2026, 8, 2, 15, 0, 0, 0, time.UTC)
	in := queue.Job{
		ID:             "a1b2c3",
		InstallationID: 42,
		Owner:          "donaldgifford",
		Repo:           "logpush",
		Trigger:        queue.TriggerScheduler,
		EnqueuedAt:     due.Add(-time.Hour),
		Attempts:       3,
		AvailableAt:    due,
	}

	payload, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal() = %v, want nil", err)
	}

	var out queue.Job
	if err := json.Unmarshal(payload, &out); err != nil {
		t.Fatalf("Unmarshal() = %v, want nil", err)
	}

	if out.Attempts != in.Attempts {
		t.Errorf("Attempts = %d, want %d", out.Attempts, in.Attempts)
	}

	if !out.AvailableAt.Equal(in.AvailableAt) {
		t.Errorf("AvailableAt = %v, want %v", out.AvailableAt, in.AvailableAt)
	}
}

// TestRetryAfterError_ErrorsAsAndUnwrap locks the error-chain
// behaviour the worker's defer path depends on: errors.As recovers
// *RetryAfterError through wrapping, and Unwrap exposes the cause.
func TestRetryAfterError_ErrorsAsAndUnwrap(t *testing.T) {
	t.Parallel()

	cause := errors.New("rate limit exhausted")
	deferral := &queue.RetryAfterError{
		After:  time.Date(2026, 8, 2, 15, 0, 0, 0, time.UTC),
		Reason: "rate_limit",
		Err:    cause,
	}

	wrapped := fmt.Errorf("process job: %w", deferral)

	var got *queue.RetryAfterError
	if !errors.As(wrapped, &got) {
		t.Fatalf("errors.As(%v) = false, want *RetryAfterError recovered", wrapped)
	}

	if got.Reason != "rate_limit" || !got.After.Equal(deferral.After) {
		t.Errorf("recovered = %+v, want %+v", got, deferral)
	}

	if !errors.Is(wrapped, cause) {
		t.Errorf("errors.Is(wrapped, cause) = false, want true via Unwrap")
	}
}

package ingest

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"

	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

const secret = "s3cret"

// fakeStarter records starts and answers from err.
type fakeStarter struct {
	mu     sync.Mutex
	starts []client.StartWorkflowOptions
	inputs []*workflows.WebhookInput
	err    error
}

//nolint:gocritic // hugeParam: the signature is client.Client's.
func (f *fakeStarter) ExecuteWorkflow(_ context.Context, o client.StartWorkflowOptions, _ any, args ...any) (client.WorkflowRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.starts = append(f.starts, o)
	f.inputs = append(f.inputs, args[0].(*workflows.WebhookInput))

	return nil, f.err
}

func newHandler(starter Starter) *Handler {
	return New(secret, starter, "repo-guardian", map[string]bool{"renovate.json": true, "CODEOWNERS": true},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func post(t *testing.T, h http.Handler, event, delivery, body string, sign bool) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/github", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", event)

	if delivery != "" {
		req.Header.Set(deliveryHeader, delivery)
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))

	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if !sign {
		sig = "sha256=" + hex.EncodeToString(make([]byte, sha256.Size))
	}

	req.Header.Set("X-Hub-Signature-256", sig)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

const (
	pushWatched = `{"ref":"refs/heads/main","repository":{"id":9,"name":"widgets","default_branch":"main","owner":{"login":"acme"}},
		"installation":{"id":7},"commits":[{"added":[],"modified":["README.md"],"removed":["renovate.json"]}]}`
	pushUnwatched = `{"ref":"refs/heads/main","repository":{"id":9,"name":"widgets","default_branch":"main","owner":{"login":"acme"}},
		"installation":{"id":7},"commits":[{"modified":["README.md"]}]}`
	pushTag = `{"ref":"refs/tags/v1.0.0","repository":{"id":9,"name":"widgets","default_branch":"main","owner":{"login":"acme"}},
		"installation":{"id":7},"commits":[{"modified":["CODEOWNERS"]}]}`
	pushBranch = `{"ref":"refs/heads/feature","repository":{"id":9,"name":"widgets","default_branch":"main","owner":{"login":"acme"}},
		"installation":{"id":7},"commits":[{"modified":["CODEOWNERS"]}]}`
	repoRenamed = `{"action":"renamed","repository":{"id":9,"name":"gadgets","full_name":"acme/gadgets","owner":{"login":"acme"}},
		"installation":{"id":7}}`
	repoEdited   = `{"action":"edited","repository":{"id":9,"name":"widgets","owner":{"login":"acme"}},"installation":{"id":7}}`
	instReposAdd = `{"action":"added","installation":{"id":7,"account":{"login":"acme"}},
		"repositories_added":[{"id":9,"name":"widgets","full_name":"acme/widgets"},{"id":10,"name":"gadgets","full_name":"acme/gadgets"}]}`
	instSuspend = `{"action":"suspend","installation":{"id":7,"account":{"login":"acme"}}}`
)

func TestServeHTTP_Table(t *testing.T) {
	tests := []struct {
		name       string
		event      string
		body       string
		sign       bool
		delivery   string
		wantStatus int
		wantStart  bool
	}{
		{"bad signature", "push", pushWatched, false, "d1", http.StatusUnauthorized, false},
		{"push touching a removed watched path", "push", pushWatched, true, "d1", http.StatusAccepted, true},
		{"push touching no watched path", "push", pushUnwatched, true, "d1", http.StatusNoContent, false},
		{"tag push", "push", pushTag, true, "d1", http.StatusNoContent, false},
		{"non-default branch", "push", pushBranch, true, "d1", http.StatusNoContent, false},
		{"handled repository action", "repository", repoRenamed, true, "d1", http.StatusAccepted, true},
		{"unhandled repository action", "repository", repoEdited, true, "d1", http.StatusNoContent, false},
		{"installation repositories added", "installation_repositories", instReposAdd, true, "d1", http.StatusAccepted, true},
		{"installation suspended", "installation", instSuspend, true, "d1", http.StatusAccepted, true},
		{"unhandled event type", "star", `{"action":"created"}`, true, "d1", http.StatusNoContent, false},
		{"missing delivery id", "push", pushWatched, true, "", http.StatusBadRequest, false},
	}

	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			starter := &fakeStarter{}

			rec := post(t, newHandler(starter), tt.event, tt.delivery, tt.body, tt.sign)
			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}

			if started := len(starter.starts) == 1; started != tt.wantStart {
				t.Errorf("started = %v, want %v", started, tt.wantStart)
			}
		})
	}
}

func TestServeHTTP_StartOptionsAndInput(t *testing.T) {
	starter := &fakeStarter{}

	if rec := post(t, newHandler(starter), "installation_repositories", "d-42", instReposAdd, true); rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d", rec.Code)
	}

	o, in := starter.starts[0], starter.inputs[0]
	if o.ID != "webhook/d-42" || o.WorkflowIDReusePolicy.String() != "RejectDuplicate" || o.Priority.FairnessKey != "7" {
		t.Errorf("options = %+v", o)
	}

	if in.DeliveryID != "d-42" || in.AccountLogin != "acme" || len(in.Repositories) != 2 || in.Repositories[1].Org != "acme" ||
		in.Repositories[1].ID != 10 {
		t.Errorf("input = %+v", in)
	}
}

func TestServeHTTP_SignatureRejectionIsCounted(t *testing.T) {
	before := testutil.ToFloat64(metrics.WebhookRejectedTotal.WithLabelValues(reasonSignature))

	post(t, newHandler(&fakeStarter{}), "push", "d1", pushWatched, false)

	if got := testutil.ToFloat64(metrics.WebhookRejectedTotal.WithLabelValues(reasonSignature)) - before; got != 1 {
		t.Errorf("webhook_rejected_total{signature} += %v, want 1", got)
	}
}

func TestServeHTTP_DuplicateDeliveryIsAccepted(t *testing.T) {
	starter := &fakeStarter{err: serviceerror.NewWorkflowExecutionAlreadyStarted("started", "", "")}

	if rec := post(t, newHandler(starter), "push", "d1", pushWatched, true); rec.Code != http.StatusAccepted {
		t.Errorf("duplicate delivery = %d, want 202", rec.Code)
	}
}

func TestServeHTTP_TemporalUnreachableIs503(t *testing.T) {
	before := testutil.ToFloat64(metrics.WebhookTemporalErrorsTotal)
	starter := &fakeStarter{err: errors.New("connection refused")}

	if rec := post(t, newHandler(starter), "push", "d1", pushWatched, true); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 so GitHub records a failed delivery", rec.Code)
	}

	if got := testutil.ToFloat64(metrics.WebhookTemporalErrorsTotal) - before; got != 1 {
		t.Errorf("webhook_temporal_errors_total += %v, want 1", got)
	}
}

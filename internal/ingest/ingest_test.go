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
	"strconv"
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

	key := secret
	if !sign {
		key = ""
	}

	return postWithKey(t, h, event, delivery, body, key)
}

// postWithKey signs body with key; an empty key sends a zero signature.
func postWithKey(t *testing.T, h http.Handler, event, delivery, body, key string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/github", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", event)

	if delivery != "" {
		req.Header.Set(deliveryHeader, delivery)
	}

	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(body))

	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if key == "" {
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
		{"installation repositories added", eventInstallationRepositories, instReposAdd, true, "d1", http.StatusAccepted, true},
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

	if rec := post(t, newHandler(starter), eventInstallationRepositories, "d-42", instReposAdd, true); rec.Code != http.StatusAccepted {
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

// The two controls Apps' routes, as cmd/repo-guardian mounts them.
const (
	evalSecret      = "eval-secret"
	remediateSecret = "remediate-secret"
	evalAppID       = 101
	remediateAppID  = 202
)

func newAppHandlers(starter Starter) (eval, remediate *Handler) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	watched := map[string]bool{"renovate.json": true}

	return NewApp("eval", evalAppID, evalSecret, starter, "repo-guardian", watched, logger),
		NewApp("remediate", remediateAppID, remediateSecret, starter, "repo-guardian", watched, logger)
}

func installationAdded(appID int) string {
	return `{"action":"added","installation":{"id":7,"app_id":` + strconv.Itoa(appID) + `,"account":{"login":"acme"}},` +
		`"repositories_added":[{"id":9,"name":"widgets","full_name":"acme/widgets"}]}`
}

// TestNewApp_EachRouteValidatesOnlyItsOwnSecret is IMPL-0028 task 3.7:
// a delivery validates only on its own App's route, and is stamped with
// that App.
func TestNewApp_EachRouteValidatesOnlyItsOwnSecret(t *testing.T) {
	starter := &fakeStarter{}
	eval, remediate := newAppHandlers(starter)

	before := testutil.ToFloat64(metrics.WebhookRejectedTotal.WithLabelValues(reasonSignature))

	if rec := postWithKey(t, eval, eventInstallationRepositories, "d1", installationAdded(evalAppID), evalSecret); rec.Code != http.StatusAccepted {
		t.Fatalf("eval route with the eval secret = %d, want 202", rec.Code)
	}

	if rec := postWithKey(
		t,
		remediate,
		eventInstallationRepositories,
		"d2",
		installationAdded(remediateAppID),
		remediateSecret,
	); rec.Code != http.StatusAccepted {
		t.Fatalf("remediate route with the remediate secret = %d, want 202", rec.Code)
	}

	if got := []string{starter.inputs[0].App, starter.inputs[1].App}; got[0] != "eval" || got[1] != "remediate" {
		t.Errorf("stamped Apps = %v, want [eval remediate]", got)
	}

	if rec := postWithKey(
		t,
		eval,
		eventInstallationRepositories,
		"d3",
		installationAdded(evalAppID),
		remediateSecret,
	); rec.Code != http.StatusUnauthorized {
		t.Errorf("eval route with the remediate secret = %d, want 401", rec.Code)
	}

	if rec := postWithKey(
		t,
		remediate,
		eventInstallationRepositories,
		"d4",
		installationAdded(remediateAppID),
		evalSecret,
	); rec.Code != http.StatusUnauthorized {
		t.Errorf("remediate route with the eval secret = %d, want 401", rec.Code)
	}

	if got := testutil.ToFloat64(metrics.WebhookRejectedTotal.WithLabelValues(reasonSignature)) - before; got != 2 {
		t.Errorf("signature rejections = %v, want 2", got)
	}
}

// TestNewApp_AppMismatch: a correctly signed installation payload that
// names another App is refused and counted.
func TestNewApp_AppMismatch(t *testing.T) {
	starter := &fakeStarter{}
	eval, _ := newAppHandlers(starter)

	before := testutil.ToFloat64(metrics.WebhookRejectedTotal.WithLabelValues(reasonAppMismatch))

	for _, event := range []string{eventInstallationRepositories, eventInstallation} {
		body := installationAdded(remediateAppID)
		if event == eventInstallation {
			body = `{"action":"created","installation":{"id":7,"app_id":202,"account":{"login":"acme"}}}`
		}

		if rec := postWithKey(t, eval, event, "d-"+event, body, evalSecret); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s naming another App = %d, want 401", event, rec.Code)
		}
	}

	if got := testutil.ToFloat64(metrics.WebhookRejectedTotal.WithLabelValues(reasonAppMismatch)) - before; got != 2 {
		t.Errorf("app_mismatch rejections = %v, want 2", got)
	}

	if len(starter.starts) != 0 {
		t.Errorf("starts = %d, want none for a refused payload", len(starter.starts))
	}
}

// TestNew_SingleAppRouteStampsNoApp: the rc route is unchanged.
func TestNew_SingleAppRouteStampsNoApp(t *testing.T) {
	starter := &fakeStarter{}

	if rec := post(t, newHandler(starter), eventInstallationRepositories, "d1", installationAdded(999), true); rec.Code != http.StatusAccepted {
		t.Fatalf("rc route = %d, want 202 whatever the App id", rec.Code)
	}

	if starter.inputs[0].App != "" {
		t.Errorf("rc route stamped App %q, want empty", starter.inputs[0].App)
	}
}

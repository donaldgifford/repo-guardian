package webhook

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	gh "github.com/google/go-github/v68/github"

	"github.com/donaldgifford/repo-guardian/internal/checker"
)

const testSecret = "test-secret"

func signPayload(payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)

	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func makeRequest(t *testing.T, eventType string, payload any) *http.Request {
	t.Helper()

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", eventType)
	req.Header.Set("X-Hub-Signature-256", signPayload(body, testSecret))

	return req
}

func TestHandleWebhook_RepositoryCreated(t *testing.T) {
	t.Parallel()

	q := checker.NewQueue(10, slog.Default())
	h := NewHandler(testSecret, q, slog.Default(), nil)

	payload := &gh.RepositoryEvent{
		Action: gh.Ptr("created"),
		Repo: &gh.Repository{
			Name:  gh.Ptr("new-repo"),
			Owner: &gh.User{Login: gh.Ptr("myorg")},
		},
		Installation: &gh.Installation{ID: gh.Ptr(int64(123))},
	}

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, makeRequest(t, "repository", payload))

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestHandleWebhook_InstallationReposAdded(t *testing.T) {
	t.Parallel()

	q := checker.NewQueue(10, slog.Default())
	h := NewHandler(testSecret, q, slog.Default(), nil)

	payload := &gh.InstallationRepositoriesEvent{
		Action:       gh.Ptr("added"),
		Installation: &gh.Installation{ID: gh.Ptr(int64(456))},
		RepositoriesAdded: []*gh.Repository{
			{Name: gh.Ptr("repo-a"), FullName: gh.Ptr("myorg/repo-a")},
			{Name: gh.Ptr("repo-b"), FullName: gh.Ptr("myorg/repo-b")},
		},
	}

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, makeRequest(t, "installation_repositories", payload))

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestHandleWebhook_InstallationCreated(t *testing.T) {
	t.Parallel()

	q := checker.NewQueue(10, slog.Default())
	h := NewHandler(testSecret, q, slog.Default(), nil)

	payload := &gh.InstallationEvent{
		Action:       gh.Ptr("created"),
		Installation: &gh.Installation{ID: gh.Ptr(int64(789))},
		Repositories: []*gh.Repository{
			{Name: gh.Ptr("repo-x"), FullName: gh.Ptr("myorg/repo-x")},
		},
	}

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, makeRequest(t, "installation", payload))

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestHandleWebhook_InvalidSignature(t *testing.T) {
	t.Parallel()

	q := checker.NewQueue(10, slog.Default())
	h := NewHandler(testSecret, q, slog.Default(), nil)

	body := []byte(`{"action":"created"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", "repository")
	req.Header.Set("X-Hub-Signature-256", "sha256=invalid")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestHandleWebhook_UnsupportedEvent(t *testing.T) {
	t.Parallel()

	q := checker.NewQueue(10, slog.Default())
	h := NewHandler(testSecret, q, slog.Default(), nil)

	payload := map[string]string{"action": "completed"}

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, makeRequest(t, "check_run", payload))

	if rr.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", rr.Code)
	}
}

func TestHandleWebhook_IgnoredAction(t *testing.T) {
	t.Parallel()

	q := checker.NewQueue(10, slog.Default())
	h := NewHandler(testSecret, q, slog.Default(), nil)

	payload := &gh.RepositoryEvent{
		Action: gh.Ptr("deleted"),
		Repo: &gh.Repository{
			Name:  gh.Ptr("some-repo"),
			Owner: &gh.User{Login: gh.Ptr("myorg")},
		},
		Installation: &gh.Installation{ID: gh.Ptr(int64(123))},
	}

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, makeRequest(t, "repository", payload))

	// Ignored actions still return 200 (event was handled, just not actionable).
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

//nolint:unparam // test helper keeps defaultBranch param for clarity
func makePushPayload(ref, defaultBranch string, commits []*gh.HeadCommit) *gh.PushEvent {
	return &gh.PushEvent{
		Ref: gh.Ptr(ref),
		Repo: &gh.PushEventRepository{
			Name:          gh.Ptr("test-repo"),
			Owner:         &gh.User{Login: gh.Ptr("myorg")},
			DefaultBranch: gh.Ptr(defaultBranch),
		},
		Installation: &gh.Installation{ID: gh.Ptr(int64(123))},
		Commits:      commits,
	}
}

func TestHandlePush_WatchedFileAdded_Enqueues(t *testing.T) {
	t.Parallel()

	q := checker.NewQueue(10, slog.Default())
	watched := map[string]bool{"catalog-info.yaml": true}
	h := NewHandler(testSecret, q, slog.Default(), watched)

	payload := makePushPayload("refs/heads/main", "main", []*gh.HeadCommit{
		{Added: []string{"catalog-info.yaml"}},
	})

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, makeRequest(t, "push", payload))

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	if q.Len() != 1 {
		t.Errorf("expected 1 job in queue, got %d", q.Len())
	}
}

func TestHandlePush_WatchedFileModified_Enqueues(t *testing.T) {
	t.Parallel()

	q := checker.NewQueue(10, slog.Default())
	watched := map[string]bool{"catalog-info.yaml": true}
	h := NewHandler(testSecret, q, slog.Default(), watched)

	payload := makePushPayload("refs/heads/main", "main", []*gh.HeadCommit{
		{Modified: []string{"catalog-info.yaml"}},
	})

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, makeRequest(t, "push", payload))

	if q.Len() != 1 {
		t.Errorf("expected 1 job in queue, got %d", q.Len())
	}
}

func TestHandlePush_UnrelatedFiles_DoesNotEnqueue(t *testing.T) {
	t.Parallel()

	q := checker.NewQueue(10, slog.Default())
	watched := map[string]bool{"catalog-info.yaml": true}
	h := NewHandler(testSecret, q, slog.Default(), watched)

	payload := makePushPayload("refs/heads/main", "main", []*gh.HeadCommit{
		{Added: []string{"README.md"}, Modified: []string{"go.mod"}},
	})

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, makeRequest(t, "push", payload))

	if q.Len() != 0 {
		t.Errorf("expected 0 jobs in queue, got %d", q.Len())
	}
}

func TestHandlePush_NonDefaultBranch_DoesNotEnqueue(t *testing.T) {
	t.Parallel()

	q := checker.NewQueue(10, slog.Default())
	watched := map[string]bool{"catalog-info.yaml": true}
	h := NewHandler(testSecret, q, slog.Default(), watched)

	payload := makePushPayload("refs/heads/feature-branch", "main", []*gh.HeadCommit{
		{Added: []string{"catalog-info.yaml"}},
	})

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, makeRequest(t, "push", payload))

	if q.Len() != 0 {
		t.Errorf("expected 0 jobs in queue, got %d", q.Len())
	}
}

func TestHandlePush_RemovedOnly_DoesNotEnqueue(t *testing.T) {
	t.Parallel()

	q := checker.NewQueue(10, slog.Default())
	watched := map[string]bool{"catalog-info.yaml": true}
	h := NewHandler(testSecret, q, slog.Default(), watched)

	payload := makePushPayload("refs/heads/main", "main", []*gh.HeadCommit{
		{Removed: []string{"catalog-info.yaml"}},
	})

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, makeRequest(t, "push", payload))

	if q.Len() != 0 {
		t.Errorf("expected 0 jobs in queue, got %d", q.Len())
	}
}

func TestHandlePush_NoWatchedPaths_DoesNotEnqueue(t *testing.T) {
	t.Parallel()

	q := checker.NewQueue(10, slog.Default())
	h := NewHandler(testSecret, q, slog.Default(), nil)

	payload := makePushPayload("refs/heads/main", "main", []*gh.HeadCommit{
		{Added: []string{"catalog-info.yaml"}},
	})

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, makeRequest(t, "push", payload))

	if q.Len() != 0 {
		t.Errorf("expected 0 jobs in queue, got %d", q.Len())
	}
}

func TestHandlePush_WatchedFileInLaterCommit_Enqueues(t *testing.T) {
	t.Parallel()

	q := checker.NewQueue(10, slog.Default())
	watched := map[string]bool{"catalog-info.yaml": true}
	h := NewHandler(testSecret, q, slog.Default(), watched)

	payload := makePushPayload("refs/heads/main", "main", []*gh.HeadCommit{
		{Added: []string{"README.md"}},
		{Modified: []string{"go.mod"}},
		{Added: []string{"catalog-info.yaml"}},
	})

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, makeRequest(t, "push", payload))

	if q.Len() != 1 {
		t.Errorf("expected 1 job in queue, got %d", q.Len())
	}
}

func TestHandlePush_TagPush_DoesNotEnqueue(t *testing.T) {
	t.Parallel()

	q := checker.NewQueue(10, slog.Default())
	watched := map[string]bool{"catalog-info.yaml": true}
	h := NewHandler(testSecret, q, slog.Default(), watched)

	payload := makePushPayload("refs/tags/v1.0.0", "main", []*gh.HeadCommit{
		{Added: []string{"catalog-info.yaml"}},
	})

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, makeRequest(t, "push", payload))

	if q.Len() != 0 {
		t.Errorf("expected 0 jobs in queue, got %d", q.Len())
	}
}

func TestIntegration_PushEvent_EnqueueWithTriggerPush(t *testing.T) {
	t.Parallel()

	q := checker.NewQueue(10, slog.Default())
	watched := map[string]bool{
		"catalog-info.yaml": true,
		"catalog-info.yml":  true,
	}
	h := NewHandler(testSecret, q, slog.Default(), watched)

	payload := makePushPayload("refs/heads/main", "main", []*gh.HeadCommit{
		{Modified: []string{"catalog-info.yaml"}},
	})

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, makeRequest(t, "push", payload))

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	if q.Len() != 1 {
		t.Fatalf("expected 1 job in queue, got %d", q.Len())
	}
}

func TestExtractOwner(t *testing.T) {
	t.Parallel()

	tests := []struct {
		fullName string
		want     string
	}{
		{"myorg/repo", "myorg"},
		{"single", "single"},
		{"a/b/c", "a"},
	}

	for _, tt := range tests {
		got := extractOwner(tt.fullName)
		if got != tt.want {
			t.Errorf("extractOwner(%q) = %q, want %q", tt.fullName, got, tt.want)
		}
	}
}

package webhook

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gh "github.com/google/go-github/v88/github"
	"github.com/stretchr/testify/mock"

	"github.com/donaldgifford/repo-guardian/internal/store"
	storemocks "github.com/donaldgifford/repo-guardian/internal/store/mocks"
)

func postWebhook(t *testing.T, h *Handler, event string, payload any) *httptest.ResponseRecorder {
	t.Helper()

	req := makeRequest(t, event, payload)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	return w
}

func TestDiscovery_InstallationRepositoriesAdded_CallsUpsertIfMissing(t *testing.T) {
	q := newRecordingQueue()

	ms := storemocks.NewMockStore(t)

	var captured []*store.RepoState

	ms.EXPECT().
		UpsertIfMissing(mock.Anything, mock.Anything).
		Run(func(_ context.Context, s *store.RepoState) {
			captured = append(captured, s)
		}).
		Return(true, nil).
		Times(2)

	h := NewHandler(testSecret, q, slog.Default(), nil, ms, "v1", time.Hour)

	w := postWebhook(t, h, "installation_repositories", &gh.InstallationRepositoriesEvent{
		Action:       gh.Ptr("added"),
		Installation: &gh.Installation{ID: gh.Ptr(int64(42))},
		RepositoriesAdded: []*gh.Repository{
			{Name: gh.Ptr("repo-a"), FullName: gh.Ptr("octo/repo-a")},
			{Name: gh.Ptr("repo-b"), FullName: gh.Ptr("octo/repo-b")},
		},
	})

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusAccepted)
	}

	if got := q.Len(); got != 2 {
		t.Errorf("enqueued = %d, want 2", got)
	}

	if got := len(captured); got != 2 {
		t.Fatalf("captured states = %d, want 2", got)
	}

	for _, s := range captured {
		if s.InstallationID != 42 {
			t.Errorf("InstallationID = %d, want 42", s.InstallationID)
		}

		if s.Owner != "octo" {
			t.Errorf("Owner = %q, want octo", s.Owner)
		}

		if s.LastCheckStatus != store.StatusPending {
			t.Errorf("LastCheckStatus = %q, want %q", s.LastCheckStatus, store.StatusPending)
		}

		if s.PolicyVersion != "" {
			t.Errorf("PolicyVersion = %q, want empty (treat as drifted)", s.PolicyVersion)
		}

		if s.LastCheckedAt == nil {
			t.Error("LastCheckedAt is nil")
		}

		// Jitter must keep the seed time in [-2*freshness, 0].
		if s.LastCheckedAt != nil {
			age := time.Since(*s.LastCheckedAt)
			if age < 0 || age > 2*time.Hour {
				t.Errorf("seed jitter outside [-2h, 0]: %v", age)
			}
		}
	}
}

func TestDiscovery_RepositoryCreated_CallsUpsertIfMissing(t *testing.T) {
	q := newRecordingQueue()

	ms := storemocks.NewMockStore(t)
	ms.EXPECT().
		UpsertIfMissing(mock.Anything, mock.Anything).
		Return(true, nil).
		Once()

	h := NewHandler(testSecret, q, slog.Default(), nil, ms, "v1", time.Hour)

	w := postWebhook(t, h, "repository", &gh.RepositoryEvent{
		Action:       gh.Ptr("created"),
		Installation: &gh.Installation{ID: gh.Ptr(int64(7))},
		Repo: &gh.Repository{
			Name:  gh.Ptr("new-repo"),
			Owner: &gh.User{Login: gh.Ptr("octo")},
		},
	})

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusAccepted)
	}
}

func TestDiscovery_PushHandler_MarksPendingBeforeEnqueue(t *testing.T) {
	q := newRecordingQueue()

	ms := storemocks.NewMockStore(t)

	var captured *store.RepoState

	ms.EXPECT().
		UpdateRepoState(mock.Anything, mock.Anything).
		Run(func(_ context.Context, s *store.RepoState) { captured = s }).
		Return(nil).
		Once()

	watched := map[string]bool{"CODEOWNERS": true}
	h := NewHandler(testSecret, q, slog.Default(), watched, ms, "v1", time.Hour)

	w := postWebhook(t, h, "push", &gh.PushEvent{
		Ref:          gh.Ptr("refs/heads/main"),
		Installation: &gh.Installation{ID: gh.Ptr(int64(99))},
		Repo: &gh.PushEventRepository{
			Name:          gh.Ptr("payments"),
			Owner:         &gh.User{Login: gh.Ptr("octo")},
			DefaultBranch: gh.Ptr("main"),
		},
		Commits: []*gh.HeadCommit{
			{Modified: []string{"CODEOWNERS"}},
		},
	})

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusAccepted)
	}

	if captured == nil {
		t.Fatal("markPending did not call UpdateRepoState")
	}

	if captured.LastCheckStatus != store.StatusPending {
		t.Errorf("LastCheckStatus = %q, want %q", captured.LastCheckStatus, store.StatusPending)
	}

	if captured.PolicyVersion != "v1" {
		t.Errorf("PolicyVersion = %q, want v1", captured.PolicyVersion)
	}

	if got := q.Len(); got != 1 {
		t.Errorf("enqueued = %d, want 1", got)
	}
}

func TestDiscovery_StoreError_DoesNotBlockEnqueue(t *testing.T) {
	q := newRecordingQueue()

	ms := storemocks.NewMockStore(t)
	ms.EXPECT().
		UpsertIfMissing(mock.Anything, mock.Anything).
		Return(false, &fakeError{"DB unavailable"}).
		Once()

	h := NewHandler(testSecret, q, slog.Default(), nil, ms, "v1", time.Hour)

	w := postWebhook(t, h, "repository", &gh.RepositoryEvent{
		Action:       gh.Ptr("created"),
		Installation: &gh.Installation{ID: gh.Ptr(int64(7))},
		Repo: &gh.Repository{
			Name:  gh.Ptr("new-repo"),
			Owner: &gh.User{Login: gh.Ptr("octo")},
		},
	})

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d (Store error must not block ACK)", w.Code, http.StatusAccepted)
	}

	if got := q.Len(); got != 1 {
		t.Errorf("enqueued = %d, want 1 (Store error must not skip the queue)", got)
	}
}

type fakeError struct{ msg string }

func (e *fakeError) Error() string { return e.msg }

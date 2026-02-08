package github

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	gh "github.com/google/go-github/v68/github"
)

// newTestClient creates a GitHubClient backed by a httptest server.
func newTestClient(t *testing.T, mux *http.ServeMux) (*GitHubClient, *httptest.Server) {
	t.Helper()

	server := httptest.NewServer(mux)

	ghClient := gh.NewClient(nil)
	ghClient, err := ghClient.WithEnterpriseURLs(server.URL+"/", server.URL+"/")
	if err != nil {
		t.Fatalf("setting enterprise URLs: %v", err)
	}

	client := &GitHubClient{
		appClient:      ghClient,
		logger:         slog.Default(),
		installClients: make(map[int64]*gh.Client),
		scopedGHClient: ghClient,
	}

	return client, server
}

func TestGetContents_Exists(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/repos/owner/repo/contents/CODEOWNERS", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		resp := &gh.RepositoryContent{
			Name: gh.Ptr("CODEOWNERS"),
			Path: gh.Ptr("CODEOWNERS"),
			Type: gh.Ptr("file"),
		}

		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encoding response: %v", err)
		}
	})

	client, server := newTestClient(t, mux)
	defer server.Close()

	exists, err := client.GetContents(context.Background(), "owner", "repo", "CODEOWNERS")
	if err != nil {
		t.Fatalf("GetContents: %v", err)
	}

	if !exists {
		t.Error("expected file to exist")
	}
}

func TestGetContents_NotFound(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/repos/owner/repo/contents/CODEOWNERS", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)

		resp := &gh.ErrorResponse{
			Message: "Not Found",
		}

		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encoding response: %v", err)
		}
	})

	client, server := newTestClient(t, mux)
	defer server.Close()

	exists, err := client.GetContents(context.Background(), "owner", "repo", "CODEOWNERS")
	if err != nil {
		t.Fatalf("GetContents: %v", err)
	}

	if exists {
		t.Error("expected file to not exist")
	}
}

func TestListOpenPullRequests(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/repos/owner/repo/pulls", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		prs := []*gh.PullRequest{
			{
				Number: gh.Ptr(1),
				Title:  gh.Ptr("chore: add CODEOWNERS"),
				Head:   &gh.PullRequestBranch{Ref: gh.Ptr("add-codeowners")},
				State:  gh.Ptr("open"),
			},
			{
				Number: gh.Ptr(2),
				Title:  gh.Ptr("feat: new feature"),
				Head:   &gh.PullRequestBranch{Ref: gh.Ptr("feature-branch")},
				State:  gh.Ptr("open"),
			},
		}

		if err := json.NewEncoder(w).Encode(prs); err != nil {
			t.Errorf("encoding response: %v", err)
		}
	})

	client, server := newTestClient(t, mux)
	defer server.Close()

	prs, err := client.ListOpenPullRequests(context.Background(), "owner", "repo")
	if err != nil {
		t.Fatalf("ListOpenPullRequests: %v", err)
	}

	if len(prs) != 2 {
		t.Fatalf("expected 2 PRs, got %d", len(prs))
	}

	if prs[0].Title != "chore: add CODEOWNERS" {
		t.Errorf("expected first PR title 'chore: add CODEOWNERS', got %q", prs[0].Title)
	}

	if prs[1].Number != 2 {
		t.Errorf("expected second PR number 2, got %d", prs[1].Number)
	}
}

func TestGetRepository(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/repos/owner/repo", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		repo := &gh.Repository{
			Name:          gh.Ptr("repo"),
			Archived:      gh.Ptr(false),
			Fork:          gh.Ptr(false),
			DefaultBranch: gh.Ptr("main"),
		}

		if err := json.NewEncoder(w).Encode(repo); err != nil {
			t.Errorf("encoding response: %v", err)
		}
	})

	client, server := newTestClient(t, mux)
	defer server.Close()

	repo, err := client.GetRepository(context.Background(), "owner", "repo")
	if err != nil {
		t.Fatalf("GetRepository: %v", err)
	}

	if repo.Archived {
		t.Error("expected repo not to be archived")
	}

	if repo.DefaultRef != "main" {
		t.Errorf("expected default branch 'main', got %q", repo.DefaultRef)
	}
}

func TestCreatePullRequest(t *testing.T) {
	t.Parallel()

	var receivedTitle, receivedBody, receivedHead, receivedBase string

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v3/repos/owner/repo/pulls", func(w http.ResponseWriter, r *http.Request) {
		var req gh.NewPullRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decoding request: %v", err)
		}

		receivedTitle = req.GetTitle()
		receivedBody = req.GetBody()
		receivedHead = req.GetHead()
		receivedBase = req.GetBase()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)

		pr := &gh.PullRequest{
			Number: gh.Ptr(42),
			Title:  req.Title,
			Head:   &gh.PullRequestBranch{Ref: req.Head},
			State:  gh.Ptr("open"),
		}

		if err := json.NewEncoder(w).Encode(pr); err != nil {
			t.Errorf("encoding response: %v", err)
		}
	})

	client, server := newTestClient(t, mux)
	defer server.Close()

	pr, err := client.CreatePullRequest(
		context.Background(),
		"owner", "repo",
		"chore: add missing files",
		"PR body",
		"repo-guardian/add-missing-files",
		"main",
	)
	if err != nil {
		t.Fatalf("CreatePullRequest: %v", err)
	}

	if pr.Number != 42 {
		t.Errorf("expected PR number 42, got %d", pr.Number)
	}

	if receivedTitle != "chore: add missing files" {
		t.Errorf("expected title 'chore: add missing files', got %q", receivedTitle)
	}

	if receivedBody != "PR body" {
		t.Errorf("expected body 'PR body', got %q", receivedBody)
	}

	if receivedHead != "repo-guardian/add-missing-files" {
		t.Errorf("expected head 'repo-guardian/add-missing-files', got %q", receivedHead)
	}

	if receivedBase != "main" {
		t.Errorf("expected base 'main', got %q", receivedBase)
	}
}

func TestGetBranchSHA_Exists(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/repos/owner/repo/git/ref/heads/main", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		ref := &gh.Reference{
			Object: &gh.GitObject{
				SHA: gh.Ptr("abc123"),
			},
		}

		if err := json.NewEncoder(w).Encode(ref); err != nil {
			t.Errorf("encoding response: %v", err)
		}
	})

	client, server := newTestClient(t, mux)
	defer server.Close()

	sha, err := client.GetBranchSHA(context.Background(), "owner", "repo", "main")
	if err != nil {
		t.Fatalf("GetBranchSHA: %v", err)
	}

	if sha != "abc123" {
		t.Errorf("expected SHA 'abc123', got %q", sha)
	}
}

func TestGetBranchSHA_NotFound(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/repos/owner/repo/git/ref/heads/nonexistent", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	client, server := newTestClient(t, mux)
	defer server.Close()

	sha, err := client.GetBranchSHA(context.Background(), "owner", "repo", "nonexistent")
	if err != nil {
		t.Fatalf("GetBranchSHA: %v", err)
	}

	if sha != "" {
		t.Errorf("expected empty SHA for nonexistent branch, got %q", sha)
	}
}

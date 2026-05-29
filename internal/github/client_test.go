package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestCreateOrUpdateFile_FileMissing_Creates(t *testing.T) {
	t.Parallel()

	var (
		createCalled bool
		createBody   gh.RepositoryContentFileOptions
	)

	mux := http.NewServeMux()
	mux.HandleFunc(
		"GET /api/v3/repos/owner/repo/contents/CODEOWNERS",
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		},
	)
	mux.HandleFunc(
		"PUT /api/v3/repos/owner/repo/contents/CODEOWNERS",
		func(w http.ResponseWriter, r *http.Request) {
			createCalled = true

			if err := json.NewDecoder(r.Body).Decode(&createBody); err != nil {
				t.Errorf("decoding request: %v", err)
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)

			if err := json.NewEncoder(w).Encode(&gh.RepositoryContentResponse{}); err != nil {
				t.Errorf("encoding response: %v", err)
			}
		},
	)

	client, server := newTestClient(t, mux)
	defer server.Close()

	err := client.CreateOrUpdateFile(
		context.Background(),
		"owner", "repo", "repo-guardian/add-missing-files", "CODEOWNERS",
		"* @platform-team", "chore: add CODEOWNERS",
	)
	if err != nil {
		t.Fatalf("CreateOrUpdateFile: %v", err)
	}

	if !createCalled {
		t.Fatal("expected create PUT to fire when file is missing")
	}

	if createBody.SHA != nil {
		t.Errorf("expected no sha on create, got %q", *createBody.SHA)
	}

	if string(createBody.Content) != "* @platform-team" {
		t.Errorf("expected content forwarded to create, got %q", string(createBody.Content))
	}
}

func TestCreateOrUpdateFile_FileExistsIdentical_Skips(t *testing.T) {
	t.Parallel()

	var putCalled bool

	content := "* @platform-team"

	mux := http.NewServeMux()
	mux.HandleFunc(
		"GET /api/v3/repos/owner/repo/contents/CODEOWNERS",
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")

			resp := &gh.RepositoryContent{
				Name:     gh.Ptr("CODEOWNERS"),
				Path:     gh.Ptr("CODEOWNERS"),
				Type:     gh.Ptr("file"),
				SHA:      gh.Ptr("abc123"),
				Encoding: gh.Ptr("base64"),
				Content:  gh.Ptr(base64.StdEncoding.EncodeToString([]byte(content))),
			}

			if err := json.NewEncoder(w).Encode(resp); err != nil {
				t.Errorf("encoding response: %v", err)
			}
		},
	)
	mux.HandleFunc(
		"PUT /api/v3/repos/owner/repo/contents/CODEOWNERS",
		func(w http.ResponseWriter, _ *http.Request) {
			putCalled = true

			w.WriteHeader(http.StatusOK)
		},
	)

	client, server := newTestClient(t, mux)
	defer server.Close()

	err := client.CreateOrUpdateFile(
		context.Background(),
		"owner", "repo", "repo-guardian/add-missing-files", "CODEOWNERS",
		content, "chore: add CODEOWNERS",
	)
	if err != nil {
		t.Fatalf("CreateOrUpdateFile: %v", err)
	}

	if putCalled {
		t.Error("expected no PUT when content matches existing file")
	}
}

func TestCreateOrUpdateFile_FileExistsDifferent_Updates(t *testing.T) {
	t.Parallel()

	var (
		putCalled bool
		putBody   gh.RepositoryContentFileOptions
	)

	mux := http.NewServeMux()
	mux.HandleFunc(
		"GET /api/v3/repos/owner/repo/contents/CODEOWNERS",
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")

			resp := &gh.RepositoryContent{
				Name:     gh.Ptr("CODEOWNERS"),
				Path:     gh.Ptr("CODEOWNERS"),
				Type:     gh.Ptr("file"),
				SHA:      gh.Ptr("oldsha456"),
				Encoding: gh.Ptr("base64"),
				Content:  gh.Ptr(base64.StdEncoding.EncodeToString([]byte("* @old-team"))),
			}

			if err := json.NewEncoder(w).Encode(resp); err != nil {
				t.Errorf("encoding response: %v", err)
			}
		},
	)
	mux.HandleFunc(
		"PUT /api/v3/repos/owner/repo/contents/CODEOWNERS",
		func(w http.ResponseWriter, r *http.Request) {
			putCalled = true

			if err := json.NewDecoder(r.Body).Decode(&putBody); err != nil {
				t.Errorf("decoding request: %v", err)
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)

			if err := json.NewEncoder(w).Encode(&gh.RepositoryContentResponse{}); err != nil {
				t.Errorf("encoding response: %v", err)
			}
		},
	)

	client, server := newTestClient(t, mux)
	defer server.Close()

	err := client.CreateOrUpdateFile(
		context.Background(),
		"owner", "repo", "repo-guardian/add-missing-files", "CODEOWNERS",
		"* @new-team", "chore: update CODEOWNERS",
	)
	if err != nil {
		t.Fatalf("CreateOrUpdateFile: %v", err)
	}

	if !putCalled {
		t.Fatal("expected PUT to fire when content differs from existing file")
	}

	if putBody.SHA == nil || *putBody.SHA != "oldsha456" {
		got := "<nil>"
		if putBody.SHA != nil {
			got = *putBody.SHA
		}

		t.Errorf("expected sha 'oldsha456' on update, got %q", got)
	}

	if string(putBody.Content) != "* @new-team" {
		t.Errorf("expected new content on update, got %q", string(putBody.Content))
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

func TestGetFileContent_Exists(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc(
		"GET /api/v3/repos/owner/repo/contents/catalog-info.yaml",
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")

			resp := &gh.RepositoryContent{
				Name:     gh.Ptr("catalog-info.yaml"),
				Path:     gh.Ptr("catalog-info.yaml"),
				Type:     gh.Ptr("file"),
				Encoding: gh.Ptr("base64"),
				Content:  gh.Ptr("YXBpVmVyc2lvbjogYmFja3N0YWdlLmlvL3YxYWxwaGEx"), // "apiVersion: backstage.io/v1alpha1"
			}

			if err := json.NewEncoder(w).Encode(resp); err != nil {
				t.Errorf("encoding response: %v", err)
			}
		},
	)

	client, server := newTestClient(t, mux)
	defer server.Close()

	content, err := client.GetFileContent(context.Background(), "owner", "repo", "catalog-info.yaml")
	if err != nil {
		t.Fatalf("GetFileContent: %v", err)
	}

	if content != "apiVersion: backstage.io/v1alpha1" {
		t.Errorf("expected decoded content, got %q", content)
	}
}

func TestGetFileContent_NotFound(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc(
		"GET /api/v3/repos/owner/repo/contents/catalog-info.yaml",
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)

			resp := &gh.ErrorResponse{Message: "Not Found"}

			if err := json.NewEncoder(w).Encode(resp); err != nil {
				t.Errorf("encoding response: %v", err)
			}
		},
	)

	client, server := newTestClient(t, mux)
	defer server.Close()

	content, err := client.GetFileContent(context.Background(), "owner", "repo", "catalog-info.yaml")
	if err != nil {
		t.Fatalf("GetFileContent: %v", err)
	}

	if content != "" {
		t.Errorf("expected empty string for missing file, got %q", content)
	}
}

func TestGetCustomPropertyValues(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc(
		"GET /api/v3/repos/owner/repo/properties/values",
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")

			props := []*gh.CustomPropertyValue{
				{PropertyName: "Owner", Value: "platform-team"},
				{PropertyName: "Component", Value: "my-service"},
			}

			if err := json.NewEncoder(w).Encode(props); err != nil {
				t.Errorf("encoding response: %v", err)
			}
		},
	)

	client, server := newTestClient(t, mux)
	defer server.Close()

	props, err := client.GetCustomPropertyValues(context.Background(), "owner", "repo")
	if err != nil {
		t.Fatalf("GetCustomPropertyValues: %v", err)
	}

	if len(props) != 2 {
		t.Fatalf("expected 2 properties, got %d", len(props))
	}

	if props[0].PropertyName != "Owner" || props[0].Value != "platform-team" {
		t.Errorf("unexpected first property: %+v", props[0])
	}

	if props[1].PropertyName != "Component" || props[1].Value != "my-service" {
		t.Errorf("unexpected second property: %+v", props[1])
	}
}

func TestSetCustomPropertyValues(t *testing.T) {
	t.Parallel()

	var receivedBody []byte

	mux := http.NewServeMux()
	mux.HandleFunc(
		"PATCH /api/v3/repos/owner/repo/properties/values",
		func(w http.ResponseWriter, r *http.Request) {
			var err error
			receivedBody, err = io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("reading request body: %v", err)
			}

			w.WriteHeader(http.StatusNoContent)
		},
	)

	client, server := newTestClient(t, mux)
	defer server.Close()

	err := client.SetCustomPropertyValues(context.Background(), "owner", "repo", []*CustomPropertyValue{
		{PropertyName: "Owner", Value: "platform-team"},
		{PropertyName: "Component", Value: "my-service"},
	})
	if err != nil {
		t.Fatalf("SetCustomPropertyValues: %v", err)
	}

	if len(receivedBody) == 0 {
		t.Fatal("expected request body to be sent")
	}

	// Verify the body contains our property names.
	bodyStr := string(receivedBody)
	if !strings.Contains(bodyStr, "Owner") || !strings.Contains(bodyStr, "platform-team") {
		t.Errorf("request body missing expected properties: %s", bodyStr)
	}
}

func TestGetVulnerabilityAlertsEnabled(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc(
		"GET /api/v3/repos/owner/repo/vulnerability-alerts",
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent) // 204 = enabled
		},
	)

	client, server := newTestClient(t, mux)
	defer server.Close()

	enabled, err := client.GetVulnerabilityAlertsEnabled(context.Background(), "owner", "repo")
	if err != nil {
		t.Fatalf("GetVulnerabilityAlertsEnabled: %v", err)
	}

	if !enabled {
		t.Error("expected vulnerability alerts to be enabled")
	}
}

func TestGetRepoSettings(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/repos/owner/repo", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		repo := &gh.Repository{
			DefaultBranch:       gh.Ptr("main"),
			HasIssues:           gh.Ptr(true),
			HasWiki:             gh.Ptr(false),
			DeleteBranchOnMerge: gh.Ptr(true),
			AllowMergeCommit:    gh.Ptr(true),
			AllowSquashMerge:    gh.Ptr(true),
			AllowRebaseMerge:    gh.Ptr(false),
		}

		if err := json.NewEncoder(w).Encode(repo); err != nil {
			t.Errorf("encoding response: %v", err)
		}
	})

	client, server := newTestClient(t, mux)
	defer server.Close()

	settings, err := client.GetRepoSettings(context.Background(), "owner", "repo")
	if err != nil {
		t.Fatalf("GetRepoSettings: %v", err)
	}

	if settings.DefaultBranch != "main" {
		t.Errorf("expected default_branch=main, got %q", settings.DefaultBranch)
	}

	if !settings.HasIssues {
		t.Error("expected has_issues=true")
	}

	if settings.HasWiki {
		t.Error("expected has_wiki=false")
	}

	if !settings.DeleteBranchOnMerge {
		t.Error("expected delete_branch_on_merge=true")
	}

	if settings.AllowRebaseMerge {
		t.Error("expected allow_rebase_merge=false")
	}
}

func TestUpdateRepository(t *testing.T) {
	t.Parallel()

	var receivedBody map[string]any

	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/v3/repos/owner/repo", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&receivedBody); err != nil {
			t.Errorf("decoding request: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")

		if err := json.NewEncoder(w).Encode(&gh.Repository{Name: gh.Ptr("repo")}); err != nil {
			t.Errorf("encoding response: %v", err)
		}
	})

	client, server := newTestClient(t, mux)
	defer server.Close()

	trueVal := true

	err := client.UpdateRepository(context.Background(), "owner", "repo", &RepoUpdateOpts{
		DeleteBranchOnMerge: &trueVal,
	})
	if err != nil {
		t.Fatalf("UpdateRepository: %v", err)
	}

	if v, ok := receivedBody["delete_branch_on_merge"]; !ok || v != true {
		t.Errorf("expected delete_branch_on_merge=true in request body, got %v", receivedBody)
	}
}

func TestListLabels(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/repos/owner/repo/labels", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		labels := []*gh.Label{
			{Name: gh.Ptr("bug"), Color: gh.Ptr("d73a4a"), Description: gh.Ptr("Something isn't working")},
			{Name: gh.Ptr("enhancement"), Color: gh.Ptr("a2eeef"), Description: gh.Ptr("New feature")},
		}

		if err := json.NewEncoder(w).Encode(labels); err != nil {
			t.Errorf("encoding response: %v", err)
		}
	})

	client, server := newTestClient(t, mux)
	defer server.Close()

	labels, err := client.ListLabels(context.Background(), "owner", "repo")
	if err != nil {
		t.Fatalf("ListLabels: %v", err)
	}

	if len(labels) != 2 {
		t.Fatalf("expected 2 labels, got %d", len(labels))
	}

	if labels[0].Name != "bug" {
		t.Errorf("expected first label name 'bug', got %q", labels[0].Name)
	}

	if labels[0].Color != "d73a4a" {
		t.Errorf("expected color 'd73a4a', got %q", labels[0].Color)
	}
}

func TestCreateLabel(t *testing.T) {
	t.Parallel()

	var receivedBody map[string]any

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v3/repos/owner/repo/labels", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&receivedBody); err != nil {
			t.Errorf("decoding request: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)

		if err := json.NewEncoder(w).Encode(&gh.Label{Name: gh.Ptr("bug")}); err != nil {
			t.Errorf("encoding response: %v", err)
		}
	})

	client, server := newTestClient(t, mux)
	defer server.Close()

	err := client.CreateLabel(context.Background(), "owner", "repo", &Label{
		Name:        "bug",
		Color:       "d73a4a",
		Description: "Something isn't working",
	})
	if err != nil {
		t.Fatalf("CreateLabel: %v", err)
	}

	if receivedBody["name"] != "bug" {
		t.Errorf("expected name 'bug' in request, got %v", receivedBody["name"])
	}
}

func TestDeleteLabel(t *testing.T) {
	t.Parallel()

	deleted := false

	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/v3/repos/owner/repo/labels/obsolete", func(w http.ResponseWriter, _ *http.Request) {
		deleted = true
		w.WriteHeader(http.StatusNoContent)
	})

	client, server := newTestClient(t, mux)
	defer server.Close()

	err := client.DeleteLabel(context.Background(), "owner", "repo", "obsolete")
	if err != nil {
		t.Fatalf("DeleteLabel: %v", err)
	}

	if !deleted {
		t.Error("expected DELETE request to be made")
	}
}

// IMPL-0013 Phase 2 — DeleteFile, UpdatePullRequest, ClosePullRequest,
// ListPRComments, UpsertPRComment tests. Each covers the happy path
// and at least one error path.

func TestDeleteFile_HappyPath(t *testing.T) {
	t.Parallel()

	gotSHA := ""
	gotBranch := ""

	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/v3/repos/owner/repo/contents/CODEOWNERS", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			SHA    string `json:"sha"`
			Branch string `json:"branch"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		gotSHA = body.SHA
		gotBranch = body.Branch

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"commit":{"sha":"def456"}}`))
	})

	client, server := newTestClient(t, mux)
	defer server.Close()

	if err := client.DeleteFile(
		context.Background(), "owner", "repo",
		"repo-guardian/add-missing-files", "CODEOWNERS", "abc123", "remove orphan",
	); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}

	if gotSHA != "abc123" {
		t.Errorf("sha sent = %q, want abc123", gotSHA)
	}
	if gotBranch != "repo-guardian/add-missing-files" {
		t.Errorf("branch sent = %q, want repo-guardian/add-missing-files", gotBranch)
	}
}

func TestDeleteFile_NotFound_Errors(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/v3/repos/owner/repo/contents/CODEOWNERS", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	client, server := newTestClient(t, mux)
	defer server.Close()

	err := client.DeleteFile(
		context.Background(), "owner", "repo",
		"repo-guardian/add-missing-files", "CODEOWNERS", "abc123", "remove orphan",
	)
	if err == nil {
		t.Fatal("DeleteFile: expected error on 404, got nil")
	}
}

func TestUpdatePullRequest_HappyPath(t *testing.T) {
	t.Parallel()

	var gotPatch struct {
		Title string `json:"title"`
		Body  string `json:"body"`
		State string `json:"state"`
	}

	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/v3/repos/owner/repo/pulls/5", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotPatch); err != nil {
			t.Fatalf("decode: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"number":5,"title":"updated","body":"new body","state":"open"}`))
	})

	client, server := newTestClient(t, mux)
	defer server.Close()

	if err := client.UpdatePullRequest(
		context.Background(), "owner", "repo", 5, "updated", "new body",
	); err != nil {
		t.Fatalf("UpdatePullRequest: %v", err)
	}

	if gotPatch.Title != "updated" || gotPatch.Body != "new body" {
		t.Errorf("patch sent: %+v", gotPatch)
	}
	if gotPatch.State != "" {
		t.Errorf("state should not be set on update, got %q", gotPatch.State)
	}
}

func TestUpdatePullRequest_NotFound_Errors(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/v3/repos/owner/repo/pulls/999", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	client, server := newTestClient(t, mux)
	defer server.Close()

	if err := client.UpdatePullRequest(
		context.Background(), "owner", "repo", 999, "x", "y",
	); err == nil {
		t.Fatal("UpdatePullRequest: expected error on 404, got nil")
	}
}

func TestClosePullRequest_HappyPath(t *testing.T) {
	t.Parallel()

	var gotPatch struct {
		State string `json:"state"`
		Title string `json:"title"`
	}

	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/v3/repos/owner/repo/pulls/7", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotPatch); err != nil {
			t.Fatalf("decode: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"number":7,"state":"closed"}`))
	})

	client, server := newTestClient(t, mux)
	defer server.Close()

	if err := client.ClosePullRequest(context.Background(), "owner", "repo", 7); err != nil {
		t.Fatalf("ClosePullRequest: %v", err)
	}

	if gotPatch.State != "closed" {
		t.Errorf("state sent = %q, want closed", gotPatch.State)
	}
	if gotPatch.Title != "" {
		t.Errorf("title should not be modified, got %q", gotPatch.Title)
	}
}

func TestClosePullRequest_Errors(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/v3/repos/owner/repo/pulls/7", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
	})

	client, server := newTestClient(t, mux)
	defer server.Close()

	if err := client.ClosePullRequest(context.Background(), "owner", "repo", 7); err == nil {
		t.Fatal("ClosePullRequest: expected error on 422, got nil")
	}
}

func TestListPRComments_HappyPath(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/repos/owner/repo/issues/5/comments", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"id": 1, "body": "first comment"},
			{"id": 2, "body": "<!-- repo-guardian:reconcile-log:v1 -->\nstatus update"}
		]`))
	})

	client, server := newTestClient(t, mux)
	defer server.Close()

	comments, err := client.ListPRComments(context.Background(), "owner", "repo", 5)
	if err != nil {
		t.Fatalf("ListPRComments: %v", err)
	}
	if len(comments) != 2 {
		t.Fatalf("expected 2 comments, got %d", len(comments))
	}
	if comments[0].ID != 1 || comments[1].ID != 2 {
		t.Errorf("unexpected IDs: %v", comments)
	}
	if !strings.HasPrefix(comments[1].Body, "<!-- repo-guardian:reconcile-log:v1 -->") {
		t.Errorf("comment 2 body lost marker: %q", comments[1].Body)
	}
}

func TestListPRComments_Errors(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/repos/owner/repo/issues/5/comments", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	client, server := newTestClient(t, mux)
	defer server.Close()

	if _, err := client.ListPRComments(context.Background(), "owner", "repo", 5); err == nil {
		t.Fatal("ListPRComments: expected error on 500, got nil")
	}
}

func TestUpsertPRComment_CreatesWhenMissing(t *testing.T) {
	t.Parallel()

	var created struct {
		Body string `json:"body"`
	}
	editedID := int64(0)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/repos/owner/repo/issues/5/comments", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id": 1, "body": "human comment"}]`))
	})
	mux.HandleFunc("POST /api/v3/repos/owner/repo/issues/5/comments", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
			t.Fatalf("decode: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id": 99}`))
	})
	mux.HandleFunc("PATCH /api/v3/repos/owner/repo/issues/comments/", func(w http.ResponseWriter, r *http.Request) {
		editedID = 1
		w.WriteHeader(http.StatusOK)
		_ = r
	})

	client, server := newTestClient(t, mux)
	defer server.Close()

	const marker = "<!-- repo-guardian:reconcile-log:v1 -->"
	if err := client.UpsertPRComment(
		context.Background(), "owner", "repo", 5, marker, "reconciled at 2026-05-29",
	); err != nil {
		t.Fatalf("UpsertPRComment: %v", err)
	}

	if !strings.HasPrefix(created.Body, marker) {
		t.Errorf("created body missing marker on row 1: %q", created.Body)
	}
	if editedID != 0 {
		t.Errorf("expected create-not-edit, but PATCH was issued")
	}
}

func TestUpsertPRComment_EditsExisting(t *testing.T) {
	t.Parallel()

	var patched struct {
		Body string `json:"body"`
	}
	createCount := 0

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/repos/owner/repo/issues/5/comments", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"id": 1, "body": "human comment"},
			{"id": 42, "body": "<!-- repo-guardian:reconcile-log:v1 -->\nold body"}
		]`))
	})
	mux.HandleFunc("POST /api/v3/repos/owner/repo/issues/5/comments", func(w http.ResponseWriter, _ *http.Request) {
		createCount++
		w.WriteHeader(http.StatusCreated)
	})
	mux.HandleFunc("PATCH /api/v3/repos/owner/repo/issues/comments/42", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&patched); err != nil {
			t.Fatalf("decode: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id": 42}`))
	})

	client, server := newTestClient(t, mux)
	defer server.Close()

	const marker = "<!-- repo-guardian:reconcile-log:v1 -->"
	if err := client.UpsertPRComment(
		context.Background(), "owner", "repo", 5, marker, "new body",
	); err != nil {
		t.Fatalf("UpsertPRComment: %v", err)
	}

	if createCount != 0 {
		t.Errorf("expected edit-not-create, but %d create call(s) issued", createCount)
	}
	if !strings.HasPrefix(patched.Body, marker) {
		t.Errorf("edited body missing marker: %q", patched.Body)
	}
	if !strings.Contains(patched.Body, "new body") {
		t.Errorf("edited body missing new content: %q", patched.Body)
	}
}

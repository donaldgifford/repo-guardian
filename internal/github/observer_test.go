package github

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"testing"
)

func TestRepoObserver_ListPullRequests_FiltersByPrefixAcrossPages(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/repos/o/r/pulls", func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page == 0 {
			page = 1
		}

		head := map[int]string{1: "repo-guardian/control/a", 2: "feature/x"}[page]
		if page == 1 {
			w.Header().Set("Link", fmt.Sprintf(`<%s?page=2>; rel="next"`, r.URL.Path))
		}

		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"number": page,
			"head":   map[string]any{"ref": head, "sha": "h", "repo": map[string]any{"id": 42}},
			"user":   map[string]any{"id": 7, "login": "rg[bot]", "type": "Bot"},
		}, {
			"number": 10 + page,
			"head":   map[string]any{"ref": "repo-guardian/control/b"},
		}})
	})

	c, srv := newTestClient(t, mux)
	defer srv.Close()

	got, err := c.RepoObserver("o", "r").ListPullRequests(t.Context(), "repo-guardian/")
	if err != nil {
		t.Fatalf("ListPullRequests = %v", err)
	}

	numbers := make([]int, 0, len(got))
	for i := range got {
		numbers = append(numbers, got[i].Number)
	}

	if fmt.Sprint(numbers) != "[1 11 12]" {
		t.Fatalf("ListPullRequests numbers = %v, want [1 11 12]", numbers)
	}

	if pr := got[0]; pr.AuthorID != 7 || pr.AuthorType != "Bot" || pr.HeadRepoID != 42 {
		t.Errorf("PR 1 author = %d/%s, head repo = %d; want 7/Bot, 42", pr.AuthorID, pr.AuthorType, pr.HeadRepoID)
	}

	if got[1].HeadRepoID != 0 {
		t.Errorf("PR 11 HeadRepoID = %d, want 0 for a deleted head repository", got[1].HeadRepoID)
	}
}

func TestRepoObserver_ListPullRequests_FailedPageIsAnError(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/repos/o/r/pulls", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "2" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.Header().Set("Link", fmt.Sprintf(`<%s?page=2>; rel="next"`, r.URL.Path))
		_, _ = w.Write([]byte(`[{"number":1,"head":{"ref":"repo-guardian/x"}}]`))
	})

	c, srv := newTestClient(t, mux)
	defer srv.Close()

	if got, err := c.RepoObserver("o", "r").ListPullRequests(t.Context(), "repo-guardian/"); err == nil {
		t.Errorf("ListPullRequests = %v, nil; want an error, never a short list", got)
	}
}

func TestRepoObserver_GetPullRequestAndCommits(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/repos/o/r/pulls/5", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"number":5,"state":"closed","merged":true,"base":{"ref":"main"},"user":{"id":3,"type":"User"}}`))
	})
	mux.HandleFunc("GET /api/v3/repos/o/r/commits", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("sha") != "rg/branch" {
			t.Errorf("sha = %q, want rg/branch", r.URL.Query().Get("sha"))
		}

		_, _ = w.Write([]byte(`[{"sha":"c2","commit":{"message":"two","verification":{"verified":true}},` +
			`"author":{"id":9,"login":"bot"},"committer":{"login":"web-flow"}},{"sha":"c1","commit":{"message":"one"}}]`))
	})

	c, srv := newTestClient(t, mux)
	defer srv.Close()

	o := c.RepoObserver("o", "r")

	pr, err := o.GetPullRequest(t.Context(), 5)
	if err != nil || !pr.Merged || pr.BaseRef != "main" || pr.AuthorID != 3 {
		t.Errorf("GetPullRequest = %+v, %v; want merged into main by user 3", pr, err)
	}

	commits, err := o.ListCommits(t.Context(), "rg/branch")
	if err != nil || len(commits) != 2 {
		t.Fatalf("ListCommits = %v, %v; want two commits", commits, err)
	}

	if c := commits[0]; c.SHA != "c2" || !c.Verified || c.AuthorID != 9 || c.CommitterLogin != "web-flow" {
		t.Errorf("ListCommits[0] = %+v, want the verified bot commit c2", c)
	}
}

func TestRepoObserver_GetRef(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/repos/o/r/git/ref/heads/present", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ref":"refs/heads/present","object":{"sha":"abc"}}`))
	})
	mux.HandleFunc("GET /api/v3/repos/o/r/git/ref/heads/absent", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	c, srv := newTestClient(t, mux)
	defer srv.Close()

	o := c.RepoObserver("o", "r")

	if sha, ok, err := o.GetRef(t.Context(), "present"); sha != "abc" || !ok || err != nil {
		t.Errorf("GetRef(present) = %q, %v, %v; want abc, true, nil", sha, ok, err)
	}

	if sha, ok, err := o.GetRef(t.Context(), "absent"); sha != "" || ok || err != nil {
		t.Errorf("GetRef(absent) = %q, %v, %v; want \"\", false, nil", sha, ok, err)
	}
}

package github

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/control"
)

func TestRepoReader_GetContents_PinnedAndCached(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/repos/o/r/contents/CODEOWNERS", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)

		if got := r.URL.Query().Get("ref"); got != "abc123" {
			t.Errorf("ref = %q, want abc123", got)
		}

		_ = json.NewEncoder(w).Encode(map[string]string{
			"type": "file", "encoding": "base64", "sha": "blob1",
			"content": base64.StdEncoding.EncodeToString([]byte("* @team\n")),
		})
	})
	mux.HandleFunc("GET /api/v3/repos/o/r/contents/missing", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	c, srv := newTestClient(t, mux)
	defer srv.Close()

	r := c.RepoReader("o", "r", "abc123")

	for range 2 {
		got, sha, found, err := r.GetContents(t.Context(), "CODEOWNERS")
		if err != nil || !found || sha != "blob1" || string(got) != "* @team\n" {
			t.Fatalf("GetContents(CODEOWNERS) = %q, %q, %v, %v; want the file", got, sha, found, err)
		}
	}

	if n := calls.Load(); n != 1 {
		t.Errorf("GetContents calls = %d, want 1 (cached)", n)
	}

	if _, _, found, err := r.GetContents(t.Context(), "missing"); found || err != nil {
		t.Errorf("GetContents(missing) found = %v, err = %v; want false, nil", found, err)
	}
}

func TestRepoReader_ListDirectory(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/repos/o/r/contents/.github/workflows", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"name":"z.yml","type":"file"},{"name":"a.yml","type":"file"}]`))
	})
	mux.HandleFunc("GET /api/v3/repos/o/r/contents/nope", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	c, srv := newTestClient(t, mux)
	defer srv.Close()

	r := c.RepoReader("o", "r", "")

	got, err := r.ListDirectory(t.Context(), ".github/workflows")
	if err != nil || fmt.Sprint(got) != "[a.yml z.yml]" {
		t.Errorf("ListDirectory = %v, %v; want [a.yml z.yml]", got, err)
	}

	if got, err := r.ListDirectory(t.Context(), "nope"); err != nil || len(got) != 0 {
		t.Errorf("ListDirectory(nope) = %v, %v; want empty", got, err)
	}
}

// TestRepoReader_GetRepository_MissingFieldsAreNil pins INV-0022 spike 3:
// a field the response lacks is unknown, never false.
func TestRepoReader_GetRepository_MissingFieldsAreNil(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/graphql", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"repository":{"squashMergeAllowed":false,"deleteBranchOnMerge":true,` +
			`"mergeCommitAllowed":null}}}`))
	})
	mux.HandleFunc("GET /api/v3/repos/o/r", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"default_branch":"main","security_and_analysis":{"secret_scanning":{"status":"enabled"}}}`))
	})

	c, srv := newTestClient(t, mux)
	defer srv.Close()

	got, err := c.RepoReader("o", "r", "").GetRepository(t.Context())
	if err != nil {
		t.Fatalf("GetRepository = %v", err)
	}

	if got.AllowSquashMerge == nil || *got.AllowSquashMerge {
		t.Errorf("AllowSquashMerge = %v, want false", got.AllowSquashMerge)
	}

	if got.DeleteBranchOnMerge == nil || !*got.DeleteBranchOnMerge {
		t.Errorf("DeleteBranchOnMerge = %v, want true", got.DeleteBranchOnMerge)
	}

	if got.AllowMergeCommit != nil || got.AllowAutoMerge != nil {
		t.Errorf("AllowMergeCommit = %v, AllowAutoMerge = %v; want nil (unknown)", got.AllowMergeCommit, got.AllowAutoMerge)
	}

	if got.DefaultBranch == nil || *got.DefaultBranch != "main" {
		t.Errorf("DefaultBranch = %v, want main", got.DefaultBranch)
	}

	if want := map[string]string{"secret_scanning": "enabled"}; fmt.Sprint(got.SecurityAndAnalysis) != fmt.Sprint(want) {
		t.Errorf("SecurityAndAnalysis = %v, want %v", got.SecurityAndAnalysis, want)
	}
}

func TestRepoReader_GetRepository_NoSecurityBlockIsNil(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/graphql", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"repository":{}}}`))
	})
	mux.HandleFunc("GET /api/v3/repos/o/r", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"default_branch":"main"}`))
	})

	c, srv := newTestClient(t, mux)
	defer srv.Close()

	got, err := c.RepoReader("o", "r", "").GetRepository(t.Context())
	if err != nil || got.SecurityAndAnalysis != nil {
		t.Errorf("GetRepository = %v, %v; want a nil SecurityAndAnalysis", got.SecurityAndAnalysis, err)
	}
}

// TestRepoReader_ListRulesets_PaginatesAndIncludesParents serves 35
// rulesets over pages of 30 (the server ignores per_page), one inherited
// from the org, and checks every one is fetched by id with its rules.
func TestRepoReader_ListRulesets_PaginatesAndIncludesParents(t *testing.T) {
	t.Parallel()

	const total, pageSize = 35, 30

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/repos/o/r/rulesets", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("includes_parents") != "true" {
			t.Error("list without includes_parents=true")
		}

		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		start := (page - 1) * pageSize

		var batch []map[string]any
		for id := start + 1; id <= min(start+pageSize, total); id++ {
			batch = append(batch, map[string]any{"id": id, "name": fmt.Sprint("rs", id)})
		}

		if start+pageSize < total {
			w.Header().Set("Link", fmt.Sprintf(`<%s?page=%d>; rel="next"`, r.URL.Path, page+1))
		}

		_ = json.NewEncoder(w).Encode(batch)
	})
	mux.HandleFunc("GET /api/v3/repos/o/r/rulesets/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.Atoi(r.PathValue("id"))

		source := "Repository"
		if id == 1 {
			source = "Organization"
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": id, "name": fmt.Sprint("rs", id), "source_type": source, "enforcement": "active",
			"target": "branch", "rules": []map[string]string{{"type": "deletion"}},
		})
	})

	c, srv := newTestClient(t, mux)
	defer srv.Close()

	got, err := c.RepoReader("o", "r", "").ListRulesets(t.Context())
	if err != nil {
		t.Fatalf("ListRulesets = %v", err)
	}

	if len(got) != total {
		t.Fatalf("ListRulesets returned %d rulesets, want %d", len(got), total)
	}

	if got[0].Source != control.RulesetSourceOrganization || got[1].Source != control.RulesetSourceRepository {
		t.Errorf("sources = %q, %q; want organization, repository", got[0].Source, got[1].Source)
	}

	if string(got[34].Rules) != `[{"type":"deletion"}]` {
		t.Errorf("Rules = %s, want the fetched rules", got[34].Rules)
	}
}

func TestRepoReader_PropertiesAndLabels(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/repos/o/r/properties/values", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"property_name":"Owner","value":"team"},{"property_name":"Tier","value":null}]`))
	})
	mux.HandleFunc("GET /api/v3/repos/o/r/labels", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"name":"bug","color":"d73a4a","description":"broken"}]`))
	})

	c, srv := newTestClient(t, mux)
	defer srv.Close()

	r := c.RepoReader("o", "r", "")

	props, err := r.GetCustomProperties(t.Context())
	if err != nil || props["Owner"] == nil || *props["Owner"] != "team" || props["Tier"] != nil {
		t.Errorf("GetCustomProperties = %v, %v; want Owner=team, Tier=nil", props, err)
	}

	labels, err := r.ListLabels(t.Context())
	if err != nil || len(labels) != 1 || labels[0] != (control.Label{Name: "bug", Color: "d73a4a", Description: "broken"}) {
		t.Errorf("ListLabels = %v, %v; want the bug label", labels, err)
	}
}

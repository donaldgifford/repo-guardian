package write_test

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/control"
	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/github/write"
)

func newWriter(t *testing.T, mux *http.ServeMux) *write.Writer {
	t.Helper()

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c, err := ghclient.NewClientForBaseURL(srv.URL+"/api/v3", srv.Client().Transport, slog.Default(), 0.10)
	if err != nil {
		t.Fatalf("NewClientForBaseURL = %v", err)
	}

	return write.New(c, "o", "r")
}

func TestCommit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		response string
		wantSHA  string
		wantErr  error
	}{
		{
			name:     "committed",
			response: `{"data":{"createCommitOnBranch":{"commit":{"oid":"new123"}}}}`,
			wantSHA:  "new123",
		},
		{
			// INV-0022 spike 1: a stale head is a 200 with STALE_DATA.
			name: "stale head",
			response: `{"data":{"createCommitOnBranch":null},"errors":[{"type":"STALE_DATA",` +
				`"path":["createCommitOnBranch"],"message":"Expected branch to point to \"abc\" but it did not.  Pull and try again."}]}`,
			wantErr: control.ErrExpectedHeadMismatch,
		},
		{
			name:     "other graphql error",
			response: `{"data":{"createCommitOnBranch":null},"errors":[{"type":"NOT_FOUND","message":"no branch"}]}`,
			wantErr:  errAny,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var input map[string]any

			mux := http.NewServeMux()
			mux.HandleFunc("POST /api/graphql", func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Variables struct {
						Input map[string]any `json:"input"`
					} `json:"variables"`
				}

				body, _ := io.ReadAll(r.Body)
				if err := json.Unmarshal(body, &req); err != nil {
					t.Errorf("decoding request: %v", err)
				}

				input = req.Variables.Input

				_, _ = io.WriteString(w, tt.response)
			})

			sha, err := newWriter(t, mux).Commit(t.Context(), "rg/x", "base1", []control.FileChange{
				{Path: "CODEOWNERS", Content: []byte("* @team\n")},
				{Path: "old.txt", Delete: true},
			}, "add CODEOWNERS\n\nbecause")

			switch {
			case tt.wantErr == nil && err != nil:
				t.Fatalf("Commit = %v, want nil", err)
			case errors.Is(tt.wantErr, errAny) && err == nil:
				t.Fatal("Commit = nil, want an error")
			case errors.Is(tt.wantErr, errAny) && errors.Is(err, control.ErrExpectedHeadMismatch):
				t.Fatalf("Commit = %v, want an error that is not ErrExpectedHeadMismatch", err)
			case tt.wantErr != nil && !errors.Is(tt.wantErr, errAny) && !errors.Is(err, tt.wantErr):
				t.Fatalf("Commit = %v, want %v", err, tt.wantErr)
			}

			if sha != tt.wantSHA {
				t.Errorf("Commit sha = %q, want %q", sha, tt.wantSHA)
			}

			if input["expectedHeadOid"] != "base1" {
				t.Errorf("expectedHeadOid = %v, want base1", input["expectedHeadOid"])
			}

			fc, _ := json.Marshal(input["fileChanges"])
			if want := `{"additions":[{"contents":"KiBAdGVhbQo=","path":"CODEOWNERS"}],"deletions":[{"path":"old.txt"}]}`; string(fc) != want {
				t.Errorf("fileChanges = %s, want %s", fc, want)
			}
		})
	}
}

// errAny marks a case that wants some error, not a particular one.
var errAny = errors.New("any error")

func TestCommit_Bounds(t *testing.T) {
	t.Parallel()

	w := newWriter(t, http.NewServeMux())

	tooMany := make([]control.FileChange, 101)
	for i := range tooMany {
		tooMany[i] = control.FileChange{Path: strings.Repeat("a", i+1)}
	}

	for name, changes := range map[string][]control.FileChange{
		"empty":     nil,
		"too many":  tooMany,
		"too large": {{Path: "big", Content: make([]byte, 1<<20+1)}},
		"duplicate": {{Path: "a"}, {Path: "a", Delete: true}},
	} {
		if _, err := w.Commit(t.Context(), "b", "s", changes, "m"); err == nil {
			t.Errorf("Commit(%s) = nil, want a refusal before any request", name)
		}
	}
}

// TestUpdateBranch covers every outcome in INV-0022 spike 2: a 422 is
// classified by re-reading the PR, never by its message.
func TestUpdateBranch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		status   int
		message  string
		head     string
		behindBy int
		wantErr  error
	}{
		{name: "accepted", status: http.StatusAccepted, message: "Updating pull request branch.", wantErr: control.ErrUpdateBranchPending},
		{
			name: "head moved", status: http.StatusUnprocessableEntity, message: "expected head sha didn’t match current head ref.",
			head: "moved", wantErr: control.ErrExpectedHeadMismatch,
		},
		{
			name: "already up to date", status: http.StatusUnprocessableEntity, message: "There are no new commits on the base branch.",
			head: "head1",
		},
		{
			name: "conflict", status: http.StatusUnprocessableEntity, message: "merge conflict between base and head",
			head: "head1", behindBy: 3, wantErr: control.ErrMergeConflict,
		},
		{name: "server error", status: http.StatusInternalServerError, wantErr: errAny},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mux := http.NewServeMux()
			mux.HandleFunc("PUT /api/v3/repos/o/r/pulls/7/update-branch", func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if !strings.Contains(string(body), `"expected_head_sha":"head1"`) {
					t.Errorf("update-branch body = %s, want expected_head_sha head1", body)
				}

				w.WriteHeader(tt.status)
				_ = json.NewEncoder(w).Encode(map[string]string{"message": tt.message})
			})
			mux.HandleFunc("GET /api/v3/repos/o/r/pulls/7", func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"number": 7, "head": map[string]string{"sha": tt.head}, "base": map[string]string{"ref": "main"},
				})
			})
			mux.HandleFunc("GET /api/v3/repos/o/r/compare/main...head1", func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]int{"behind_by": tt.behindBy})
			})

			err := newWriter(t, mux).UpdateBranch(t.Context(), 7, "head1")

			switch {
			case tt.wantErr == nil && err != nil:
				t.Errorf("UpdateBranch = %v, want nil", err)
			case errors.Is(tt.wantErr, errAny):
				for _, s := range []error{control.ErrUpdateBranchPending, control.ErrExpectedHeadMismatch, control.ErrMergeConflict} {
					if err == nil || errors.Is(err, s) {
						t.Errorf("UpdateBranch = %v, want a plain error", err)
					}
				}
			case tt.wantErr != nil && !errors.Is(err, tt.wantErr):
				t.Errorf("UpdateBranch = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestCreateRef_ExistingBranchFails(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v3/repos/o/r/git/refs", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(w, `{"message":"Reference already exists"}`)
	})

	if err := newWriter(t, mux).CreateRef(t.Context(), "rg/x", "abc"); err == nil {
		t.Error("CreateRef on an existing branch = nil, want an error")
	}
}

func TestUpsertRuleset(t *testing.T) {
	t.Parallel()

	var calls []string

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/o/r/rulesets", func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		_, _ = io.WriteString(w, `{}`)
	})
	mux.HandleFunc("/api/v3/repos/o/r/rulesets/9", func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		_, _ = io.WriteString(w, `{}`)
	})

	w := newWriter(t, mux)
	rules := json.RawMessage(`[{"type":"deletion"}]`)

	if err := w.UpsertRuleset(t.Context(), control.Ruleset{Name: "new", Enforcement: "active", Rules: rules}); err != nil {
		t.Fatalf("create = %v", err)
	}

	if err := w.UpsertRuleset(t.Context(), control.Ruleset{ID: 9, Name: "x", Source: control.RulesetSourceRepository}); err != nil {
		t.Fatalf("update = %v", err)
	}

	if err := w.UpsertRuleset(t.Context(), control.Ruleset{ID: 10, Name: "org", Source: control.RulesetSourceOrganization}); err == nil {
		t.Error("updating an org ruleset = nil, want a refusal")
	}

	if want := "[POST /api/v3/repos/o/r/rulesets PUT /api/v3/repos/o/r/rulesets/9]"; strings.Join(calls, " ") != strings.Trim(want, "[]") {
		t.Errorf("calls = %v, want %s", calls, want)
	}
}

func TestUpsertLabel_MatchesCaseInsensitively(t *testing.T) {
	t.Parallel()

	var edited, created bool

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/repos/o/r/labels", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `[{"name":"Bug","color":"000000"}]`)
	})
	mux.HandleFunc("PATCH /api/v3/repos/o/r/labels/Bug", func(w http.ResponseWriter, _ *http.Request) {
		edited = true
		_, _ = io.WriteString(w, `{}`)
	})
	mux.HandleFunc("POST /api/v3/repos/o/r/labels", func(w http.ResponseWriter, _ *http.Request) {
		created = true
		_, _ = io.WriteString(w, `{}`)
	})

	if err := newWriter(t, mux).UpsertLabel(t.Context(), control.Label{Name: "bug", Color: "d73a4a"}); err != nil {
		t.Fatalf("UpsertLabel = %v", err)
	}

	if !edited || created {
		t.Errorf("edited = %v, created = %v; want the existing Bug label edited", edited, created)
	}
}

func TestUpdateRepository_WritesOnlySetFields(t *testing.T) {
	t.Parallel()

	var body map[string]any

	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/v3/repos/o/r", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = io.WriteString(w, `{}`)
	})

	off := false
	settings := control.RepositorySettings{
		AllowMergeCommit:    &off,
		SecurityAndAnalysis: map[string]string{"secret_scanning": "enabled"},
	}

	if err := newWriter(t, mux).UpdateRepository(t.Context(), settings); err != nil {
		t.Fatalf("UpdateRepository = %v", err)
	}

	got, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("Marshal = %v", err)
	}

	if want := `{"allow_merge_commit":false,"security_and_analysis":{"secret_scanning":{"status":"enabled"}}}`; string(got) != want {
		t.Errorf("PATCH body = %s, want %s", got, want)
	}
}

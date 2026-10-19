//go:build integration

package postgres_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"

	apispec "github.com/donaldgifford/repo-guardian/api"
	"github.com/donaldgifford/repo-guardian/internal/api"
	"github.com/donaldgifford/repo-guardian/internal/api/apitest"
	"github.com/donaldgifford/repo-guardian/internal/api/gen"
	"github.com/donaldgifford/repo-guardian/internal/api/oidctest"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres"
)

const endpointsAudience = "repo-guardian-api"

// endpoints is the API with OIDC auth over a seeded database: acme
// (widgets, compliant), acme-web (b, failing with an open PR) and
// acme-pay (c compliant, d archived), plus a snapshot.
type endpoints struct {
	f      *apiFixture
	c      *apitest.Client
	issuer *oidctest.Issuer
	ids    map[string]int64
}

func newEndpoints(t *testing.T) *endpoints {
	t.Helper()

	f := newAPIFixture(t)
	ids := seedTwoOrgs(t, f)

	ctx := t.Context()
	if _, err := f.store.InsertComplianceSnapshot(ctx, t0.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	iss := oidctest.Start(t, endpointsAudience)

	path := filepath.Join(t.TempDir(), "authz.yaml")
	if err := os.WriteFile(path, []byte("groups:\n  platform: [\"*\"]\n  team-web: [\"acme-web\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	authz, err := api.LoadAuthzConfig(path)
	if err != nil {
		t.Fatal(err)
	}

	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	authn := api.NewAuthenticator(&api.AuthnConfig{
		Issuer: iss.URL, Audience: endpointsAudience, NameClaim: "preferred_username", GroupsClaim: "groups",
	}, quiet)
	authn.Start(ctx)

	pool, err := postgres.NewReadOnlyPool(ctx, f.dsns.ReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	e := &endpoints{f: f, issuer: iss, ids: ids, c: apitest.New(t, &api.Options{
		Reader: postgres.NewAPIReader(pool), Authn: authn, Authz: authz, Logger: quiet,
		Now: func() time.Time { return t0 },
	})}

	return e
}

func (e *endpoints) get(t *testing.T, path, group string) *apitest.Response {
	t.Helper()

	return e.c.Get(path, e.issuer.Valid(t, group))
}

func (e *endpoints) repoPath(name, suffix string) string {
	return "/repositories/" + strconv.FormatInt(e.ids[name], 10) + suffix
}

func decodeAs[T any](t *testing.T, r *apitest.Response) T {
	t.Helper()

	if r.Status != http.StatusOK {
		t.Fatalf("status = %d: %s", r.Status, r.Body)
	}

	var v T
	if err := json.Unmarshal(r.Body, &v); err != nil {
		t.Fatal(err)
	}

	return v
}

// Every endpoint answers for a caller who sees everything, apitest
// validates each body against the spec, and an operation the spec adds
// without a case here fails the test.
func TestEndpoints_ValidateAgainstTheSpec(t *testing.T) {
	t.Parallel()

	e := newEndpoints(t)

	cases := []struct {
		op, method, path string
		want             int
	}{
		{"/me", http.MethodGet, "/me", http.StatusOK},
		{"/summary", http.MethodGet, "/summary", http.StatusOK},
		{"/controls", http.MethodGet, "/controls", http.StatusOK},
		{"/controls/{id}", http.MethodGet, "/controls/codeowners", http.StatusOK},
		{"/policies", http.MethodGet, "/policies", http.StatusOK},
		{"/orgs", http.MethodGet, "/orgs", http.StatusOK},
		{"/orgs/{org}", http.MethodGet, "/orgs/acme-web", http.StatusOK},
		{"/repositories", http.MethodGet, "/repositories", http.StatusOK},
		{"/repositories/{id}", http.MethodGet, e.repoPath("acme-web/b", ""), http.StatusOK},
		{"/repositories/{id}/controls", http.MethodGet, e.repoPath("acme-web/b", "/controls"), http.StatusOK},
		{"/repositories/{id}/events", http.MethodGet, e.repoPath("acme-web/b", "/events"), http.StatusOK},
		{"/repositories/{id}/evaluations", http.MethodGet, e.repoPath("acme-web/b", "/evaluations"), http.StatusOK},
		// No Temporal client in this harness: the spec's 501.
		{"/repositories/{id}/evaluate", http.MethodPost, e.repoPath("acme-web/b", "/evaluate"), http.StatusNotImplemented},
		{"/remediations", http.MethodGet, "/remediations?hold=conflict&hold=permission", http.StatusOK},
		{"/compliance/history", http.MethodGet, "/compliance/history?days=7", http.StatusOK},
		{"/status", http.MethodGet, "/status", http.StatusOK},
		{"/openapi.yaml", http.MethodGet, "/openapi.yaml", http.StatusOK},
	}

	doc, err := openapi3.NewLoader().LoadFromData(apispec.Spec)
	if err != nil {
		t.Fatal(err)
	}

	covered := map[string]bool{}

	for _, tc := range cases {
		covered[tc.method+" "+tc.op] = true

		t.Run(tc.method+" "+tc.op, func(t *testing.T) {
			var r *apitest.Response
			if tc.method == http.MethodPost {
				r = e.c.Post(tc.path, e.issuer.Valid(t, "platform"))
			} else {
				r = e.get(t, tc.path, "platform")
			}

			if r.Status != tc.want {
				t.Errorf("%s %s = %d, want %d: %s", tc.method, tc.path, r.Status, tc.want, r.Body)
			}
		})
	}

	for path, item := range doc.Paths.Map() {
		for method := range item.Operations() {
			if !covered[method+" "+path] {
				t.Errorf("%s %s has no case", method, path)
			}
		}
	}

	for _, agg := range []string{"none", "fleet", "org", "control"} {
		h := decodeAs[gen.ComplianceHistory](t, e.get(t, "/compliance/history?aggregate="+agg, "platform"))
		if len(h.Items) == 0 {
			t.Errorf("/compliance/history?aggregate=%s is empty", agg)
		}
	}

	b := decodeAs[gen.RepositoryDetail](t, e.get(t, e.repoPath("acme-web/b", ""), "platform"))
	if b.Repository.Name != "b" || b.LastEvaluation == nil {
		t.Errorf("repository b = %+v", b)
	}

	rc := decodeAs[gen.RepositoryControlList](t, e.get(t, e.repoPath("acme-web/b", "/controls"), "platform"))
	if len(rc.Items) != 1 || rc.Items[0].ControlId != "codeowners" {
		t.Errorf("repository b controls = %+v, want codeowners", rc.Items)
	}
}

// An org the caller cannot see answers exactly like one that does not
// exist, and lists never include it.
func TestEndpoints_TwoOrgAuthz(t *testing.T) {
	t.Parallel()

	e := newEndpoints(t)

	for _, path := range []string{
		e.repoPath("acme-pay/c", ""), e.repoPath("acme-pay/c", "/events"),
		e.repoPath("acme-pay/c", "/evaluations"), "/repositories/999999",
	} {
		if r := e.get(t, path, "team-web"); r.Status != http.StatusNotFound {
			t.Errorf("team-web GET %s = %d, want 404", path, r.Status)
		}
	}

	orgs := decodeAs[gen.OrgList](t, e.get(t, "/orgs", "team-web"))
	if len(orgs.Items) != 1 || orgs.Items[0].Org != "acme-web" {
		t.Errorf("team-web orgs = %+v", orgs.Items)
	}

	repos := decodeAs[gen.RepositoryPage](t, e.get(t, "/repositories", "team-web"))
	if len(repos.Items) != 1 || repos.Items[0].Org != "acme-web" {
		t.Errorf("team-web repositories = %+v", repos.Items)
	}

}

func TestEndpoints_RepositoryFilters(t *testing.T) {
	t.Parallel()

	e := newEndpoints(t)

	tests := []struct {
		query string
		want  int
	}{
		{query: "", want: 4},
		{query: "active=true", want: 3},
		{query: "active=false", want: 1},
		{query: "park_reason=archived", want: 1},
		{query: "org=acme-pay", want: 2},
		{query: "q=B", want: 1},
		{query: "q=wid", want: 1},
		{query: "q=%25", want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			page := decodeAs[gen.RepositoryPage](t, e.get(t, "/repositories?"+tt.query, "platform"))
			if len(page.Items) != tt.want {
				t.Errorf("/repositories?%s = %d items, want %d", tt.query, len(page.Items), tt.want)
			}
		})
	}
}

func TestEndpoints_BadPagingIs400(t *testing.T) {
	t.Parallel()

	e := newEndpoints(t)

	first := decodeAs[gen.RepositoryPage](t, e.get(t, "/repositories?limit=1", "platform"))
	if first.NextCursor == nil {
		t.Fatal("no next cursor with limit=1 over four repositories")
	}

	for _, path := range []string{
		"/repositories?limit=0", "/repositories?limit=201", "/repositories?cursor=not-a-cursor",
		"/repositories?limit=1&org=acme&cursor=" + url.QueryEscape(*first.NextCursor),
	} {
		if r := e.get(t, path, "platform"); r.Status != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400", path, r.Status)
		}
	}
}

// Keyset pages neither skip nor repeat rows while writes land between
// them.
func TestEndpoints_PaginationIsStableUnderWrites(t *testing.T) {
	t.Parallel()

	e := newEndpoints(t)

	want := map[int64]bool{}
	for _, id := range e.ids {
		want[id] = true
	}

	seen := map[int64]bool{}
	last := int64(0)
	next := "/repositories?limit=1"

	for i := 0; next != ""; i++ {
		page := decodeAs[gen.RepositoryPage](t, e.get(t, next, "platform"))

		for _, r := range page.Items {
			if seen[r.Id] || r.Id <= last {
				t.Fatalf("repository %d repeated or out of order after %d", r.Id, last)
			}

			seen[r.Id], last = true, r.Id
		}

		// A write between pages: a new repository.
		e.f.discover(t, "acme", "new-"+strconv.Itoa(i))

		next = ""
		if page.NextCursor != nil && i < 20 {
			next = "/repositories?limit=1&cursor=" + url.QueryEscape(*page.NextCursor)
		}
	}

	for id := range want {
		if !seen[id] {
			t.Errorf("repository %d was skipped", id)
		}
	}
}

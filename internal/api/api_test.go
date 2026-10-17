package api_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/donaldgifford/repo-guardian/internal/api"
	"github.com/donaldgifford/repo-guardian/internal/api/apitest"
	"github.com/donaldgifford/repo-guardian/internal/api/gen"
	"github.com/donaldgifford/repo-guardian/internal/api/oidctest"
	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/store"
)

const audience = "repo-guardian-api"

var (
	quiet = slog.New(slog.NewTextHandler(io.Discard, nil))
	now   = time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
)

// orgReader answers Repositories by filtering per-org fixtures over the
// scope, the way the SQL predicate does, and records each scope. The
// other reads are exercised against Postgres (apireader integration
// tests); calling one here panics on the nil embedded Reader.
type orgReader struct {
	api.Reader

	mu     sync.Mutex
	repos  []store.Repository
	scopes []store.APIScope
	policy *store.ActivePolicy
}

// Repository answers one repository within scope, ErrNotFound otherwise,
// as the SQL predicate does.
func (r *orgReader) Repository(_ context.Context, scope store.APIScope, id int64) (*store.RepositoryDetail, error) {
	for i := range r.repos {
		if r.repos[i].ID == id && (scope.All() || slices.Contains(scope.Orgs(), r.repos[i].Org)) {
			return &store.RepositoryDetail{Repository: r.repos[i]}, nil
		}
	}

	return nil, store.ErrNotFound
}

func (r *orgReader) ActivePolicy(context.Context) (*store.ActivePolicy, error) {
	if r.policy == nil {
		return nil, store.ErrNotFound
	}

	return r.policy, nil
}

func (*orgReader) Ping(context.Context) error { return nil }

func (r *orgReader) Repositories(_ context.Context, scope store.APIScope, _ *store.RepositoryFilter) (store.Page[store.Repository], error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.scopes = append(r.scopes, scope)

	var out store.Page[store.Repository]

	for i := range r.repos {
		if scope.All() || slices.Contains(scope.Orgs(), r.repos[i].Org) {
			out.Items = append(out.Items, r.repos[i])
		}
	}

	return out, nil
}

func twoOrgs() *orgReader {
	return &orgReader{repos: []store.Repository{
		{ID: 1, Org: "acme-web", Name: "site", Active: true, DiscoveredAt: now},
		{ID: 2, Org: "acme-web", Name: "cdn", Active: true, DiscoveredAt: now},
		{ID: 3, Org: "acme-pay", Name: "ledger", Active: true, DiscoveredAt: now},
	}}
}

const authzYAML = `
groups:
  platform: ["*"]
  team-web: ["acme-web"]
defaultOrgs: []
clients:
  ci-dashboard: ["team-web"]
`

// harness is an API with OIDC auth against a test issuer.
type harness struct {
	issuer *oidctest.Issuer
	reader *orgReader
	client *apitest.Client
}

func newHarness(t *testing.T, opts ...func(*api.Options)) *harness {
	t.Helper()

	iss := oidctest.Start(t, audience)

	path := filepath.Join(t.TempDir(), "authz.yaml")
	if err := os.WriteFile(path, []byte(authzYAML), 0o600); err != nil {
		t.Fatal(err)
	}

	authz, err := api.LoadAuthzConfig(path)
	if err != nil {
		t.Fatal(err)
	}

	authn := api.NewAuthenticator(&api.AuthnConfig{
		Issuer: iss.URL, Audience: audience, NameClaim: "preferred_username", GroupsClaim: "groups",
	}, quiet)
	authn.Start(t.Context())

	reader := twoOrgs()
	o := &api.Options{
		Reader: reader, Authn: authn, Authz: authz, Logger: quiet,
		Now: func() time.Time { return now },
	}

	for _, opt := range opts {
		opt(o)
	}

	return &harness{issuer: iss, reader: reader, client: apitest.New(t, o)}
}

func decode[T any](t *testing.T, r *apitest.Response) T {
	t.Helper()

	var v T
	if err := json.Unmarshal(r.Body, &v); err != nil {
		t.Fatalf("decode %s: %v", r.Body, err)
	}

	return v
}

func TestMe_ReportsVisibleOrgs(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	tests := []struct {
		name    string
		token   string
		wantAll bool
		want    []string
	}{
		{"platform sees every org", h.issuer.Valid(t, "platform"), true, []string{}},
		{"team sees its orgs", h.issuer.Valid(t, "team-web", "unknown-group"), false, []string{"acme-web"}},
		{"no matching group sees nothing", h.issuer.Valid(t, "unknown-group"), false, []string{}},
		{"no groups claim sees nothing", h.issuer.NoGroups(t), false, []string{}},
		{"machine client maps through azp", h.issuer.Mint(t, &oidctest.Token{Client: "ci-dashboard"}), false, []string{"acme-web"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			resp := h.client.Get("/me", tt.token)
			if resp.Status != http.StatusOK {
				t.Fatalf("status = %d: %s", resp.Status, resp.Body)
			}

			me := decode[gen.Me](t, resp)
			if me.AllOrgs != tt.wantAll || !slices.Equal(me.Orgs, tt.want) || me.Subject != "user-1" {
				t.Errorf("me = %+v, want all=%v orgs=%v", me, tt.wantAll, tt.want)
			}
		})
	}
}

func TestRepositories_AreScopedToVisibleOrgs(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	orgsOf := func(group string) []string {
		page := decode[gen.RepositoryPage](t, h.client.Get("/repositories", h.issuer.Valid(t, group)))

		orgs := make([]string, 0, len(page.Items))
		for _, r := range page.Items {
			orgs = append(orgs, r.Org)
		}

		return orgs
	}

	if got := orgsOf("team-web"); !slices.Equal(got, []string{"acme-web", "acme-web"}) {
		t.Errorf("team-web repositories = %v, want acme-web only", got)
	}

	if got := orgsOf("platform"); len(got) != 3 {
		t.Errorf("platform repositories = %v, want both orgs", got)
	}
}

func TestRepositories_NoVisibleOrgsIsForbiddenButMeAnswers(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	token := h.issuer.Valid(t, "unknown-group")

	if resp := h.client.Get("/repositories", token); resp.Status != http.StatusForbidden {
		t.Errorf("repositories = %d, want 403", resp.Status)
	}

	if len(h.reader.scopes) != 0 {
		t.Errorf("reader was queried with %v for a principal with no orgs", h.reader.scopes)
	}

	if resp := h.client.Get("/me", token); resp.Status != http.StatusOK {
		t.Errorf("me = %d, want 200", resp.Status)
	}
}

// Every token rejection is a 401 with a Bearer challenge and increments
// api_auth_failures_total with its own reason.
func TestAuthn_RejectionsAre401WithReason(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	tests := []struct {
		token, reason string
	}{
		{"", "missing"},
		{"not-a-jwt", "malformed"},
		{h.issuer.Expired(t), "expired"},
		{h.issuer.WrongAudience(t), "audience"},
		{h.issuer.WrongIssuer(t), "issuer"},
		{h.issuer.BadSignature(t), "signature"},
		{h.issuer.IDTokenShaped(t), "id_token"},
		{h.issuer.Mint(t, &oidctest.Token{HMAC: true}), "algorithm"},
		{h.issuer.Mint(t, &oidctest.Token{NotBefore: time.Now().Add(time.Hour)}), "not_yet_valid"},
	}

	for _, tt := range tests {
		before := testutil.ToFloat64(metrics.APIAuthFailuresTotal.WithLabelValues(tt.reason))

		resp := h.client.Get("/me", tt.token)
		if resp.Status != http.StatusUnauthorized || !strings.HasPrefix(resp.Header.Get("WWW-Authenticate"), "Bearer") {
			t.Errorf("%s: status %d, WWW-Authenticate %q; want 401 with a Bearer challenge", tt.reason, resp.Status,
				resp.Header.Get("WWW-Authenticate"))
		}

		if got := testutil.ToFloat64(metrics.APIAuthFailuresTotal.WithLabelValues(tt.reason)) - before; got < 1 {
			t.Errorf("%s: api_auth_failures_total{reason=%q} did not increment", tt.reason, tt.reason)
		}
	}
}

// Tokens within the 60s skew are accepted.
func TestAuthn_ClockSkew(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	if resp := h.client.Get("/me", h.issuer.Mint(t, &oidctest.Token{Expiry: time.Now().Add(-30 * time.Second)})); resp.Status != http.StatusOK {
		t.Errorf("token expired 30s ago = %d, want accepted within the 60s skew", resp.Status)
	}
}

func TestPublicRoutesNeedNoToken(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	if resp := h.client.Get("/status", ""); resp.Status != http.StatusOK {
		t.Errorf("status = %d", resp.Status)
	}

	resp := h.client.Get("/openapi.yaml", "")
	if resp.Status != http.StatusOK || !strings.HasPrefix(string(resp.Body), "openapi: 3.1.0") {
		t.Errorf("openapi.yaml = %d, %.40s", resp.Status, resp.Body)
	}
}

func TestAuthDisabled_GrantsEveryOrg(t *testing.T) {
	t.Parallel()

	c := apitest.New(t, &api.Options{Reader: twoOrgs(), Logger: quiet})

	me := decode[gen.Me](t, c.Get("/me", ""))
	if !me.AllOrgs || me.Subject != "anonymous" {
		t.Errorf("me = %+v, want the anonymous all-orgs principal", me)
	}
}

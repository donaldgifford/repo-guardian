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

	authn := api.NewAuthenticator(iss.AuthnConfig(), quiet)
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

// summaryReader answers Summary with a fixed view.
type summaryReader struct {
	*orgReader

	view store.SummaryView
}

func (r *summaryReader) Summary(context.Context, store.APIScope, time.Time) (*store.SummaryView, error) {
	return &r.view, nil
}

// TestSummary_ParkedByReasonAndControlsFailing is IMPL-0030 7.9: both
// computed from the view /summary already reads, validated against the
// spec by apitest.
func TestSummary_ParkedByReasonAndControlsFailing(t *testing.T) {
	t.Parallel()

	r := &summaryReader{orgReader: twoOrgs(), view: store.SummaryView{
		Repositories: []store.OrgRepositoryCount{
			{Org: "acme-web", Active: true, Count: 5},
			{Org: "acme-web", ParkReason: store.ParkArchived, Count: 2},
			{Org: "acme-pay", ParkReason: store.ParkArchived, Count: 1},
			{Org: "acme-pay", ParkReason: store.ParkAccessDenied, Count: 3},
		},
		Compliance: []store.ControlPosture{
			{Org: "acme-web", ControlID: "codeowners", Decision: "baseline", NonCompliant: 2, Assigned: 2},
			{Org: "acme-pay", ControlID: "codeowners", Decision: "baseline", NonCompliant: 1, Assigned: 1},
			{Org: "acme-pay", ControlID: "labels", Decision: "org_added", Compliant: 4, Assigned: 4},
			{Org: "acme-pay", ControlID: "branch-ruleset", Decision: "baseline", NonCompliant: 1, Assigned: 1},
		},
	}}

	h := newHarness(t, func(o *api.Options) { o.Reader = r })

	s := decode[gen.Summary](t, h.client.Get("/summary", h.issuer.Valid(t, "platform")))

	want := []gen.ReasonCount{{Reason: "access_denied", Count: 3}, {Reason: "archived", Count: 3}}
	if !slices.Equal(s.ParkedByReason, want) || s.Parked != 6 {
		t.Errorf("parked_by_reason = %+v (parked %d), want %+v", s.ParkedByReason, s.Parked, want)
	}

	if s.ControlsFailing != 2 {
		t.Errorf("controls_failing = %d, want codeowners and branch-ruleset", s.ControlsFailing)
	}
}

// claimsRecorder is a minimal http.ResponseWriter: depguard keeps
// net/http/httptest out of this package's tests.
type claimsRecorder struct {
	header http.Header
	status int
}

func (r *claimsRecorder) Header() http.Header       { return r.header }
func (*claimsRecorder) Write(b []byte) (int, error) { return len(b), nil }
func (r *claimsRecorder) WriteHeader(status int)    { r.status = status }

// verifiedClaims runs token through an Authenticator built from cfg and
// returns the claims the middleware put on the request.
func verifiedClaims(t *testing.T, cfg *api.AuthnConfig, token string) *api.Claims {
	t.Helper()

	authn := api.NewAuthenticator(cfg, quiet)
	authn.Start(t.Context())

	var got *api.Claims

	h := authn.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, _ = api.ClaimsFrom(r.Context())
	}))

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/me", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Authorization", "Bearer "+token)

	rec := &claimsRecorder{header: http.Header{}}
	h.ServeHTTP(rec, req)

	if got == nil {
		t.Fatalf("token refused: status %d", rec.status)
	}

	return got
}

// TestAuthn_ProviderClaimShapes pins how each provider's token shape maps
// onto Claims under the claim names an operator configures (IMPL-0033
// 1.4, DESIGN-0038 B3).
func TestAuthn_ProviderClaimShapes(t *testing.T) {
	t.Parallel()

	iss := oidctest.Start(t, audience)

	tests := []struct {
		name                                string
		clientClaim, groupsClaim, nameClaim string
		token                               *oidctest.Token
		wantClient, wantName                string
		wantGroups                          []string
	}{
		{
			name:        "keycloak: client in azp",
			clientClaim: "azp", groupsClaim: "groups", nameClaim: "preferred_username",
			token:      &oidctest.Token{Client: "ci-dashboard", Groups: []string{"team-web"}},
			wantClient: "ci-dashboard", wantName: "Test User", wantGroups: []string{"team-web"},
		},
		{
			name:        "okta: client in cid",
			clientClaim: "cid", groupsClaim: "groups", nameClaim: "sub",
			token:      oidctest.Okta("0oa1ci-dashboard", []string{"team-web"}),
			wantClient: "0oa1ci-dashboard", wantName: "user-1", wantGroups: []string{"team-web"},
		},
		{
			name:        "entra: client in appid, app roles as groups",
			clientClaim: "appid", groupsClaim: "roles", nameClaim: "preferred_username",
			token:      oidctest.Entra("11111111-2222-3333-4444-555555555555", []string{"Guardian.Read"}),
			wantClient: "11111111-2222-3333-4444-555555555555", wantName: "Test User", wantGroups: []string{"Guardian.Read"},
		},
		{
			name:        "auth0: namespaced groups claim",
			clientClaim: "azp", groupsClaim: "https://example.com/groups", nameClaim: "preferred_username",
			token:    oidctest.Namespaced([]string{"platform"}),
			wantName: "Test User", wantGroups: []string{"platform"},
		},
		{
			name:        "a claim the token lacks yields no client",
			clientClaim: "cid", groupsClaim: "groups", nameClaim: "preferred_username",
			token:    &oidctest.Token{Client: "ci-dashboard"},
			wantName: "Test User", wantGroups: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := iss.AuthnConfig()
			cfg.ClientClaim, cfg.GroupsClaim, cfg.NameClaim = tt.clientClaim, tt.groupsClaim, tt.nameClaim

			got := verifiedClaims(t, cfg, iss.Mint(t, tt.token))
			if got.Client != tt.wantClient || got.Name != tt.wantName || !slices.Equal(got.Groups, tt.wantGroups) {
				t.Errorf("claims = {Client:%q Name:%q Groups:%v}, want {Client:%q Name:%q Groups:%v}",
					got.Client, got.Name, got.Groups, tt.wantClient, tt.wantName, tt.wantGroups)
			}
		})
	}
}

// TestAuthz_UnconfiguredClientClaimGrantsNothing is the property the
// clients warning relies on: with ClientClaim pointed at a claim the
// token does not carry, a mapped machine client gets no orgs.
func TestAuthz_UnconfiguredClientClaimGrantsNothing(t *testing.T) {
	t.Parallel()

	iss := oidctest.Start(t, audience)

	path := filepath.Join(t.TempDir(), "authz.yaml")
	if err := os.WriteFile(path, []byte(authzYAML), 0o600); err != nil {
		t.Fatal(err)
	}

	authz, err := api.LoadAuthzConfig(path)
	if err != nil {
		t.Fatal(err)
	}

	token := iss.Mint(t, &oidctest.Token{Client: "ci-dashboard", NoGroups: true})

	if s := authz.Scope(verifiedClaims(t, iss.AuthnConfig(), token)); s.Empty() {
		t.Fatal("azp-configured scope is empty, want the ci-dashboard mapping")
	}

	cfg := iss.AuthnConfig()
	cfg.ClientClaim = "cid"

	if s := authz.Scope(verifiedClaims(t, cfg, token)); !s.Empty() {
		t.Errorf("cid-configured scope = %v, want empty", s.Orgs())
	}
}

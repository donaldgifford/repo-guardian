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

// orgReader answers Summary by aggregating per-org fixtures over the
// scope, the way the SQL predicate does, and records each scope. The
// other reads are exercised against Postgres (apireader integration
// tests); calling one here panics on the nil embedded Reader.
type orgReader struct {
	api.Reader

	mu     sync.Mutex
	orgs   map[string]*store.Summary
	scopes []store.APIScope
}

func (*orgReader) Ping(context.Context) error { return nil }

func (r *orgReader) Summary(_ context.Context, scope store.APIScope) (*store.Summary, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.scopes = append(r.scopes, scope)
	out := &store.Summary{Parked: []store.ParkedCount{}, OpenPRs: []time.Time{}}

	for org, s := range r.orgs {
		if !scope.All() && !slices.Contains(scope.Orgs(), org) {
			continue
		}

		out.Tracked += s.Tracked
		out.Findings.Compliant += s.Findings.Compliant
		out.Findings.NonCompliant += s.Findings.NonCompliant
		out.Parked = append(out.Parked, s.Parked...)
		out.OpenPRs = append(out.OpenPRs, s.OpenPRs...)
	}

	if d := out.Findings.Compliant + out.Findings.NonCompliant; d > 0 {
		p := float64(out.Findings.Compliant*1000/d) / 10
		out.CompliantPercent = &p
	}

	return out, nil
}

func twoOrgs() *orgReader {
	return &orgReader{orgs: map[string]*store.Summary{
		"acme-web": {
			Tracked: 3, Findings: store.StatusCounts{Compliant: 2, NonCompliant: 1},
			OpenPRs: []time.Time{now.Add(-2 * time.Hour), now.Add(-40 * 24 * time.Hour)},
		},
		"acme-pay": {
			Tracked: 5, Findings: store.StatusCounts{Compliant: 5},
			Parked: []store.ParkedCount{{Reason: store.ParkArchived, Count: 2}},
		},
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

func newHarness(t *testing.T) *harness {
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

	return &harness{issuer: iss, reader: reader, client: apitest.New(t, &api.Options{
		Reader: reader, Authn: authn, Authz: authz, StaleAfter: 30 * 24 * time.Hour, Logger: quiet,
		Now: func() time.Time { return now },
	})}
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

func TestSummary_IsScopedToVisibleOrgs(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	web := decode[gen.Summary](t, h.client.Get("/summary", h.issuer.Valid(t, "team-web")))
	if web.Tracked != 3 || web.Findings.Compliant != 2 || len(web.Parked) != 0 {
		t.Errorf("team-web summary = %+v, want acme-web only", web)
	}

	if web.CompliantPercent == nil || *web.CompliantPercent != 66.6 {
		t.Errorf("compliant_percent = %v, want the floored 66.6", web.CompliantPercent)
	}

	if web.StalePrs != 1 || web.OpenPrs[0].Count != 1 || web.OpenPrs[3].Count != 1 || web.StaleAfter != "720h0m0s" {
		t.Errorf("PRs = %+v stale %d after %s", web.OpenPrs, web.StalePrs, web.StaleAfter)
	}

	all := decode[gen.Summary](t, h.client.Get("/summary", h.issuer.Valid(t, "platform")))
	if all.Tracked != 8 || len(all.Parked) != 1 {
		t.Errorf("platform summary = %+v, want both orgs", all)
	}

	override := decode[gen.Summary](t, h.client.Get("/summary?stale_after=1h", h.issuer.Valid(t, "team-web")))
	if override.StalePrs != 2 {
		t.Errorf("stale_prs with stale_after=1h = %d, want 2", override.StalePrs)
	}
}

func TestSummary_NoVisibleOrgsIsForbiddenButMeAnswers(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	token := h.issuer.Valid(t, "unknown-group")

	if resp := h.client.Get("/summary", token); resp.Status != http.StatusForbidden {
		t.Errorf("summary = %d, want 403", resp.Status)
	}

	if len(h.reader.scopes) != 0 {
		t.Errorf("reader was queried with %v for a principal with no orgs", h.reader.scopes)
	}

	if resp := h.client.Get("/me", token); resp.Status != http.StatusOK {
		t.Errorf("me = %d, want 200", resp.Status)
	}
}

func TestSummary_StaleAfterIsBounded(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	token := h.issuer.Valid(t, "platform")

	tests := []struct {
		value string
		want  int
	}{
		{value: "0h", want: http.StatusBadRequest},
		{value: "59m", want: http.StatusBadRequest},
		{value: "1h", want: http.StatusOK},
		{value: "8760h", want: http.StatusOK},
		{value: "8761h", want: http.StatusBadRequest},
	}

	for _, tt := range tests {
		if resp := h.client.Get("/summary?stale_after="+tt.value, token); resp.Status != tt.want {
			t.Errorf("stale_after=%s: status = %d, want %d", tt.value, resp.Status, tt.want)
		}
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

	c := apitest.New(t, &api.Options{Reader: twoOrgs(), Logger: quiet, StaleAfter: time.Hour})

	me := decode[gen.Me](t, c.Get("/me", ""))
	if !me.AllOrgs || me.Subject != "anonymous" {
		t.Errorf("me = %+v, want the anonymous all-orgs principal", me)
	}
}

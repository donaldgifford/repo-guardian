//go:build integration

package postgres_test

import (
	"context"
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

	"github.com/donaldgifford/repo-guardian/internal/api"
	"github.com/donaldgifford/repo-guardian/internal/api/apitest"
	"github.com/donaldgifford/repo-guardian/internal/api/gen"
	"github.com/donaldgifford/repo-guardian/internal/api/oidctest"
	"github.com/donaldgifford/repo-guardian/internal/findings"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres"
)

const endpointsAudience = "repo-guardian-api"

// endpoints is the API with OIDC auth over a seeded database: acme
// (widgets, compliant), acme-web (b, failing with a 40-day-old PR) and
// acme-pay (c compliant, d parked), plus a snapshot and a policy.
type endpoints struct {
	f      *v2Fixture
	c      *apitest.Client
	issuer *oidctest.Issuer
	ids    map[string]int64
}

func newEndpoints(t *testing.T) *endpoints {
	t.Helper()

	f := newV2Fixture(t)
	seedTwoOrgs(t, f)

	ctx := t.Context()
	if _, err := f.store.InsertComplianceSnapshot(ctx, t0.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	if _, err := f.store.RecordPolicyVersion(ctx, "v2:test", store.PolicySummary{Rules: []store.PolicyRule{
		{Kind: findings.RuleKindFile, Name: "codeowners", Description: "CODEOWNERS exists"},
	}}); err != nil {
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

	pool, err := postgres.NewReadOnlyPool(ctx, f.dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	e := &endpoints{f: f, issuer: iss, ids: map[string]int64{}, c: apitest.New(t, &api.Options{
		Reader: postgres.NewAPIReader(pool), Authn: authn, Authz: authz, StaleAfter: 30 * 24 * time.Hour, Logger: quiet,
		Now: func() time.Time { return t0 },
	})}

	for _, name := range []string{"acme/widgets", "acme-web/b", "acme-pay/c", "acme-pay/d"} {
		org, repo := name[:len(name)-len(filepath.Base(name))-1], filepath.Base(name)

		r, err := f.store.FindRepository(ctx, org, repo, nil)
		if err != nil {
			t.Fatalf("find %s: %v", name, err)
		}

		e.ids[name] = r.ID
	}

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

// Every endpoint answers 200 for a caller who sees everything, and
// apitest validates each body against the spec.
func TestEndpoints_ValidateAgainstTheSpec(t *testing.T) {
	t.Parallel()

	e := newEndpoints(t)

	for _, path := range []string{
		"/me", "/summary", "/rules", "/rules/file/codeowners", "/orgs", "/orgs/acme-web", "/orgs/ACME-PAY",
		"/findings", "/findings?status=non_compliant", "/repositories", e.repoPath("acme-web/b", ""),
		e.repoPath("acme-web/b", "/events"), e.repoPath("acme-web/b", "/checks"), "/compliance/history",
		"/installations", "/policy",
	} {
		t.Run(path, func(t *testing.T) {
			if r := e.get(t, path, "platform"); r.Status != http.StatusOK {
				t.Errorf("GET %s = %d: %s", path, r.Status, r.Body)
			}
		})
	}

	rule := decodeAs[gen.RuleDetail](t, e.get(t, "/rules/file/codeowners", "platform"))
	if rule.Counts.Compliant != 2 || rule.Counts.NonCompliant != 1 || len(rule.OldestFailures) != 1 ||
		rule.Description == nil || len(rule.TopReasons) != 1 || rule.TopReasons[0].Reason != string(findings.ReasonFileMissing) {
		t.Errorf("rule = %+v", rule)
	}

	b := decodeAs[gen.RepositoryDetail](t, e.get(t, e.repoPath("acme-web/b", ""), "platform"))
	if len(b.Findings) != 1 || b.Findings[0].Pr == nil || !b.Findings[0].PrStale || b.Findings[0].Evidence == nil || b.LastCheck == nil {
		t.Errorf("repository b = %+v", b)
	}
}

// An org the caller cannot see answers exactly like one that does not
// exist, and lists never include it.
func TestEndpoints_TwoOrgAuthz(t *testing.T) {
	t.Parallel()

	e := newEndpoints(t)

	for _, path := range []string{
		"/orgs/acme-pay", "/orgs/no-such-org", e.repoPath("acme-pay/c", ""), e.repoPath("acme-pay/c", "/events"),
		e.repoPath("acme-pay/c", "/checks"), "/repositories/999999",
	} {
		if r := e.get(t, path, "team-web"); r.Status != http.StatusNotFound {
			t.Errorf("team-web GET %s = %d, want 404", path, r.Status)
		}
	}

	if r := e.get(t, "/rules/file/no-such-rule", "team-web"); r.Status != http.StatusNotFound {
		t.Errorf("unknown rule = %d, want 404", r.Status)
	}

	orgs := decodeAs[gen.OrgList](t, e.get(t, "/orgs", "team-web"))
	if len(orgs.Items) != 1 || orgs.Items[0].Org != "acme-web" {
		t.Errorf("team-web orgs = %+v", orgs.Items)
	}

	repos := decodeAs[gen.RepositoryPage](t, e.get(t, "/repositories", "team-web"))
	if len(repos.Items) != 1 || repos.Items[0].Org != "acme-web" {
		t.Errorf("team-web repositories = %+v", repos.Items)
	}

	pay := decodeAs[gen.FindingPage](t, e.get(t, "/findings?org=acme-pay", "team-web"))
	if len(pay.Items) != 0 {
		t.Errorf("team-web findings in acme-pay = %+v, want none", pay.Items)
	}

	rules := decodeAs[gen.RuleList](t, e.get(t, "/rules", "team-web"))
	if len(rules.Items) != 1 || rules.Items[0].Counts.NonCompliant != 1 || rules.Items[0].Counts.Compliant != 0 {
		t.Errorf("team-web rules = %+v", rules.Items)
	}

	history := decodeAs[gen.HistoryPage](t, e.get(t, "/compliance/history", "team-web"))
	for _, h := range history.Items {
		if h.Org != "acme-web" {
			t.Errorf("team-web history includes %s", h.Org)
		}
	}

	if inst := decodeAs[gen.InstallationPage](t, e.get(t, "/installations", "team-web")); len(inst.Items) != 0 {
		t.Errorf("team-web installations = %+v, want none (the installation's account is acme)", inst.Items)
	}
}

func TestEndpoints_FindingFilters(t *testing.T) {
	t.Parallel()

	e := newEndpoints(t)

	tests := []struct {
		query string
		want  int
	}{
		{query: "", want: 3},
		{query: "status=non_compliant", want: 1},
		{query: "status=compliant", want: 2},
		{query: "reason=file_missing", want: 1},
		{query: "remediation=pr_open", want: 1},
		{query: "org=ACME-PAY", want: 1},
		{query: "kind=file&rule=codeowners", want: 3},
		{query: "rule=no-such-rule", want: 0},
		{query: "pr_stale=true", want: 1},
		{query: "pr_stale=false", want: 2},
		{query: "pr_stale=true&stale_after=8760h", want: 0},
		{query: "since_before=" + url.QueryEscape(t0.Add(time.Hour).Format(time.RFC3339)), want: 3},
		{query: "since_before=" + url.QueryEscape(t0.Add(-time.Hour).Format(time.RFC3339)), want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			page := decodeAs[gen.FindingPage](t, e.get(t, "/findings?"+tt.query, "platform"))
			if len(page.Items) != tt.want {
				t.Errorf("/findings?%s = %d items, want %d", tt.query, len(page.Items), tt.want)
			}
		})
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
		"/repositories?limit=0", "/repositories?limit=201", "/findings?cursor=not-a-cursor",
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
	ctx := context.Background()

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

		// A write between pages: a new repository, and a changed finding.
		if _, err := e.f.store.UpsertDiscovered(ctx, &store.DiscoveredRepo{
			Org: "acme", Name: "new-" + strconv.Itoa(i), InstallationID: testInstallation,
		}); err != nil {
			t.Fatal(err)
		}

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

	var findingsSeen []gen.Finding

	next = "/findings?limit=1"
	for i := 0; next != "" && i < 10; i++ {
		page := decodeAs[gen.FindingPage](t, e.get(t, next, "platform"))
		findingsSeen = append(findingsSeen, page.Items...)

		// Flip widgets' finding between pages.
		if _, err := e.f.store.RecordCheck(ctx, &store.CheckRecord{
			Key: "flip-" + strconv.Itoa(i), RepositoryID: e.f.repoID, InstallationID: testInstallation, Trigger: store.TriggerSchedule,
			PolicyVersion: "v2:test", StartedAt: t0, FinishedAt: t0, Outcomes: []store.Outcome{missing("codeowners", "CODEOWNERS")},
		}); err != nil {
			t.Fatal(err)
		}

		next = ""
		if page.NextCursor != nil {
			next = "/findings?limit=1&cursor=" + url.QueryEscape(*page.NextCursor)
		}
	}

	if len(findingsSeen) != 3 {
		t.Errorf("paged %d findings, want the 3 seeded exactly once", len(findingsSeen))
	}
}

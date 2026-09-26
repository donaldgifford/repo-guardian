//go:build integration

package postgres_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/api"
	"github.com/donaldgifford/repo-guardian/internal/api/apitest"
	"github.com/donaldgifford/repo-guardian/internal/api/gen"
	"github.com/donaldgifford/repo-guardian/internal/findings"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres"
)

// apiClient serves the API over a read-only pool on f's database with
// auth disabled, so every caller sees every org.
func apiClient(t *testing.T, f *v2Fixture) (*apitest.Client, *postgres.APIReader) {
	t.Helper()

	pool, err := postgres.NewReadOnlyPool(t.Context(), f.dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	reader := postgres.NewAPIReader(pool)

	return apitest.New(t, &api.Options{
		Reader: reader, StaleAfter: 30 * 24 * time.Hour, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { return t0 },
	}), reader
}

func getJSON[T any](t *testing.T, c *apitest.Client, path string) T {
	t.Helper()

	resp := c.Get(path, "")
	if resp.Status != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", path, resp.Status, resp.Body)
	}

	var v T
	if err := json.Unmarshal(resp.Body, &v); err != nil {
		t.Fatal(err)
	}

	return v
}

// eventID identifies a timeline entry across pages.
type eventID struct {
	source string
	id     int64
}

func TestAPI_TimelineMergesBothSourcesAndPagesStably(t *testing.T) {
	t.Parallel()

	f := newV2Fixture(t)
	ctx := t.Context()

	// A failing check, then a passing one: two finding events. Parking
	// and unparking add repository events beside the discovery event.
	for i, o := range []store.Outcome{missing("codeowners", "CODEOWNERS"), compliant("codeowners")} {
		if _, err := f.store.RecordCheck(ctx, &store.CheckRecord{
			Key: "tl" + string(rune('a'+i)), RepositoryID: f.repoID, InstallationID: testInstallation, Trigger: store.TriggerSchedule,
			PolicyVersion: "v2:test", StartedAt: t0, FinishedAt: t0, Outcomes: []store.Outcome{o},
		}); err != nil {
			t.Fatal(err)
		}
	}

	if err := f.store.Park(ctx, f.repoID, store.ParkAccessDenied, true); err != nil {
		t.Fatal(err)
	}

	c, _ := apiClient(t, f)
	base := "/repositories/" + strconv.FormatInt(f.repoID, 10) + "/events"

	whole := getJSON[gen.EventPage](t, c, base+"?limit=200")
	if whole.NextCursor != nil {
		t.Fatalf("one page of 200 has a next cursor")
	}

	sources := map[string]int{}
	for i, e := range whole.Items {
		sources[e.Source]++

		if i > 0 && e.OccurredAt.After(whole.Items[i-1].OccurredAt) {
			t.Errorf("event %d is newer than event %d: the timeline is not newest first", i, i-1)
		}

		// A finding cleared by parking has no to_status; every finding
		// event names its rule.
		if e.Source == store.EventSourceFinding && (e.RuleName == nil || e.FromStatus == nil && e.ToStatus == nil) {
			t.Errorf("finding event %d lacks its rule or statuses: %+v", e.Id, e)
		}

		if e.Source == store.EventSourceRepository && e.Kind == nil {
			t.Errorf("repository event %d lacks its kind", e.Id)
		}
	}

	if sources[store.EventSourceFinding] < 2 || sources[store.EventSourceRepository] < 1 {
		t.Fatalf("timeline sources = %v, want both merged", sources)
	}

	var paged []eventID

	next := base + "?limit=1"
	for pages := 0; next != ""; pages++ {
		if pages > len(whole.Items) {
			t.Fatal("paging did not terminate")
		}

		page := getJSON[gen.EventPage](t, c, next)
		for _, e := range page.Items {
			paged = append(paged, eventID{e.Source, e.Id})
		}

		next = ""
		if page.NextCursor != nil {
			next = base + "?limit=1&cursor=" + url.QueryEscape(*page.NextCursor)
		}
	}

	if len(paged) != len(whole.Items) {
		t.Fatalf("paged %d events, one page had %d", len(paged), len(whole.Items))
	}

	for i, e := range whole.Items {
		if paged[i] != (eventID{e.Source, e.Id}) {
			t.Errorf("event %d: paged %v, whole page %v", i, paged[i], eventID{e.Source, e.Id})
		}
	}
}

func TestAPI_PolicyShowsVersionRolloutAndRules(t *testing.T) {
	t.Parallel()

	f := newV2Fixture(t)
	c, _ := apiClient(t, f)

	if resp := c.Get("/policy", ""); resp.Status != http.StatusNotFound {
		t.Fatalf("policy before any version = %d, want 404", resp.Status)
	}

	summary := store.PolicySummary{Rules: []store.PolicyRule{
		{Kind: findings.RuleKindFile, Name: "codeowners", Description: "CODEOWNERS exists", CheckMode: "exists"},
	}}
	if _, err := f.store.RecordPolicyVersion(t.Context(), "v2:abc", summary); err != nil {
		t.Fatal(err)
	}

	pol := getJSON[gen.Policy](t, c, "/policy")
	if pol.Version != "v2:abc" || pol.RolloutState != "in_progress" || len(pol.Rules) != 1 || pol.Rules[0].Name != "codeowners" {
		t.Errorf("policy = %+v", pol)
	}

	if err := f.store.CompletePolicyRollout(t.Context(), "v2:abc"); err != nil {
		t.Fatal(err)
	}

	if pol := getJSON[gen.Policy](t, c, "/policy"); pol.RolloutState != "complete" || pol.RolloutCompletedAt == nil {
		t.Errorf("policy after rollout = %+v", pol)
	}
}

func TestStatus_RefreshReadsPostgres(t *testing.T) {
	t.Parallel()

	f := newV2Fixture(t)
	seedTwoOrgs(t, f)

	if err := f.store.RecordServiceRun(t.Context(), &store.ServiceRun{
		Kind: store.ServiceRunDiscovery, Success: true, StartedAt: t0.Add(-2 * time.Minute), FinishedAt: t0.Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	_, reader := apiClient(t, f)
	page := api.NewStatusPage(&api.StatusConfig{
		Reader: reader, CheckInterval: 24 * time.Hour, DiscoveryInterval: time.Hour, SnapshotInterval: 24 * time.Hour,
		RateReserve: 0.1, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return t0.Add(5 * time.Minute) },
	})

	if err := page.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}

	s := page.Current()
	states := map[string]string{}

	for _, c := range s.Components {
		states[c.Name] = c.State
	}

	if states["checks"] != "operational" || states["discovery"] != "operational" || states["snapshots"] != "unknown" {
		t.Errorf("components = %v", states)
	}

	if s.Compliance.Percent == nil || *s.Compliance.Percent != 66.6 {
		t.Errorf("fleet compliance = %v, want 66.6", s.Compliance.Percent)
	}
}

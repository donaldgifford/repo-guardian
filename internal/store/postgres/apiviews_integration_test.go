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
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres"
)

// apiClient serves the API over a read-only pool on f's database with
// auth disabled, so every caller sees every org.
func apiClient(t *testing.T, f *apiFixture) (*apitest.Client, *postgres.APIReader) {
	t.Helper()

	pool, err := postgres.NewReadOnlyPool(t.Context(), f.dsns.ReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	reader := postgres.NewAPIReader(pool)

	return apitest.New(t, &api.Options{
		Reader: reader, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
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

	f := newAPIFixture(t)

	// A failing evaluation, then a passing one: two result events.
	// Archiving withdraws the result (a third, with no to_status) and adds
	// a parked repository event beside the discovery event.
	f.evaluate(t, f.repoID, "tl-a", "non_compliant")
	f.evaluate(t, f.repoID, "tl-b", "compliant")
	f.park(t, f.repoID, "archived")

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

		// A result withdrawn by parking has no to_status; every result
		// event names its control.
		if e.Source == store.EventSourceResult && (e.ControlId == nil || e.FromStatus == nil && e.ToStatus == nil) {
			t.Errorf("result event %d lacks its control or statuses: %+v", e.Id, e)
		}

		if e.Source == store.EventSourceRepository && e.Kind == nil {
			t.Errorf("repository event %d lacks its kind", e.Id)
		}
	}

	if sources[store.EventSourceResult] < 3 || sources[store.EventSourceRepository] < 2 {
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

func TestStatus_RefreshReadsPostgres(t *testing.T) {
	t.Parallel()

	f := newAPIFixture(t)
	seedTwoOrgs(t, f)

	// The store stamps checks with the real clock, so the page reads it too.
	now := time.Now()
	if err := f.store.RecordServiceRun(t.Context(), &store.ServiceRun{
		Kind: store.ServiceRunDiscovery, Success: true, StartedAt: now.Add(-2 * time.Minute), FinishedAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	_, reader := apiClient(t, f)
	page := api.NewStatusPage(&api.StatusConfig{
		Reader: reader, CheckInterval: 24 * time.Hour, DiscoveryInterval: time.Hour, SnapshotInterval: 24 * time.Hour,
		RateReserve: 0.1, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return now.Add(5 * time.Minute) },
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

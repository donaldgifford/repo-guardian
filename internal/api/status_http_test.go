package api_test

import (
	"context"
	"net/http"
	"regexp"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"

	apispec "github.com/donaldgifford/repo-guardian/api"
	"github.com/donaldgifford/repo-guardian/internal/api"
	"github.com/donaldgifford/repo-guardian/internal/api/apitest"
	"github.com/donaldgifford/repo-guardian/internal/api/gen"
	"github.com/donaldgifford/repo-guardian/internal/store"
)

// countingStatus counts the status page's reads.
type countingStatus struct{ reads atomic.Int32 }

func (c *countingStatus) StatusInputs(context.Context, time.Time) (*store.StatusInputs, error) {
	c.reads.Add(1)

	return &store.StatusInputs{
		LastCheckSuccess:   &now,
		LastServiceSuccess: map[store.ServiceRunKind]time.Time{store.ServiceRunDiscovery: now, store.ServiceRunSnapshot: now},
		Rates:              []store.RateSnapshot{{Limit: 5000, Remaining: 5000, ResetAt: now.Add(time.Hour)}},
		Compliance:         store.StatusCounts{Compliant: 9, NonCompliant: 1},
	}, nil
}

func statusPage(reader api.StatusReader) *api.StatusPage {
	return api.NewStatusPage(&api.StatusConfig{
		Reader: reader, CheckInterval: 24 * time.Hour, DiscoveryInterval: time.Hour, SnapshotInterval: 24 * time.Hour,
		RateReserve: 0.1, Logger: quiet, Now: func() time.Time { return now },
	})
}

// Requests read the cache: after one refresh, any number of requests
// cause zero further reads. The API's Reader is a nil-embedding fake
// that panics if touched.
func TestStatus_RequestsNeverQuery(t *testing.T) {
	t.Parallel()

	counter := &countingStatus{}
	page := statusPage(counter)
	c := apitest.New(t, &api.Options{Reader: &orgReader{}, Status: page, Logger: quiet, Now: func() time.Time { return now }})

	before := decode[gen.Status](t, c.Get("/status", ""))
	if before.State != "unknown" || counter.reads.Load() != 0 {
		t.Fatalf("before the first refresh: state %s after %d reads, want unknown after 0", before.State, counter.reads.Load())
	}

	if err := page.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}

	for range 50 {
		if r := c.Get("/status", ""); r.Status != http.StatusOK {
			t.Fatalf("status = %d", r.Status)
		}
	}

	if n := counter.reads.Load(); n != 1 {
		t.Errorf("reads after one refresh and 50 requests = %d, want 1", n)
	}

	after := decode[gen.Status](t, c.Get("/status", ""))
	if after.State != "operational" || !after.Compliance.Measured || *after.Compliance.Percent != 90 {
		t.Errorf("status after refresh = %+v", after)
	}
}

func TestStatus_PublicUnlessConfigured(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	if r := h.client.Get("/status", ""); r.Status != http.StatusOK {
		t.Errorf("public status without a token = %d, want 200", r.Status)
	}

	iss := h.issuer
	authn := api.NewAuthenticator(&api.AuthnConfig{
		Issuer: iss.URL, Audience: audience, NameClaim: "preferred_username", GroupsClaim: "groups",
	}, quiet)
	authn.Start(t.Context())

	private := apitest.New(t, &api.Options{
		Reader: &orgReader{}, Authn: authn, Authz: &api.AuthzConfig{}, StatusRequiresAuth: true, Logger: quiet,
		Now: func() time.Time { return now },
	})

	if r := private.Get("/status", ""); r.Status != http.StatusUnauthorized {
		t.Errorf("STATUS_PUBLIC=false without a token = %d, want 401", r.Status)
	}

	// Any valid caller may read it, even one who sees no org.
	if r := private.Get("/status", iss.Valid(t)); r.Status != http.StatusOK {
		t.Errorf("STATUS_PUBLIC=false with a token and no orgs = %d, want 200: %s", r.Status, r.Body)
	}
}

// statusStrings are the only string fields the status page may carry;
// each is an enum or a fixed template. A new string field is how an org
// name or an error message would leak onto a public page.
var statusStrings = []string{
	"Status.state", "Status.updated_at(date-time)", "Status.components[].name", "Status.components[].state",
	"Status.components[].detail",
}

// TestStatus_PrivacyGuard fails if the Status schema gains a field that
// could carry free text (DESIGN-0027 § Status page).
func TestStatus_PrivacyGuard(t *testing.T) {
	t.Parallel()

	doc, err := openapi3.NewLoader().LoadFromData(apispec.Spec)
	if err != nil {
		t.Fatal(err)
	}

	var got []string

	var walk func(path string, s *openapi3.SchemaRef)
	walk = func(path string, s *openapi3.SchemaRef) {
		v := s.Value
		switch {
		case v.Type.Is("object"):
			for name, p := range v.Properties {
				walk(path+"."+name, p)
			}
		case v.Type.Is("array"):
			walk(path+"[]", v.Items)
		case v.Type.Includes("string"):
			if v.Format != "" {
				path += "(" + v.Format + ")"
			}

			got = append(got, path)
		}
	}

	walk("Status", doc.Components.Schemas["Status"])
	sort.Strings(got)

	want := append([]string(nil), statusStrings...)
	sort.Strings(want)

	if len(got) != len(want) {
		t.Fatalf("status string fields = %v, want exactly %v", got, want)
	}

	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("status string fields = %v, want exactly %v", got, want)
		}
	}
}

// details are the fixed component detail templates.
var details = regexp.MustCompile(`^(last success|last run|last webhook-triggered check) (<1m|\d+[mhd]) ago$|` +
	`^oldest task waiting (<1m|\d+[mhd])$|` +
	`^(no successful check yet|no successful run yet|no webhook-triggered check yet|no rate-limit snapshot yet|` +
	`over 10% of checks failed in the last hour|every installation is below its reserve|an installation is below its reserve|` +
	`every installation is within budget|backlog unavailable|no workers polling)$`)

func TestStatus_DetailsAreFixedTemplates(t *testing.T) {
	t.Parallel()

	page := statusPage(&countingStatus{})
	if err := page.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}

	for _, c := range page.Current().Components {
		if !details.MatchString(c.Detail) {
			t.Errorf("%s detail %q is not a fixed template", c.Name, c.Detail)
		}
	}
}

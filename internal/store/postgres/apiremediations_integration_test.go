//go:build integration

package postgres_test

import (
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/api/gen"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres"
)

// seedRemediations adds, beside acme-web/b's open PR #7: a direct apply
// on acme-pay/c with its proposal, one journaled step and an event; a
// merged PR on acme/widgets created well before t0 and holding
// permission; and a conflict hold on acme-pay/c's result, set before
// any pull request exists (IMPL-0030 OQ20).
func seedRemediations(t *testing.T, e *endpoints) {
	t.Helper()

	c, w := strconv.FormatInt(e.ids["acme-pay/c"], 10), strconv.FormatInt(e.ids["acme/widgets"], 10)

	run(t, e.f.dsns.Owner, `INSERT INTO remediations (repository_id, control_id, assignment_epoch, operation_key, kind, state,
		intent_revision, proposal, remediated_generation, created_at, last_run_at)
		VALUES (`+c+`, 'codeowners', 1, 'op-apply', 'api', 'applied', 'i',
		'[{"key":"setting:delete_branch_on_merge","op":"set","before":false,"after":true}]', 1, now(), now())`)
	run(t, e.f.dsns.Owner, `INSERT INTO remediation_steps (remediation_id, operation_key, seq, kind, resource_key, status,
		before, after, occurred_at)
		SELECT id, 'op-apply', 1, 'api_write', 'setting:delete_branch_on_merge', 'done', 'false', 'true', now()
		FROM remediations WHERE operation_key = 'op-apply'`)
	run(t, e.f.dsns.Owner, `INSERT INTO remediation_events (remediation_id, kind, detail, occurred_at)
		SELECT id, 'applied', '{}', now() FROM remediations WHERE operation_key = 'op-apply'`)
	run(t, e.f.dsns.Owner, `INSERT INTO remediations (repository_id, control_id, assignment_epoch, operation_key, kind, state,
		intent_revision, pr_number, hold, remediated_generation, created_at, last_run_at, closed_at)
		VALUES (`+w+`, 'codeowners', 1, 'op-old', 'pull_request', 'merged', 'i', 3, 'permission', 1,
		'2026-07-01T00:00:00Z', '2026-07-02T00:00:00Z', '2026-07-02T00:00:00Z')`)
	run(t, e.f.dsns.Owner, `UPDATE control_results SET hold = 'conflict', hold_since = '2026-08-25T00:00:00Z'
		WHERE repository_id = `+c)
}

// TestEndpoints_Remediations is IMPL-0030 7.4 and 7.16: every filter,
// validated against the spec by apitest, the held result as a row with
// no id or state, and the backlog on /summary and the status page.
func TestEndpoints_Remediations(t *testing.T) {
	t.Parallel()

	e := newEndpoints(t)
	seedRemediations(t, e)

	tests := []struct {
		query string
		want  int
	}{
		{query: "", want: 3},
		{query: "kind=api", want: 1},
		{query: "state=open", want: 1},
		{query: "kind=pull_request&state=open&older_than_days=30", want: 0},
		{query: "older_than_days=30", want: 1},
		{query: "org=acme-web", want: 1},
		{query: "control=codeowners", want: 3},
		{query: "hold=conflict", want: 1},
		{query: "hold=conflict&kind=api", want: 0},
		{query: "hold=conflict&state=open", want: 0},
		{query: "hold=permission", want: 1},
		{query: "hold=conflict&hold=permission", want: 2},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			page := decodeAs[gen.RemediationPage](t, e.get(t, "/remediations?"+tt.query, "platform"))
			if len(page.Items) != tt.want {
				t.Errorf("/remediations?%s = %d items, want %d: %+v", tt.query, len(page.Items), tt.want, page.Items)
			}
		})
	}

	held := decodeAs[gen.RemediationPage](t, e.get(t, "/remediations?hold=conflict", "platform")).Items[0]
	if held.Id != nil || held.State != nil || held.PrNumber != nil || held.Kind != "pull_request" ||
		held.HoldSince == nil || held.Repository != "c" {
		t.Errorf("held row = %+v, want a pull_request row with no id, state or PR", held)
	}

	apply := decodeAs[gen.RemediationPage](t, e.get(t, "/remediations?kind=api", "platform")).Items[0]
	if apply.Id == nil || apply.Proposal == nil || len(*apply.Proposal) != 1 || len(apply.Steps) != 1 ||
		apply.Steps[0].After != true {
		t.Errorf("direct apply = %+v, want its proposal and one step", apply)
	}

	if s := decodeAs[gen.Summary](t, e.get(t, "/summary", "platform")); s.RemediationBacklog != 1 {
		t.Errorf("summary remediation_backlog = %d, want the open PR alone", s.RemediationBacklog)
	}

	pool, err := postgres.NewReadOnlyPool(t.Context(), e.f.dsns.ReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	in, err := postgres.NewAPIReader(pool).StatusInputs(t.Context(), time.Now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	if in.RemediationBacklog != 1 {
		t.Errorf("status remediation backlog = %d, want 1", in.RemediationBacklog)
	}
}

// TestEndpoints_RemediationsOnTheRepository: the latest remediation on
// /repositories/{id}/controls, and remediation events in its timeline.
func TestEndpoints_RemediationsOnTheRepository(t *testing.T) {
	t.Parallel()

	e := newEndpoints(t)
	seedRemediations(t, e)

	rc := decodeAs[gen.RepositoryControlList](t, e.get(t, e.repoPath("acme-pay/c", "/controls"), "platform"))
	if len(rc.Items) != 1 || rc.Items[0].Remediation == nil || rc.Items[0].Remediation.Kind != "api" ||
		len(rc.Items[0].Remediation.Steps) != 1 {
		t.Errorf("acme-pay/c controls = %+v, want codeowners with its direct apply", rc.Items)
	}

	events := decodeAs[gen.EventPage](t, e.get(t, e.repoPath("acme-pay/c", "/events"), "platform"))

	found := false

	for _, ev := range events.Items {
		if ev.Source == "remediation" {
			found = ev.Kind != nil && *ev.Kind == "applied" && ev.ControlId != nil && (*ev.Detail)["remediation_id"] != nil
		}
	}

	if !found {
		t.Errorf("acme-pay/c events = %+v, want the applied remediation event", events.Items)
	}
}

// TestEndpoints_RemediationsTwoOrgAuthz: a team sees neither another
// org's remediations nor its held results.
func TestEndpoints_RemediationsTwoOrgAuthz(t *testing.T) {
	t.Parallel()

	e := newEndpoints(t)
	seedRemediations(t, e)

	for query, want := range map[string]int{"": 1, "hold=conflict": 0, "hold=conflict&hold=permission": 0, "kind=api": 0} {
		page := decodeAs[gen.RemediationPage](t, e.get(t, "/remediations?"+query, "team-web"))
		if len(page.Items) != want {
			t.Errorf("team-web /remediations?%s = %+v, want %d rows", query, page.Items, want)
		}

		for _, r := range page.Items {
			if r.Org != "acme-web" {
				t.Errorf("team-web saw %s/%s", r.Org, r.Repository)
			}
		}
	}

	if s := decodeAs[gen.Summary](t, e.get(t, "/summary", "team-web")); s.RemediationBacklog != 1 {
		t.Errorf("team-web remediation_backlog = %d, want 1", s.RemediationBacklog)
	}
}

// TestEndpoints_RemediationsPageHeldAndTrackedRows: one cursor walks
// held results and remediations rows together without a repeat, and a
// cursor reused under another filter is refused.
func TestEndpoints_RemediationsPageHeldAndTrackedRows(t *testing.T) {
	t.Parallel()

	e := newEndpoints(t)
	seedRemediations(t, e)

	seen := map[string]bool{}
	next := "/remediations?hold=conflict&hold=permission&limit=1"

	for i := 0; next != "" && i < 5; i++ {
		page := decodeAs[gen.RemediationPage](t, e.get(t, next, "platform"))
		for _, r := range page.Items {
			k := r.Repository + "/" + strconv.FormatBool(r.Id != nil)
			if seen[k] {
				t.Fatalf("row %s repeated", k)
			}

			seen[k] = true
		}

		next = ""
		if page.NextCursor != nil {
			next = "/remediations?hold=conflict&hold=permission&limit=1&cursor=" + url.QueryEscape(*page.NextCursor)

			if r := e.get(t, "/remediations?hold=conflict&limit=1&cursor="+url.QueryEscape(*page.NextCursor), "platform"); r.Status != http.StatusBadRequest {
				t.Errorf("cursor under another filter = %d, want 400", r.Status)
			}
		}
	}

	if len(seen) != 2 || !seen["c/false"] || !seen["widgets/true"] {
		t.Errorf("walked %v, want the held result on c and the remediation on widgets", seen)
	}
}

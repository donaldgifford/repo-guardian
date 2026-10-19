package activities

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"

	"github.com/donaldgifford/repo-guardian/internal/control"
	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// memServiceStore adds the maintenance reads to the in-memory store:
// the candidates are the test's (the view's actions are pinned by
// TestRemediationMaintenance_Actions), each row's target is built from
// it, and unreachable is the Remediation App losing the repository.
type memServiceStore struct {
	*memRemediationStore

	cands       []store.MaintenanceCandidate
	unreachable bool
	observedAt  time.Time
}

func (m *memServiceStore) MaintenanceCandidates(context.Context, int) ([]store.MaintenanceCandidate, error) {
	return m.cands, nil
}

func (m *memServiceStore) MaintenanceTarget(_ context.Context, id int64) (*store.MaintenanceTarget, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, r := range m.rows {
		if r.ID == id {
			return &store.MaintenanceTarget{
				RemediationID: r.ID, RepositoryID: prRepo, ControlID: r.ControlID, Kind: r.Kind, State: r.State,
				OperationKey: r.OperationKey, AssignmentEpoch: r.AssignmentEpoch, PRNumber: r.PRNumber,
				Org: "acme", Name: "svc", InstallationID: 7, Remediable: !m.unreachable, ObservedAt: m.observedAt,
			}, nil
		}
	}

	return nil, store.ErrRemediationNotFound
}

func (m *memServiceStore) ExpireReservations(_ context.Context, now time.Time) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var orgs []string

	m.rows = slices.DeleteFunc(m.rows, func(r *store.Remediation) bool {
		expired := r.State == store.StateReserved && r.ReservedUntil != nil && r.ReservedUntil.Before(now)
		if expired {
			orgs = append(orgs, "acme")
		}

		return expired
	})

	return orgs, nil
}

func (*memServiceStore) DueRemediations(context.Context, int) ([]store.DueRemediation, error) {
	return nil, nil
}

func (*memServiceStore) RefreshRemediateAccess(context.Context, *store.AppInstallation, []int64) ([]store.AccessChange, error) {
	return nil, nil
}

func (*memServiceStore) PruneRemediateInstallations(context.Context, []int64) ([]store.AccessChange, error) {
	return nil, nil
}

func (*memServiceStore) RecordServiceRun(context.Context, *store.ServiceRun) error { return nil }

// maintain runs one maintenance pass over the harness with c's latest
// row as the one candidate under action.
func (h *prHarness) maintain(svc *memServiceStore, c, action string) *workflows.MaintenanceResult {
	h.t.Helper()

	row := h.latestOf(c)
	svc.cands = []store.MaintenanceCandidate{{
		RemediationID: row.ID, RepositoryID: prRepo, ControlID: c, Kind: row.Kind, State: row.State, Action: action,
	}}

	s := NewRemediationServices(&RemediationServicesConfig{Remediator: h.r, Store: svc, Logger: slog.New(slog.DiscardHandler)})

	var suite testsuite.WorkflowTestSuite

	env := suite.NewTestActivityEnvironment()
	env.RegisterActivityWithOptions(s.Maintain, activity.RegisterOptions{Name: workflows.RemediationMaintainActivity})

	val, err := env.ExecuteActivity(workflows.RemediationMaintainActivity, 10)
	if err != nil {
		h.t.Fatalf("Maintain = %v", err)
	}

	var res workflows.MaintenanceResult
	if err := val.Get(&res); err != nil {
		h.t.Fatal(err)
	}

	return &res
}

func serviceStore(h *prHarness) *memServiceStore {
	return &memServiceStore{memRemediationStore: h.st, observedAt: time.Now().Add(-time.Minute)}
}

// The ways a control stops being remediated (DESIGN-0032 "Lifecycle
// maintenance"), each as the remediator's read of the control sees it.
// Each reaches remediation_maintenance as withdraw.
var withdrawals = map[string]func(h *prHarness, c string){
	"control withdrawn":    func(h *prHarness, c string) { delete(h.st.targets, c) },
	"repository excluded":  func(h *prHarness, c string) { h.st.targets[c].State = "excluded" },
	"repository parked":    func(h *prHarness, c string) { h.st.targets[c].Active = false },
	"org removed":          func(h *prHarness, c string) { delete(h.st.targets, c) },
	"switched to evaluate": func(h *prHarness, c string) { h.st.targets[c].EffectiveMode = "evaluate" },
}

// latencySamples is remediation_cleanup_latency_seconds{org="acme"}'s
// sample count.
func latencySamples(t *testing.T) uint64 {
	t.Helper()

	h, ok := metrics.RemediationCleanupLatencySeconds.WithLabelValues("acme").(prometheus.Histogram)
	if !ok {
		t.Fatal("remediation_cleanup_latency_seconds is not a histogram")
	}

	var m dto.Metric
	if err := h.Write(&m); err != nil {
		t.Fatal(err)
	}

	return m.GetHistogram().GetSampleCount()
}

// TestMaintain_WithdrawClosesThePR is IMPL-0030 6.7 for a pull request:
// whatever withdrew the control, the open PR closes as closed_withdrawn
// with a comment, the branch stays, and no evaluation is needed.
func TestMaintain_WithdrawClosesThePR(t *testing.T) {
	t.Parallel()

	for name, withdraw := range withdrawals {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h := newNoticeHarness(t)
			n := h.onboard()
			withdraw(h, prControl)

			svc := serviceStore(h)
			before := latencySamples(t)

			res := h.maintain(svc, prControl, store.MaintenanceWithdraw)
			if res.Closed != 1 || res.Failed != 0 {
				t.Fatalf("Maintain = %+v, want one closed", res)
			}

			if pr := h.repo.PullRequest(n); pr.State != "closed" || pr.Merged {
				t.Errorf("PR = %+v, want closed unmerged", pr)
			}

			if row := h.latestOf(prControl); row.State != store.StateClosedWithdrawn || row.ClosedAt == nil {
				t.Errorf("row = %+v, want closed_withdrawn", row)
			}

			if comments := h.repo.Comments(n); len(comments) == 0 || !strings.HasPrefix(comments[len(comments)-1], StickyMarker) {
				t.Errorf("comments = %q, want the sticky comment", comments)
			}

			if _, ok, _ := h.repo.As(prBot).GetRef(context.Background(), branchName(prControl)); !ok {
				t.Error("the branch was deleted")
			}

			if latencySamples(t) <= before {
				t.Error("remediation_cleanup_latency_seconds observed nothing")
			}

			// Terminal: a second pass finds nothing to close.
			if again := h.maintain(svc, prControl, store.MaintenanceWithdraw); again.Closed != 0 {
				t.Errorf("second pass = %+v, want nothing closed", again)
			}
		})
	}
}

// TestMaintain_WithdrawEndsTheRecommendation is IMPL-0030 6.7 for a
// recommendation: it ends withdrawn with no GitHub write.
func TestMaintain_WithdrawEndsTheRecommendation(t *testing.T) {
	t.Parallel()

	for name, withdraw := range withdrawals {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h := newAPIHarness(t, control.ApplyRecommend, "settings", nil)
			seedSettings(t, h)
			h.runAs("settings", workflows.RemediationDone)
			withdraw(h, "settings")

			res := h.maintain(serviceStore(h), "settings", store.MaintenanceWithdraw)
			if res.Withdrawn != 1 {
				t.Fatalf("Maintain = %+v, want one withdrawn", res)
			}

			if row := h.latestOf("settings"); row.State != store.StateWithdrawn {
				t.Errorf("row state = %q, want withdrawn", row.State)
			}

			if w := h.botWrites(); len(w) != 0 {
				t.Errorf("withdrawing a recommendation wrote to GitHub: %v", w)
			}
		})
	}
}

// TestMaintain_UnreachableBlocksTheCleanup: with the Remediation App
// unable to reach the repository, the PR stays open and the row holds
// permission (blocked cleanup); a refused close does the same.
func TestMaintain_UnreachableBlocksTheCleanup(t *testing.T) {
	t.Parallel()

	t.Run("unreachable", func(t *testing.T) {
		t.Parallel()

		h := newNoticeHarness(t)
		n := h.onboard()
		delete(h.st.targets, prControl)

		svc := serviceStore(h)
		svc.unreachable = true

		if res := h.maintain(svc, prControl, store.MaintenanceWithdraw); res.Blocked != 1 {
			t.Fatalf("Maintain = %+v, want blocked", res)
		}

		if pr := h.repo.PullRequest(n); pr.State != prOpen {
			t.Errorf("PR state = %q, want it left open", pr.State)
		}

		if row := h.latestOf(prControl); row.State != store.StateOpen || row.Hold != store.HoldPermission {
			t.Errorf("row = %+v, want open holding permission", row)
		}
	})

	t.Run("close refused", func(t *testing.T) {
		t.Parallel()

		h := newNoticeHarness(t)
		n := h.onboard()
		delete(h.st.targets, prControl)
		h.repo.FailNext("ClosePullRequest", control.ErrPermission)

		if res := h.maintain(serviceStore(h), prControl, store.MaintenanceWithdraw); res.Blocked != 1 {
			t.Fatalf("Maintain = %+v, want blocked", res)
		}

		if row := h.latestOf(prControl); row.State != store.StateOpen || row.Hold != store.HoldPermission || h.repo.PullRequest(n).State != prOpen {
			t.Errorf("row = %+v, want open holding permission", row)
		}
	})
}

// reassigningWriter re-assigns the control as the comment is written:
// between the candidate read and the close.
type reassigningWriter struct {
	control.Writer

	reassign func()
}

func (w reassigningWriter) UpsertPRComment(ctx context.Context, number int, marker, body string) error {
	w.reassign()

	return w.Writer.UpsertPRComment(ctx, number, marker, body)
}

// TestMaintain_ReassignedStopsTheCleanup: a control assigned again
// between the candidate read and the close fails the fence before the
// close.
func TestMaintain_ReassignedStopsTheCleanup(t *testing.T) {
	t.Parallel()

	h := newNoticeHarness(t)
	n := h.onboard()
	saved := *h.st.targets[prControl]
	delete(h.st.targets, prControl)

	clients, ok := h.r.clients.(*ghfakeRemediationClients)
	if !ok {
		t.Fatal("harness clients are not the ghfake ones")
	}

	clients.wrap = func(w control.Writer) control.Writer {
		return reassigningWriter{Writer: w, reassign: func() {
			h.st.mu.Lock()
			h.st.targets[prControl] = &saved
			h.st.mu.Unlock()
		}}
	}

	res := h.maintain(serviceStore(h), prControl, store.MaintenanceWithdraw)
	if res.Closed != 0 || h.repo.PullRequest(n).State != prOpen || h.latestOf(prControl).State != store.StateOpen {
		t.Errorf("Maintain = %+v, PR %q; want nothing closed under a re-assigned control", res, h.repo.PullRequest(n).State)
	}
}

// TestMaintain_CloseCompliantOnlyOnAFreshRead: a stored compliant status
// alone closes nothing; once main really is compliant the PR closes as
// closed_compliant.
func TestMaintain_CloseCompliantOnlyOnAFreshRead(t *testing.T) {
	t.Parallel()

	h := newNoticeHarness(t)
	n := h.onboard()
	h.st.targets[prControl].Status = "compliant"
	svc := serviceStore(h)

	if res := h.maintain(svc, prControl, store.MaintenanceCloseCompliant); res.Closed != 0 || h.repo.PullRequest(n).State != prOpen {
		t.Fatalf("Maintain = %+v, want the PR left open while main is not compliant", res)
	}

	h.repo.Push(alice, "main", map[string]*string{"NOTICE": strp(noticeV1)}, "fix on main")

	if res := h.maintain(svc, prControl, store.MaintenanceCloseCompliant); res.Closed != 1 {
		t.Fatalf("Maintain = %+v, want one closed", res)
	}

	if row := h.latestOf(prControl); row.State != store.StateClosedCompliant || h.repo.PullRequest(n).State != "closed" {
		t.Errorf("row = %+v, want closed_compliant", row)
	}
}

// TestMaintain_SupersedeAndResolveRecommendations: a moved
// recommendation is superseded; one main now satisfies is resolved.
func TestMaintain_SupersedeAndResolveRecommendations(t *testing.T) {
	t.Parallel()

	h := newAPIHarness(t, control.ApplyRecommend, "settings", nil)
	seedSettings(t, h)
	h.runAs("settings", workflows.RemediationDone)

	if res := h.maintain(serviceStore(h), "settings", store.MaintenanceSupersede); res.Superseded != 1 {
		t.Fatalf("supersede = %+v", res)
	}

	if row := h.latestOf("settings"); row.State != store.StateSuperseded {
		t.Errorf("row state = %q, want superseded", row.State)
	}

	h2 := newAPIHarness(t, control.ApplyRecommend, "settings", nil)
	seedSettings(t, h2)
	h2.runAs("settings", workflows.RemediationDone)

	if res := h2.maintain(serviceStore(h2), "settings", store.MaintenanceCloseCompliant); res.Closed != 0 {
		t.Fatalf("close_compliant on a non-compliant repository = %+v, want nothing", res)
	}
}

// TestMaintain_ExpiresReservations: an expired reservation is freed and
// counted per org.
func TestMaintain_ExpiresReservations(t *testing.T) {
	t.Parallel()

	h := newNoticeHarness(t)
	past := time.Now().Add(-time.Minute)
	h.st.rows = append(h.st.rows, &store.Remediation{
		ID: 1, ControlID: prControl, OperationKey: "k", Kind: store.RemediationPullRequest, State: store.StateReserved, ReservedUntil: &past,
	})

	before := testutil.ToFloat64(metrics.RemediationReservationsExpiredTotal.WithLabelValues("acme"))

	if res := h.maintain(serviceStore(h), prControl, store.MaintenanceExpire); res.Expired != 1 {
		t.Fatalf("Maintain = %+v, want one expired", res)
	}

	if len(h.st.rows) != 0 {
		t.Errorf("rows = %+v, want the reservation freed", h.st.rows)
	}

	if got := testutil.ToFloat64(metrics.RemediationReservationsExpiredTotal.WithLabelValues("acme")) - before; got < 1 {
		t.Errorf("remediation_reservations_expired_total{org=acme} rose by %v, want 1", got)
	}
}

package api

import (
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/api/gen"
	"github.com/donaldgifford/repo-guardian/internal/store"
)

var statusNow = time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)

func ptrTime(d time.Duration) *time.Time {
	t := statusNow.Add(-d)

	return &t
}

var statusCfg = StatusConfig{
	CheckInterval: 24 * time.Hour, DiscoveryInterval: time.Hour, SnapshotInterval: 24 * time.Hour, RateReserve: 0.10,
}

func component(t *testing.T, s *gen.Status, name string) gen.Component {
	t.Helper()

	for _, c := range s.Components {
		if c.Name == name {
			return c
		}
	}

	t.Fatalf("no %s component in %+v", name, s.Components)

	return gen.Component{}
}

func TestStatus_ChecksThresholds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		last           *time.Time
		finished, errs int
		want           string
	}{
		{name: "never", want: stateUnknown},
		{name: "recent", last: ptrTime(3 * time.Minute), finished: 100, errs: 10, want: stateOperational},
		{name: "error ratio over 10%", last: ptrTime(3 * time.Minute), finished: 100, errs: 11, want: stateDegraded},
		{name: "last success over 2h", last: ptrTime(2*time.Hour + time.Minute), want: stateDegraded},
		{name: "no success in 2x interval", last: ptrTime(48*time.Hour + time.Minute), want: stateDown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := evaluateStatus(&store.StatusInputs{LastCheckSuccess: tt.last, ChecksLastHour: tt.finished, ErrorsLastHour: tt.errs},
				nil, &statusCfg, statusNow)
			if got := component(t, &s, "checks").State; got != tt.want {
				t.Errorf("checks = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestStatus_ServiceThresholds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		component string
		kind      store.ServiceRunKind
		ago       time.Duration
		want      string
	}{
		{name: "discovery never", component: "discovery", want: stateUnknown},
		{name: "discovery fresh", component: "discovery", kind: store.ServiceRunDiscovery, ago: time.Hour, want: stateOperational},
		{name: "discovery over 2x interval", component: "discovery", kind: store.ServiceRunDiscovery, ago: 3 * time.Hour, want: stateDegraded},
		{name: "discovery over 24h", component: "discovery", kind: store.ServiceRunDiscovery, ago: 25 * time.Hour, want: stateDown},
		{name: "snapshot fresh", component: "snapshots", kind: store.ServiceRunSnapshot, ago: 30 * time.Hour, want: stateOperational},
		{name: "snapshot never down", component: "snapshots", kind: store.ServiceRunSnapshot, ago: 72 * time.Hour, want: stateDegraded},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			in := &store.StatusInputs{LastServiceSuccess: map[store.ServiceRunKind]time.Time{}}
			if tt.kind != "" {
				in.LastServiceSuccess[tt.kind] = statusNow.Add(-tt.ago)
			}

			s := evaluateStatus(in, nil, &statusCfg, statusNow)
			if got := component(t, &s, tt.component).State; got != tt.want {
				t.Errorf("%s = %s, want %s", tt.component, got, tt.want)
			}
		})
	}
}

func TestStatus_WebhooksAreInformational(t *testing.T) {
	t.Parallel()

	in := &store.StatusInputs{
		LastCheckSuccess: ptrTime(time.Minute), LastWebhookCheck: ptrTime(25 * time.Hour), ActiveRepositories: 3,
		LastServiceSuccess: map[store.ServiceRunKind]time.Time{
			store.ServiceRunDiscovery: statusNow.Add(-time.Minute), store.ServiceRunSnapshot: statusNow.Add(-time.Minute),
		},
		Rates: []store.RateSnapshot{{Limit: 5000, Remaining: 4000, ResetAt: statusNow.Add(time.Hour)}},
	}

	s := evaluateStatus(in, nil, &statusCfg, statusNow)
	if got := component(t, &s, "webhooks").State; got != stateDegraded {
		t.Errorf("webhooks after 24h quiet = %s, want degraded", got)
	}

	if s.State != stateOperational {
		t.Errorf("overall = %s, want operational: webhooks are informational", s.State)
	}

	in.ActiveRepositories = 0

	s = evaluateStatus(in, nil, &statusCfg, statusNow)
	if got := component(t, &s, "webhooks").State; got != stateOperational {
		t.Errorf("webhooks with no active repositories = %s, want operational", got)
	}
}

func TestStatus_BudgetThresholds(t *testing.T) {
	t.Parallel()

	future, past := statusNow.Add(time.Hour), statusNow.Add(-time.Minute)
	ok := store.RateSnapshot{Limit: 5000, Remaining: 600, ResetAt: future}
	low := store.RateSnapshot{Limit: 5000, Remaining: 400, ResetAt: future}
	lowButReset := store.RateSnapshot{Limit: 5000, Remaining: 0, ResetAt: past}

	tests := []struct {
		name  string
		rates []store.RateSnapshot
		want  string
	}{
		{name: "no snapshot", want: stateUnknown},
		{name: "within budget", rates: []store.RateSnapshot{ok, ok}, want: stateOperational},
		{name: "one below reserve", rates: []store.RateSnapshot{ok, low}, want: stateDegraded},
		{name: "all below reserve", rates: []store.RateSnapshot{low, low}, want: stateDown},
		{name: "reset already passed", rates: []store.RateSnapshot{lowButReset}, want: stateOperational},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := evaluateStatus(&store.StatusInputs{Rates: tt.rates}, nil, &statusCfg, statusNow)
			if got := component(t, &s, "github_budget").State; got != tt.want {
				t.Errorf("github_budget = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestStatus_BacklogThresholds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		backlog *backlogResult
		want    string
	}{
		{name: "probe failed", backlog: &backlogResult{err: errCursor}, want: stateUnknown},
		{name: "no pollers", backlog: &backlogResult{Backlog: Backlog{Pollers: 0}}, want: stateDown},
		{name: "old backlog", backlog: &backlogResult{Backlog: Backlog{Pollers: 2, Age: 16 * time.Minute}}, want: stateDegraded},
		{name: "healthy", backlog: &backlogResult{Backlog: Backlog{Pollers: 2, Age: time.Minute}}, want: stateOperational},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := evaluateStatus(&store.StatusInputs{}, tt.backlog, &statusCfg, statusNow)
			if got := component(t, &s, "backlog").State; got != tt.want {
				t.Errorf("backlog = %s, want %s", got, tt.want)
			}
		})
	}

	if s := evaluateStatus(&store.StatusInputs{}, nil, &statusCfg, statusNow); len(s.Components) != 5 {
		t.Errorf("without a probe there are %d components, want 5 (no backlog)", len(s.Components))
	}
}

func TestStatus_OverallIsWorstMeasured(t *testing.T) {
	t.Parallel()

	tests := []struct {
		states []string
		want   string
	}{
		{states: []string{stateUnknown, stateUnknown}, want: stateUnknown},
		{states: []string{stateUnknown, stateOperational}, want: stateOperational},
		{states: []string{stateOperational, stateDegraded}, want: stateDegraded},
		{states: []string{stateDegraded, stateDown, stateOperational}, want: stateDown},
	}

	for _, tt := range tests {
		var cs []gen.Component
		for _, st := range tt.states {
			cs = append(cs, gen.Component{Name: "checks", State: st})
		}

		if got := overallState(cs); got != tt.want {
			t.Errorf("overall(%v) = %s, want %s", tt.states, got, tt.want)
		}
	}
}

func TestStatus_ComplianceIsUnmeasuredWithoutFindings(t *testing.T) {
	t.Parallel()

	s := evaluateStatus(&store.StatusInputs{}, nil, &statusCfg, statusNow)
	if s.Compliance.Measured || s.Compliance.Percent != nil {
		t.Errorf("compliance with no findings = %+v, want unmeasured", s.Compliance)
	}

	s = evaluateStatus(&store.StatusInputs{Compliance: store.StatusCounts{Compliant: 2, NonCompliant: 1}}, nil, &statusCfg, statusNow)
	if !s.Compliance.Measured || s.Compliance.Percent == nil || *s.Compliance.Percent != 66.6 {
		t.Errorf("compliance = %+v, want 66.6", s.Compliance)
	}
}

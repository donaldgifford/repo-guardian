package api

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/api/gen"
	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/store"
)

// Component states, worst last.
const (
	stateOperational = "operational"
	stateDegraded    = "degraded"
	stateDown        = "down"
	stateUnknown     = "unknown"
)

// Status-page thresholds (DESIGN-0027 § Status page).
const (
	statusRefresh        = 30 * time.Second
	checkErrorRatio      = 0.10
	checkStaleAfter      = 2 * time.Hour
	webhookQuietAfter    = 24 * time.Hour
	discoveryDownAfter   = 24 * time.Hour
	backlogDegradedAfter = 15 * time.Minute
)

// StatusReader reads the status page's inputs. *postgres.APIReader
// satisfies it.
type StatusReader interface {
	StatusInputs(ctx context.Context, now time.Time) (*store.StatusInputs, error)
}

// Backlog is the workflow task queue as Temporal describes it.
type Backlog struct {
	// Age is how long the oldest task has waited.
	Age     time.Duration
	Pollers int
}

// BacklogProbe describes the work backlog; it must be read-only.
type BacklogProbe func(ctx context.Context) (Backlog, error)

// StatusConfig configures a StatusPage.
type StatusConfig struct {
	Reader StatusReader
	// Backlog is optional; without it the backlog component is omitted.
	Backlog BacklogProbe

	CheckInterval     time.Duration
	DiscoveryInterval time.Duration
	SnapshotInterval  time.Duration
	// RateReserve is the fraction of an installation's rate limit held
	// in reserve (RATE_LIMIT_THRESHOLD).
	RateReserve float64

	Logger *slog.Logger
	Now    func() time.Time
}

// StatusPage is the public status page's cache. Only its refresher
// queries; a request reads the last computed page and never touches the
// database, so the endpoint is cheap to hit.
type StatusPage struct {
	cfg     StatusConfig
	current atomic.Pointer[gen.Status]
}

// NewStatusPage returns a page that reads unknown until its first
// refresh.
func NewStatusPage(cfg *StatusConfig) *StatusPage {
	c := *cfg
	if c.Now == nil {
		c.Now = time.Now
	}

	return &StatusPage{cfg: c}
}

// Current returns the last computed page, or an unknown page before the
// first refresh.
func (p *StatusPage) Current() gen.Status {
	if s := p.current.Load(); s != nil {
		return *s
	}

	return gen.Status{State: stateUnknown, UpdatedAt: p.cfg.Now().UTC(), Components: []gen.Component{}}
}

// Run refreshes the page now and every 30s until ctx ends. A failed
// refresh keeps the previous page, whose updated_at then ages.
func (p *StatusPage) Run(ctx context.Context) {
	t := time.NewTicker(statusRefresh)
	defer t.Stop()

	for {
		if err := p.Refresh(ctx); err != nil && ctx.Err() == nil {
			p.cfg.Logger.Warn("status page refresh failed", "error", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Refresh recomputes the page.
func (p *StatusPage) Refresh(ctx context.Context) (err error) {
	defer func(start time.Time) {
		outcome := "ok"
		if err != nil {
			outcome = "error"
		}

		metrics.APIStatusRefreshSeconds.WithLabelValues(outcome).Observe(time.Since(start).Seconds())
	}(time.Now())

	now := p.cfg.Now()

	in, err := p.cfg.Reader.StatusInputs(ctx, now)
	if err != nil {
		return err
	}

	var backlog *backlogResult
	if p.cfg.Backlog != nil {
		b, err := p.cfg.Backlog(ctx)
		backlog = &backlogResult{Backlog: b, err: err}
	}

	s := evaluateStatus(in, backlog, &p.cfg, now)
	p.current.Store(&s)

	return nil
}

type backlogResult struct {
	Backlog

	err error
}

// evaluateStatus applies DESIGN-0027's component rules to in.
func evaluateStatus(in *store.StatusInputs, backlog *backlogResult, cfg *StatusConfig, now time.Time) gen.Status {
	components := []gen.Component{
		checksComponent(in, cfg, now),
		webhooksComponent(in, now),
		serviceComponent("discovery", in.LastServiceSuccess[store.ServiceRunDiscovery], 2*cfg.DiscoveryInterval, discoveryDownAfter, now),
		serviceComponent("snapshots", in.LastServiceSuccess[store.ServiceRunSnapshot], 2*cfg.SnapshotInterval, 0, now),
		budgetComponent(in.Rates, cfg.RateReserve, now),
	}

	if backlog != nil {
		components = append(components, backlogComponent(backlog))
	}

	return gen.Status{
		State:      overallState(components),
		UpdatedAt:  now.UTC(),
		Components: components,
		Compliance: gen.Compliance{Percent: store.CompliantPercent(in.Compliance), Measured: store.CompliantPercent(in.Compliance) != nil},
	}
}

// informational components never move the overall state: quiet orgs
// send no webhooks, and that is normal.
var informational = map[string]bool{"webhooks": true}

// overallState is the worst measured state; unknown only when nothing
// is measured.
func overallState(components []gen.Component) string {
	rank := map[string]int{stateOperational: 1, stateDegraded: 2, stateDown: 3}
	worst := stateUnknown

	for _, c := range components {
		if informational[c.Name] {
			continue
		}

		if rank[c.State] > rank[worst] {
			worst = c.State
		}
	}

	return worst
}

// span renders a duration as the fixed token used in every component
// detail: "<1m", "42m", "5h" or "3d".
func span(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	}
}

func ago(d time.Duration) string { return span(d) + " ago" }

func checksComponent(in *store.StatusInputs, cfg *StatusConfig, now time.Time) gen.Component {
	c := gen.Component{Name: "checks"}

	if in.LastCheckSuccess == nil {
		c.State, c.Detail = stateUnknown, "no successful check yet"

		return c
	}

	since := now.Sub(*in.LastCheckSuccess)
	c.Detail = "last success " + ago(since)

	errorRatio := 0.0
	if in.ChecksLastHour > 0 {
		errorRatio = float64(in.ErrorsLastHour) / float64(in.ChecksLastHour)
	}

	switch {
	case since > 2*cfg.CheckInterval:
		c.State = stateDown
	case errorRatio > checkErrorRatio:
		c.State, c.Detail = stateDegraded, "over 10% of checks failed in the last hour"
	case since > checkStaleAfter:
		c.State = stateDegraded
	default:
		c.State = stateOperational
	}

	return c
}

func webhooksComponent(in *store.StatusInputs, now time.Time) gen.Component {
	c := gen.Component{Name: "webhooks", State: stateOperational}

	switch {
	case in.LastWebhookCheck == nil:
		c.Detail = "no webhook-triggered check yet"
		if in.ActiveRepositories > 0 {
			c.State = stateDegraded
		}
	case now.Sub(*in.LastWebhookCheck) > webhookQuietAfter && in.ActiveRepositories > 0:
		c.State, c.Detail = stateDegraded, "last webhook-triggered check "+ago(now.Sub(*in.LastWebhookCheck))
	default:
		c.Detail = "last webhook-triggered check " + ago(now.Sub(*in.LastWebhookCheck))
	}

	return c
}

// serviceComponent rates a scheduled service by its last success;
// downAfter zero means the service is never down, only degraded.
func serviceComponent(name string, last time.Time, degradedAfter, downAfter time.Duration, now time.Time) gen.Component {
	c := gen.Component{Name: name}

	if last.IsZero() {
		c.State, c.Detail = stateUnknown, "no successful run yet"

		return c
	}

	since := now.Sub(last)
	c.Detail = "last run " + ago(since)

	switch {
	case downAfter > 0 && since > downAfter:
		c.State = stateDown
	case since > degradedAfter:
		c.State = stateDegraded
	default:
		c.State = stateOperational
	}

	return c
}

func budgetComponent(rates []store.RateSnapshot, reserve float64, now time.Time) gen.Component {
	c := gen.Component{Name: "github_budget"}

	if len(rates) == 0 {
		c.State, c.Detail = stateUnknown, "no rate-limit snapshot yet"

		return c
	}

	throttled := 0

	for _, r := range rates {
		if float64(r.Remaining) < reserve*float64(r.Limit) && r.ResetAt.After(now) {
			throttled++
		}
	}

	switch {
	case throttled == len(rates):
		c.State, c.Detail = stateDown, "every installation is below its reserve"
	case throttled > 0:
		c.State, c.Detail = stateDegraded, "an installation is below its reserve"
	default:
		c.State, c.Detail = stateOperational, "every installation is within budget"
	}

	return c
}

func backlogComponent(b *backlogResult) gen.Component {
	c := gen.Component{Name: "backlog"}

	switch {
	case b.err != nil:
		c.State, c.Detail = stateUnknown, "backlog unavailable"
	case b.Pollers == 0:
		c.State, c.Detail = stateDown, "no workers polling"
	case b.Age > backlogDegradedAfter:
		c.State, c.Detail = stateDegraded, "oldest task waiting "+span(b.Age)
	default:
		c.State, c.Detail = stateOperational, "oldest task waiting "+span(b.Age)
	}

	return c
}

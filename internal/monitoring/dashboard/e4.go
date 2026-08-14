package dashboard

import (
	"github.com/donaldgifford/repo-guardian/internal/monitoring"
)

// Log lines this dashboard matches on.
//
// Every one of these is a CONTRACT with the code that emits it, not a
// convenience. A LogQL matcher that stops matching returns no rows, and
// no rows renders identically to "this never happens" — the same
// silent-success failure the whole of DESIGN-0022 exists to remove. The
// catalog-parse line is locked by
// internal/reconciler/log_contract_test.go; the rest are matched on a
// stable prefix so a suffix reword does not break them.
const (
	logCatalogParseFailed = "catalog-info parse failed"
	logRepositoryParked   = "parking repository until discovery sees it again"
	logAttemptCapDropped  = "job exceeded attempt cap"
	logStoreWriteback     = "store write-back failed"
	logRuleStateWriteback = "rule-state write-back failed"
	logDeferringJob       = "rate limit throttled; deferring job"
	logSweepComplete      = "stale-sweep complete"
)

// Webhook rejection lines, as one regex alternation.
//
// Split out because the two layers reject for different reasons and an
// operator needs to tell them apart: the allowlist fires before the HMAC
// check, so a burst of the first with none of the second means the proxy
// header configuration changed, not the secret.
const (
	logRejectedIP      = "rejected request from non-GitHub IP"
	logNoIP            = "could not extract IP from request"
	logInvalidPayload  = "invalid webhook payload"
	logEnqueueFailed   = "failed to enqueue job"
	webhookRejectedRe  = logRejectedIP + "|" + logNoIP + "|" + logInvalidPayload
	webhookIncidentsRe = webhookRejectedRe + "|" + logEnqueueFailed
)

// Log levels as slog's JSON handler writes them.
const (
	levelError = "ERROR"
	levelWarn  = "WARN"
)

// maxLogLines caps every log panel.
//
// Log panels are the one place on these dashboards where the cost of a
// query scales with how bad things are: the panel an operator opens
// during an incident is the one whose stream is busiest. A cap keeps it
// answerable.
const maxLogLines = 200

// e4Loki builds the evidence dashboard.
//
// E1 says a rule is failing, E2 says which org, E3 says whether the
// service is healthy — and none of them can say WHICH repository or WHY,
// because a metric with a repo label would be unbounded cardinality
// (DESIGN-0022 Finding G). That answer only exists in the logs, so this
// dashboard is the fourth tier rather than a nicety: without it the
// other three end at "something is wrong somewhere".
//
// # On the stream selector
//
// Every panel starts from ds.Stream(), which is operator-supplied. It is
// the one input on these dashboards that cannot be verified from inside
// this repository, so the first panel prints it: an empty E4 is far more
// likely to be a label-scheme mismatch than a silent fleet.
func e4Loki(_ *monitoring.Model, ds Datasources) Dashboard {
	b := New("repo-guardian-logs", "repo-guardian — logs",
		"Evidence tier: which repository, and why. Reads Loki, not Prometheus. "+
			"If every panel is empty, check the stream selector before believing the silence.",
		[]string{tagProject, tagGenerated})

	b = withErrorSection(b, ds)
	b = withRepositoryFaultSection(b, ds)
	b = withWritebackSection(b, ds)
	b = withWebhookSection(b, ds)
	b = withSweepLogSection(b, ds)

	return Dashboard{
		Slug:    "repo-guardian-logs",
		Title:   "repo-guardian — logs",
		Folder:  GrafanaFolder,
		Builder: b,
	}
}

// withErrorSection charts the overall log shape.
func withErrorSection(b *Builder, ds Datasources) *Builder {
	stream := ds.Stream()

	return b.
		WithRow(Row("Errors")).
		WithPanel(LogTimeSeries(ds, "Log lines by level",
			"The shape of the log, not its contents. A step change in WARN volume with a flat "+
				"ERROR rate is usually one repository going bad, not the service. Stream selector: "+
				"`"+stream+"` — if this panel is empty, nothing else on the dashboard can work.",
			unitShort, LogQuery{
				Expr:   `sum by (level) (count_over_time(` + stream + ` | json [$__interval]))`,
				Legend: "{{ level }}",
			})).
		WithPanel(LogTimeSeries(ds, "Top error and warning messages",
			"Grouped by message, so a single repeating fault is one line rather than a wall. "+
				"Messages are static strings; the variable parts are structured fields, which is "+
				"what makes this grouping bounded.",
			unitShort, LogQuery{
				Expr: `topk(10,
  sum by (msg) (count_over_time(` + stream + ` | json | level=~"` + levelError + `|` + levelWarn + `" [15m]))
)`,
				Legend: "{{ msg }}",
			})).
		WithPanel(Logs(ds, "Recent errors",
			"Newest first. The first place to look when any alert on E1 or E3 fires.",
			LogQuery{
				Expr:     stream + ` | json | level="` + levelError + `"`,
				MaxLines: maxLogLines,
			}))
}

// withRepositoryFaultSection answers "which repository".
func withRepositoryFaultSection(b *Builder, ds Datasources) *Builder {
	stream := ds.Stream()

	return b.
		WithRow(Row("Repository faults")).
		WithPanel(LogTimeSeries(ds, "Repositories with an unparseable catalog-info",
			"The named counterpart to catalog_parse_failed_total, which says how many but not "+
				"which. A repository here has had its custom properties left alone rather than "+
				"cleared — deliberately (INV-0011 A1), and invisibly until you look here.",
			unitShort, LogQuery{
				Expr: `sum by (owner, repo) (
  count_over_time(` + stream + ` |= "` + logCatalogParseFailed + `" | json [1h])
)`,
				Legend: "{{ owner }}/{{ repo }}",
			})).
		WithPanel(Logs(ds, "catalog-info parse failures",
			"The `err` field says what is wrong with the file. Note the sibling condition — a "+
				"valid non-Component entity — logs at INFO under a different message and is not a "+
				"fault; this panel deliberately does not match it.",
			LogQuery{
				Expr:     stream + ` |= "` + logCatalogParseFailed + `" | json`,
				MaxLines: maxLogLines,
			})).
		WithPanel(LogTimeSeries(ds, "Parked repositories",
			"Repositories the installation can no longer read — archived, forked, transferred or "+
				"un-installed. Parking is why repos_tracked can fall without anything being wrong, "+
				"and this is the only per-repository record of it.",
			unitShort, LogQuery{
				Expr: `sum by (owner, repo, reason) (
  count_over_time(` + stream + ` |= "` + logRepositoryParked + `" | json [1h])
)`,
				Legend: "{{ owner }}/{{ repo }} ({{ reason }})",
			}))
}

// withWritebackSection charts the two failures that make posture lie.
func withWritebackSection(b *Builder, ds Datasources) *Builder {
	stream := ds.Stream()

	return b.
		WithRow(Row("Write-back and job loss")).
		WithPanel(LogTimeSeries(ds, "State write-back failures",
			"Write-back is best-effort by design — the queue is the source of truth for 'did we "+
				"do the work' — so these never fail a job and never appear in errors_total. What "+
				"they do is make every posture gauge on E1 and E2 stale, which is the one failure "+
				"those gauges cannot report about themselves.",
			unitShort,
			LogQuery{
				Expr:   `sum(count_over_time(` + stream + ` |= "` + logStoreWriteback + `" [15m]))`,
				Legend: "repo state",
			},
			LogQuery{
				Expr:   `sum(count_over_time(` + stream + ` |= "` + logRuleStateWriteback + `" [15m]))`,
				Legend: "rule state",
			})).
		WithPanel(LogTimeSeries(ds, "Jobs dropped at the attempt cap",
			"A job that hit MAX_JOB_ATTEMPTS and was acked away rather than retried forever. The "+
				"repository is not lost — the stale sweep is its recovery path — but it will not "+
				"converge until the sweep picks it up.",
			unitShort, LogQuery{
				Expr: `sum by (owner, repo) (
  count_over_time(` + stream + ` |= "` + logAttemptCapDropped + `" | json [1h])
)`,
				Legend: "{{ owner }}/{{ repo }}",
			})).
		WithPanel(Logs(ds, "Deferrals and drops",
			"The rate-limit path end to end. A deferral is not a failure: the check never ran, so "+
				"there is deliberately no error counted and no write-back recorded. Repeated "+
				"deferrals of the same repository are how a job walks to the attempt cap.",
			LogQuery{
				Expr:     stream + ` |~ "` + logDeferringJob + `|` + logAttemptCapDropped + `" | json`,
				MaxLines: maxLogLines,
			}))
}

// withWebhookSection charts what the front door rejected.
func withWebhookSection(b *Builder, ds Datasources) *Builder {
	stream := ds.Stream()

	return b.
		WithRow(Row("Webhook")).
		WithPanel(LogTimeSeries(ds, "Rejected webhook deliveries",
			"Rejections by message. The IP allowlist and the HMAC check are two layers, and which "+
				"one fired matters: an allowlist rejection is usually a proxy misconfiguration "+
				"(TRUST_PROXY_HEADERS behind Tailscale), a signature rejection is a wrong secret.",
			unitShort, LogQuery{
				Expr: `sum by (msg) (
  count_over_time(` + stream + ` |~ "` + webhookRejectedRe + `" | json [15m])
)`,
				Legend: "{{ msg }}",
			})).
		WithPanel(Logs(ds, "Webhook rejections and enqueue failures",
			"An enqueue failure means GitHub was told 202 and the work was then dropped — the "+
				"delivery will not be retried, so the stale sweep is the only path back.",
			LogQuery{
				Expr:     stream + ` |~ "` + webhookIncidentsRe + `" | json`,
				MaxLines: maxLogLines,
			}))
}

// withSweepLogSection charts the sweep's own summary line.
func withSweepLogSection(b *Builder, ds Datasources) *Builder {
	stream := ds.Stream()

	return b.
		WithRow(Row("Sweeps")).
		WithPanel(LogTimeSeries(ds, "Repositories enqueued per sweep",
			"Unwrapped from the sweep's summary line rather than taken from the histogram on E3, "+
				"because this one carries the policy version alongside it: a sudden full-fleet "+
				"batch is almost always a policy edit, and that is only visible here.",
			unitShort, LogQuery{
				Expr:   `max_over_time(` + stream + ` |= "` + logSweepComplete + `" | json | unwrap enqueued [$__interval])`,
				Legend: "enqueued",
			})).
		WithPanel(Logs(ds, "Sweep summaries",
			"One line per sweep: how many rows were stale, how many were enqueued, and the policy "+
				"version that decided. `stale` far exceeding `enqueued` means the batch cap is "+
				"truncating and the fleet cannot converge in one interval.",
			LogQuery{
				Expr:     stream + ` |= "` + logSweepComplete + `" | json`,
				MaxLines: maxLogLines,
			}))
}

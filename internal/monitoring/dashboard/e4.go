package dashboard

import (
	"github.com/donaldgifford/repo-guardian/internal/monitoring"
)

// Log lines this dashboard matches on.
//
// Every one of these is a CONTRACT with the code that emits it, not a
// convenience. A LogQL matcher that stops matching returns no rows, and
// no rows renders identically to "this never happens" — the same
// silent-success failure the whole of DESIGN-0022 exists to remove. All
// of them are emitted by internal/activities or internal/workflows,
// which TestLogLines_AreStillEmittedByTheBinary checks; they are matched
// on a stable prefix so a suffix reword does not break them.
const (
	logRepositoryParked   = "parking repository until discovery sees it again"
	logEvaluationDeferred = "rate limit throttled; deferring evaluation"
	logEvaluationFailed   = "evaluation failed after retries"
	logAwaitsResolution   = "assignment awaits re-resolution"
	logDiscoveryPartial   = "controls discovery: installation listing incomplete"
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
// E1 says a control is failing, E2 says which org, E3 says whether the
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
func e4Loki(_ *monitoring.Model, ds Datasources, id Identity) Dashboard {
	b := New(id.slug(kindLogs), id.title(labelLogs),
		"Evidence tier: which repository, and why. Reads Loki, not Prometheus. "+
			"If every panel is empty, check the stream selector before believing the silence.",
		[]string{tagProject, tagGenerated})

	b = withErrorSection(b, ds)
	b = withRepositoryFaultSection(b, ds)
	b = withEvaluationLogSection(b, ds)

	return id.dashboard(kindLogs, labelLogs, b)
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
		WithPanel(LogTimeSeries(ds, "Parked repositories",
			"Repositories the Evaluation App can no longer read — archived, forked, transferred "+
				"or un-installed. Parking is why assignments can fall without anything being "+
				"wrong, and this is the only per-repository record of it.",
			unitShort, LogQuery{
				Expr: `sum by (repository_id, reason) (
  count_over_time(` + stream + ` |= "` + logRepositoryParked + `" | json [1h])
)`,
				Legend: "{{ repository_id }} ({{ reason }})",
			})).
		WithPanel(Logs(ds, "Assignments awaiting re-resolution",
			"Stored assignments whose control revision the running policy no longer carries. "+
				"They are skipped, not failed, until the policy rollout re-resolves the "+
				"repository; one that stays here past POLICY_ROLLOUT_WINDOW is stuck.",
			LogQuery{
				Expr:     stream + ` |= "` + logAwaitsResolution + `" | json`,
				MaxLines: maxLogLines,
			})).
		WithPanel(Logs(ds, "Incomplete discovery listings",
			"An installation whose repository listing failed. Discovery parks nothing after an "+
				"incomplete listing, so a removed repository stays assigned until a pass "+
				"completes.",
			LogQuery{
				Expr:     stream + ` |= "` + logDiscoveryPartial + `" | json`,
				MaxLines: maxLogLines,
			}))
}

// withEvaluationLogSection charts the two ways an evaluation does not
// finish: deferred to the installation's budget reset, or failed after
// its activity retries. The EvaluationWorkflow tries again either way,
// so neither is lost work, but a repository that keeps appearing here is
// not converging.
func withEvaluationLogSection(b *Builder, ds Datasources) *Builder {
	stream := ds.Stream()

	return b.
		WithRow(Row("Deferred and failed evaluations")).
		WithPanel(LogTimeSeries(ds, "Deferred and failed evaluations",
			"A deferral is not a failure: the evaluation never ran, so there is deliberately no "+
				"error counted and no result recorded. A failed evaluation exhausted its activity "+
				"retries and waits for the next interval.",
			unitShort,
			LogQuery{
				Expr:   `sum(count_over_time(` + stream + ` |= "` + logEvaluationDeferred + `" [15m]))`,
				Legend: "deferred",
			},
			LogQuery{
				Expr:   `sum(count_over_time(` + stream + ` |= "` + logEvaluationFailed + `" [15m]))`,
				Legend: "failed",
			})).
		WithPanel(Logs(ds, "Deferrals and failures",
			"Deferrals carry owner and repo; failures carry the `check_key`, whose first "+
				"segment names the repository's EvaluationWorkflow. Repeated failures of one "+
				"repository are a repository that will not converge.",
			LogQuery{
				Expr:     stream + ` |~ "` + logEvaluationDeferred + `|` + logEvaluationFailed + `" | json`,
				MaxLines: maxLogLines,
			}))
}

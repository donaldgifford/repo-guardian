package dashboard

import (
	"github.com/donaldgifford/repo-guardian/internal/monitoring"
)

// Units used by the system dashboard.
const (
	unitSeconds = "s"
	unitBytes   = "bytes"
	unitNanos   = "ns"
)

// e3System builds the system dashboard.
//
// Service and infrastructure tiers only — no compliance number appears
// on it. That separation is the Finding I taxonomy doing its job: when
// a business panel and a service panel share a screen and the picture
// looks wrong, there is no way to tell which half is lying.
//
// # On the series names
//
// Every OTEL series charted here was read off a live scrape rather than
// inferred from the semconv spec, because the bridge's exposition names
// are not what the spec's metric names suggest. otelpgx publishes the
// pgx-native `pgxpool_*` family, not `db.client.connection.*`; redisotel
// publishes `db_client_connections_*` with `db_system="redis"`; and the
// two carry different pool-name labels (`pool_name` versus
// `db_client_connection_pool_name`). A panel built from the spec would
// render empty and look like a quiet, healthy service. The Temporal SDK
// series are the names the alert catalogue already charts; IMPL-0029
// task 6.6 checks them against a live scrape.
func e3System(_ *monitoring.Model, ds Datasources, id Identity) Dashboard {
	b := New(id.slug(kindSystem), id.title(labelSystem),
		"Service and infrastructure health. No compliance numbers: those are E1's and E2's.",
		[]string{tagProject, tagGenerated})

	b = withHTTPSection(b, ds)
	b = withTemporalSection(b, ds)
	b = withEvaluationSection(b, ds)
	b = withBudgetSection(b, ds)
	b = withStoreSection(b, ds)
	b = withAPISection(b, ds)
	b = withRuntimeSection(b, ds)

	return id.dashboard(kindSystem, labelSystem, b)
}

// withHTTPSection charts the semconv HTTP server and client.
func withHTTPSection(b *Builder, ds Datasources) *Builder {
	return b.
		WithRow(Row("HTTP")).
		WithPanel(TimeSeries(ds, "Webhook requests by status",
			"Server-side semconv histogram count, keyed on http_route rather than the request "+
				"path — the webhook endpoint is reachable by anyone who finds the hostname, so "+
				"anything derived from the URL would be attacker-minted cardinality.",
			unitShort, Query{
				Expr:   `sum by (http_response_status_code) (rate(http_server_request_duration_seconds_count[5m]))`,
				Legend: "{{ http_response_status_code }}",
			})).
		WithPanel(TimeSeries(ds, "Webhook latency p99",
			"Server-side request duration. A rise here with a flat error rate usually means "+
				"ingest is waiting on Temporal to start the webhook workflow rather than failing.",
			unitSeconds, Query{
				Expr: `histogram_quantile(0.99,
  sum by (le, http_route) (rate(http_server_request_duration_seconds_bucket[5m]))
)`,
				Legend: "{{ http_route }}",
			})).
		WithPanel(TimeSeries(ds, "GitHub API calls by status",
			"Client-side semconv histogram count. otelhttp sits OUTERMOST in the transport "+
				"chain, so a call the rate-limit transport refuses is still counted here with an "+
				"error_type and no status code — that is the throttle signal, and underneath the "+
				"chain it would vanish and read as a traffic lull.",
			unitShort, Query{
				Expr:   `sum by (http_response_status_code) (rate(http_client_request_duration_seconds_count[5m]))`,
				Legend: "{{ http_response_status_code }}",
			})).
		WithPanel(TimeSeries(ds, "GitHub API latency p99",
			"Client-side request duration to GitHub. The usual cause of a rising evaluation duration.",
			unitSeconds, Query{
				Expr: `histogram_quantile(0.99,
  sum by (le) (rate(http_client_request_duration_seconds_bucket[5m]))
)`,
				Legend: legendP99,
			}))
}

// withTemporalSection charts the Temporal SDK's worker-side series.
func withTemporalSection(b *Builder, ds Datasources) *Builder {
	return b.
		WithRow(Row("Temporal")).
		WithPanel(TimeSeries(ds, "Activity schedule-to-start p99",
			"How long an evaluation waited for a worker slot, per task queue. The evaluation "+
				"queue and the remediation queue scale separately, so read them separately.",
			unitSeconds, Query{
				Expr: `histogram_quantile(0.99,
  sum by (le, task_queue) (rate(temporal_activity_schedule_to_start_latency_seconds_bucket[15m]))
)`,
				Legend: "{{ task_queue }}",
			})).
		WithPanel(TimeSeries(ds, "Workflow task schedule-to-start p99",
			"Workflow tasks are short; waiting here means workers are saturated or gone. "+
				"Temporal recommends alerting above 200ms.",
			unitSeconds, Query{
				Expr: `histogram_quantile(0.99,
  sum by (le, task_queue) (rate(temporal_workflow_task_schedule_to_start_latency_seconds_bucket[15m]))
)`,
				Legend: "{{ task_queue }}",
			})).
		WithPanel(TimeSeries(ds, "Free worker task slots",
			"Lowest free slot count per queue and worker type. Zero on every pod is "+
				"saturation; zero on one busy pod is normal.",
			unitShort, Query{
				Expr:   `min by (task_queue, worker_type) (temporal_worker_task_slots_available)`,
				Legend: "{{ task_queue }} {{ worker_type }}",
			})).
		WithPanel(TimeSeries(ds, "Sticky cache forced evictions",
			"More long-lived workflows than the sticky cache holds. Every EvaluationWorkflow "+
				"is one, so this tracks fleet size against the worker cache.",
			unitShort, Query{
				Expr:   `sum(rate(temporal_sticky_cache_total_forced_eviction[15m]))`,
				Legend: "evictions",
			}))
}

// withEvaluationSection charts the evaluator's own counters.
func withEvaluationSection(b *Builder, ds Datasources) *Builder {
	return b.
		WithRow(Row("Evaluation")).
		WithPanel(TimeSeries(ds, "Evaluations by outcome",
			"Evaluations finished per second. A deferral is not here: the evaluation never ran, "+
				"so it has no outcome.",
			unitShort, Query{
				Expr:   `sum by (outcome) (rate(repo_guardian_evaluations_total[15m]))`,
				Legend: "{{ outcome }}",
			})).
		WithPanel(TimeSeries(ds, "Evaluation errors by control",
			"Control evaluations with an error status. One control erroring everywhere is a "+
				"control or permission problem; every control erroring is the evaluator.",
			unitShort, Query{
				Expr:   `sum by (control) (rate(repo_guardian_evaluation_errors_total[15m]))`,
				Legend: legendControl,
			})).
		WithPanel(TimeSeries(ds, "Recorded changes and stale-epoch discards",
			"Changes are results whose generation moved. Discards are staged results the record "+
				"step dropped because the assignment changed underneath the evaluation; a steady "+
				"trickle during a policy rollout is expected, a sustained rate is not.",
			unitShort,
			Query{Expr: `sum(rate(repo_guardian_evaluation_changes_total[15m]))`, Legend: "changes"},
			Query{
				Expr:   `sum by (reason) (rate(repo_guardian_results_discarded_total[15m]))`,
				Legend: "discarded ({{ reason }})",
			})).
		WithPanel(TimeSeries(ds, "Evaluation freshness",
			"Age of a control's previous result when it is re-evaluated. The p50 sits near "+
				"EVAL_INTERVAL by design; a p95 well past it means the fleet is not keeping up.",
			unitSeconds,
			Query{
				Expr:   `histogram_quantile(0.5, sum by (le) (rate(repo_guardian_evaluation_freshness_seconds_bucket[1h])))`,
				Legend: "p50",
			},
			Query{
				Expr:   `histogram_quantile(0.95, sum by (le) (rate(repo_guardian_evaluation_freshness_seconds_bucket[1h])))`,
				Legend: "p95",
			}))
}

// withBudgetSection charts the GitHub rate budget and discovery.
func withBudgetSection(b *Builder, ds Datasources) *Builder {
	return b.
		WithRow(Row("Budget and discovery")).
		WithPanel(TimeSeries(ds, "Budget acquisitions by result",
			"The per-installation budget's answers. optimistic means no rate report yet, so the "+
				"budget granted blind; wait means the evaluation sleeps until the reset.",
			unitShort, Query{
				Expr:   `sum by (result) (rate(repo_guardian_budget_acquire_total[15m]))`,
				Legend: "{{ result }}",
			})).
		WithPanel(TimeSeries(ds, "GitHub API budget remaining",
			"Remaining core budget per installation, as the last response reported it.",
			unitShort, Query{
				Expr:   `min by (installation_id) (repo_guardian_rate_limit_remaining)`,
				Legend: "{{ installation_id }}",
			})).
		WithPanel(TimeSeries(ds, "Repositories discovered",
			"New or reactivated repositories per installation. Discovery is the only path that "+
				"un-parks a repository.",
			unitShort, Query{
				Expr:   `sum by (installation_id) (increase(repo_guardian_repo_discovered_total[1h]))`,
				Legend: "{{ installation_id }}",
			})).
		WithPanel(TimeSeries(ds, "Discovery API calls",
			"Listing calls per endpoint. Discovery is not budget-gated, so this is its whole "+
				"cost against the rate limit.",
			unitShort, Query{
				Expr:   `sum by (endpoint) (increase(repo_guardian_discovery_api_calls_total[1h]))`,
				Legend: "{{ endpoint }}",
			}))
}

// withStoreSection charts Postgres.
func withStoreSection(b *Builder, ds Datasources) *Builder {
	return b.
		WithRow(Row("Store")).
		WithPanel(TimeSeries(ds, "Store query latency p99",
			"repo-guardian's own query timer, which measures the statement rather than the "+
				"connection. Pair it with pool wait: a p99 that rises while pool wait stays flat "+
				"is Postgres, not saturation here.",
			unitSeconds, Query{
				Expr:   `histogram_quantile(0.99, sum by (le, operation) (rate(repo_guardian_store_query_seconds_bucket[15m])))`,
				Legend: "{{ operation }}",
			})).
		WithPanel(TimeSeries(ds, "Store query failure rate",
			"Share of store queries returning an error. The `> 0` keeps an idle store at 'no "+
				"data' rather than a division by zero.",
			unitPercent, Query{Expr: `sum(rate(repo_guardian_store_query_seconds_count{outcome="error"}[10m]))
/
(sum(rate(repo_guardian_store_query_seconds_count[10m])) > 0)`})).
		WithPanel(TimeSeries(ds, "Pool wait p99 by role",
			"Time a store call waited for a connection. Postgres is shared capacity: this is "+
				"where evaluator, remediator and api load meet.",
			unitSeconds, Query{
				Expr:   `histogram_quantile(0.99, sum by (le, role) (rate(repo_guardian_store_pool_wait_seconds_bucket[15m])))`,
				Legend: "{{ role }}",
			})).
		WithPanel(TimeSeries(ds, "pgx pool connections",
			"otelpgx publishes the pgx-native pgxpool_* family rather than the "+
				"db.client.connection.* semconv one, and keys it on "+
				"db_client_connection_pool_name. Acquired approaching max is the saturation signal.",
			unitShort,
			Query{Expr: `max(pgxpool_acquired_connections)`, Legend: "acquired"},
			Query{Expr: `max(pgxpool_idle_connections)`, Legend: "idle"},
			Query{Expr: `max(pgxpool_max_connections)`, Legend: "max"})).
		WithPanel(TimeSeries(ds, "pgx acquire wait",
			"Time spent waiting for a connection, as a rate of the cumulative nanosecond "+
				"counter. Non-zero and rising means the pool is the bottleneck.",
			unitNanos, Query{
				Expr:   `sum(rate(pgxpool_empty_acquire_wait_time_nanoseconds_total[5m]))`,
				Legend: "wait",
			}))
}

// withAPISection charts the read-only API.
func withAPISection(b *Builder, ds Datasources) *Builder {
	return b.
		WithRow(Row("API")).
		WithPanel(TimeSeries(ds, "API authentication failures",
			"Refused requests by reason. A burst of one reason (issuer, audience, signature) "+
				"after an IdP change is a configuration mismatch, not an attack.",
			unitShort, Query{
				Expr:   `sum by (reason) (rate(repo_guardian_api_auth_failures_total[15m]))`,
				Legend: legendReason,
			})).
		WithPanel(TimeSeries(ds, "Status page refresh p99",
			"The background read behind /status. The page itself never queries, so a slow "+
				"refresh ages the page rather than slowing requests.",
			unitSeconds, Query{
				Expr:   `histogram_quantile(0.99, sum by (le, outcome) (rate(repo_guardian_api_status_refresh_seconds_bucket[15m])))`,
				Legend: "{{ outcome }}",
			}))
}

// withRuntimeSection charts the process.
func withRuntimeSection(b *Builder, ds Datasources) *Builder {
	return b.
		WithRow(Row("Runtime")).
		WithPanel(TimeSeries(ds, "Goroutines",
			"A count that only grows is a leak. Worth watching after any change to worker "+
				"concurrency.",
			unitShort, Query{Expr: `max by (pod) (go_goroutines)`, Legend: "{{ pod }}"})).
		WithPanel(TimeSeries(ds, "Resident memory",
			"Process RSS per replica. The evaluator's grows with the sticky workflow cache.",
			unitBytes, Query{
				Expr:   `max by (pod) (process_resident_memory_bytes)`,
				Legend: "{{ pod }}",
			}))
}

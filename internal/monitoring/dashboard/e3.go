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
// render empty and look like a quiet, healthy service.
func e3System(_ *monitoring.Model, ds Datasources) Dashboard {
	b := New("repo-guardian-system", "repo-guardian — system",
		"Service and infrastructure health. No compliance numbers: those are E1's and E2's.",
		[]string{tagProject, tagGenerated})

	b = withHTTPSection(b, ds)
	b = withQueueSection(b, ds)
	b = withStoreSection(b, ds)
	b = withRuntimeSection(b, ds)

	return Dashboard{
		Slug:    "repo-guardian-system",
		Title:   "repo-guardian — system",
		Folder:  GrafanaFolder,
		Builder: b,
	}
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
			"Server-side request duration. A rise here with a flat error rate usually means the "+
				"handler is blocking on the queue rather than failing.",
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
			"Client-side request duration to GitHub. The usual cause of a rising check duration.",
			unitSeconds, Query{
				Expr: `histogram_quantile(0.99,
  sum by (le) (rate(http_client_request_duration_seconds_bucket[5m]))
)`,
				Legend: legendP99,
			}))
}

// withQueueSection charts the durable queue, USE-style.
func withQueueSection(b *Builder, ds Datasources) *Builder {
	return b.
		WithRow(Row("Queue")).
		WithPanel(TimeSeries(ds, "Queue depth",
			"Jobs waiting in each key. `jobs` is the ready list; a depth that only grows means "+
				"workers are not keeping up.",
			unitShort, Query{
				Expr:   `max by (queue) (repo_guardian_queue_depth)`,
				Legend: "{{ queue }}",
			})).
		WithPanel(TimeSeries(ds, "Delayed (deferred) jobs",
			"Jobs parked in the delayed ZSET awaiting promotion. Sustained depth is rate-limit "+
				"backpressure: work is not being lost, but the fleet is not converging either. "+
				"Promotion lags due-time by up to one REAPER_INTERVAL by design.",
			unitShort, Query{Expr: `max(repo_guardian_queue_delayed_depth)`, Legend: "delayed"})).
		WithPanel(TimeSeries(ds, "Deferrals by reason",
			"Why jobs were parked. A deferral is not a failure — the check never ran, so it is "+
				"deliberately absent from errors_total and from the repo's write-back.",
			unitShort, Query{
				Expr:   `sum by (reason) (rate(repo_guardian_queue_delayed_total[15m]))`,
				Legend: "{{ reason }}",
			})).
		WithPanel(TimeSeries(ds, "Reaps and exhausted jobs",
			"Reaps mean workers died mid-job or exceeded JOB_ACK_TIMEOUT, and every reap "+
				"duplicates work against an API budget that is probably already tight. Exhausted "+
				"means a job hit MAX_JOB_ATTEMPTS and was dropped; the stale sweep is its "+
				"recovery path.",
			unitShort,
			Query{Expr: `sum(rate(repo_guardian_queue_reaped_total[15m]))`, Legend: "reaped"},
			Query{Expr: `sum(rate(repo_guardian_queue_attempts_exhausted_total[15m]))`, Legend: "exhausted"})).
		WithPanel(TimeSeries(ds, "Queue wait p99",
			"How long a job sat before a worker claimed it.",
			unitSeconds, Query{
				Expr:   `histogram_quantile(0.99, sum by (le) (rate(repo_guardian_queue_wait_seconds_bucket[15m])))`,
				Legend: legendP99,
			})).
		WithPanel(TimeSeries(ds, "Valkey connection pool",
			"redisotel's semconv pool gauge. Note db_system is \"redis\" — the library reports "+
				"the protocol, not the server, and repo-guardian runs Valkey.",
			unitShort, Query{
				Expr:   `sum by (state) (db_client_connections_usage{db_system="redis"})`,
				Legend: "{{ state }}",
			}))
}

// withStoreSection charts Postgres and the state write-back.
func withStoreSection(b *Builder, ds Datasources) *Builder {
	return b.
		WithRow(Row("Store")).
		WithPanel(TimeSeries(ds, "Store query latency p99",
			"repo-guardian's own query timer, which measures the statement rather than the "+
				"connection. Pair it with the pool panels: a p99 that rises while acquisitions "+
				"stay flat is Postgres, not saturation here.",
			unitSeconds, Query{
				Expr:   `histogram_quantile(0.99, sum by (le, query) (rate(repo_guardian_store_query_seconds_bucket[15m])))`,
				Legend: "{{ query }}",
			})).
		WithPanel(TimeSeries(ds, "Store query failure rate",
			"Share of store queries returning an error. Backs RepoGuardianStoreQueryErrors.",
			unitPercent, Query{Expr: `sum(rate(repo_guardian_store_query_seconds_count{outcome="error"}[10m]))
/
clamp_min(sum(rate(repo_guardian_store_query_seconds_count[10m])), 1)`})).
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
			})).
		WithPanel(TimeSeries(ds, "State write-back outcomes",
			"Every processed job writes its result back best-effort. Errors here never fail a "+
				"job — the queue is the source of truth for 'did we do the work' — but a "+
				"sustained error rate means posture is drifting from reality.",
			unitShort, Query{
				Expr:   `sum by (outcome) (rate(repo_guardian_store_writeback_total[15m]))`,
				Legend: "{{ outcome }}",
			})).
		WithPanel(TimeSeries(ds, "Write-back latency p99",
			"Should sit well under 50ms; it is on the job's critical path.",
			unitSeconds, Query{
				Expr: `histogram_quantile(0.99,
  sum by (le) (rate(repo_guardian_store_writeback_duration_seconds_bucket[15m]))
)`,
				Legend: legendP99,
			}))
}

// withRuntimeSection charts the process, the scheduler and the two
// sweep-shaped histograms that had no panel until now.
func withRuntimeSection(b *Builder, ds Datasources) *Builder {
	return b.
		WithRow(Row("Runtime and sweeps")).
		WithPanel(TimeSeries(ds, "Goroutines",
			"A count that only grows is a leak. Worth watching after any change to the worker "+
				"pool or the reaper.",
			unitShort, Query{Expr: `max by (pod) (go_goroutines)`, Legend: "{{ pod }}"})).
		WithPanel(TimeSeries(ds, "Resident memory",
			"Process RSS per replica.",
			unitBytes, Query{
				Expr:   `max by (pod) (process_resident_memory_bytes)`,
				Legend: "{{ pod }}",
			})).
		WithPanel(TimeSeries(ds, "Scheduler leadership",
			"1 where a replica holds a named lock. Aggregated with max by (name), never sum: "+
				"during failover both replicas can briefly hold a series, and summing reports "+
				"two leaders where there is one.",
			unitShort, Query{
				Expr:   `max by (name) (repo_guardian_scheduler_is_leader)`,
				Legend: "{{ name }}",
			})).
		WithPanel(TimeSeries(ds, "Stale-sweep batch size",
			"Repositories enqueued per sweep. A batch pinned at the configured cap means the "+
				"sweep is truncating and the fleet is not converging within one interval.",
			unitShort, Query{
				Expr: `histogram_quantile(0.95,
  sum by (le) (rate(repo_guardian_scheduler_sweep_batch_size_bucket[1h]))
)`,
				Legend: "p95",
			})).
		WithPanel(TimeSeries(ds, "Discovery duration p99",
			"How long one discovery pass takes to enumerate installations and repositories.",
			unitSeconds, Query{
				Expr:   `histogram_quantile(0.99, sum by (le) (rate(repo_guardian_discovery_duration_seconds_bucket[1h])))`,
				Legend: legendP99,
			})).
		WithPanel(TimeSeries(ds, "Posture export health",
			"Successful and failed posture ticks. Zero successes means every gauge on E1 and E2 "+
				"is frozen but still being served — the one failure those gauges cannot show "+
				"about themselves.",
			unitShort, Query{
				Expr:   `sum by (outcome) (rate(repo_guardian_posture_export_total[1h]))`,
				Legend: "{{ outcome }}",
			})).
		WithPanel(TimeSeries(ds, "Posture export duration p99",
			"The store read behind the posture gauges. A slow read delays every number on E1.",
			unitSeconds, Query{
				Expr: `histogram_quantile(0.99,
  sum by (le) (rate(repo_guardian_posture_export_duration_seconds_bucket[1h]))
)`,
				Legend: legendP99,
			}))
}

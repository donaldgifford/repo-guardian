package observability

import (
	"fmt"

	"github.com/redis/go-redis/extra/redisotel/v9"
	"github.com/redis/go-redis/v9"
)

// InstrumentValkey attaches semconv client metrics to a Valkey client.
//
// This is the first measurement of Valkey command latency in the
// system. Everything below the queue and scheduler abstractions was
// previously invisible: the reaper's Lua scripts, the leader-election
// SETNX, and the BRPOP the worker pool blocks on all round-trip through
// this client, and a slow or flapping Valkey showed up only as
// second-order weirdness in job throughput.
//
// # One client, both subsystems
//
// The queue and the scheduler share a single *redis.Client by design
// (main.go's newScheduler takes the queue's client — an IMPL-0011 Phase
// 4 decision), so one call here covers both. IMPL-0023 task 3.4 says
// "both Valkey clients"; there has only ever been one.
//
// # Metrics only, and no goroutine
//
// Tracing is deliberately not instrumented — spans go nowhere in this
// build, and redisotel's tracing carries a per-command overhead that
// buys nothing until a tracing backend exists.
//
// redisotel spawns a background goroutine ONLY when given a close
// channel, whose job is to unregister the metric callbacks. This client
// lives for the whole process and its callbacks die with the meter
// provider at shutdown, so no channel is passed and no goroutine is
// created — the alternative would be a goroutine per instrumented
// client whose only purpose is to tidy up immediately before exit.
func InstrumentValkey(client redis.UniversalClient) error {
	if err := redisotel.InstrumentMetrics(client); err != nil {
		return fmt.Errorf("observability: instrument valkey: %w", err)
	}

	return nil
}

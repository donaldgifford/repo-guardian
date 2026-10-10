// Package store defines repo-guardian's persistent state: the v2 domain
// types (repositories, findings, checks, events), the Writer the worker
// role records through, the Reader and API views the api role reads,
// and the shared compliance math.
//
// The implementation lives in store/postgres. The v1 Store (repo_state
// and rule_state, read by the stale sweep and posture exporter) was
// deleted with the v1 runtime in IMPL-0028 Phase 1.
package store

import "errors"

// ErrNotFound indicates the requested record is not in the store.
// Implementations MUST return this exact sentinel (or wrap it via
// fmt.Errorf with %w) so callers can `errors.Is(err, store.ErrNotFound)`.
var ErrNotFound = errors.New("not found")

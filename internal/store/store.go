// Package store defines repo-guardian's persistent state: the Controls
// interface the evaluation role records through, the Remediations
// interface the remediation role reserves, claims and records through,
// the repository and timeline views the api role reads, and the shared
// compliance math.
//
// The implementation lives in store/postgres, over the controls schema
// chain (migrations_controls). The rc line's findings store and its v1
// predecessor were deleted in IMPL-0029 Phase 7.
package store

import "errors"

// ErrNotFound indicates the requested record is not in the store.
// Implementations MUST return this exact sentinel (or wrap it via
// fmt.Errorf with %w) so callers can `errors.Is(err, store.ErrNotFound)`.
var ErrNotFound = errors.New("not found")

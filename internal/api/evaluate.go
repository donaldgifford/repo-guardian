package api

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/api/gen"
)

// recheckWindow is how long a re-check request suppresses the next for
// the same repository (DESIGN-0032 D9).
const recheckWindow = time.Minute

// The 409 problem types of POST /repositories/{id}/evaluate.
const (
	problemWorkflowMissing = "workflow_missing"
	problemParked          = "parked"
)

// ErrWorkflowMissing is what a Rechecker returns when no evaluation
// workflow is running for the repository: answered 409 workflow_missing.
var ErrWorkflowMissing = errors.New("no evaluation workflow is running")

// Rechecker sends a repository's evaluation workflow a re-check at the
// highest priority. It is the api role's only write, and it writes
// nothing to the store.
type Rechecker interface {
	Recheck(ctx context.Context, repositoryID int64) error
}

// recheckDedupe allows one re-check per repository per recheckWindow in
// this replica (OQ12 (a)): with N replicas a repository is signalled at
// most N times a minute, and the workflow coalesces signals anyway.
type recheckDedupe struct {
	mu   sync.Mutex
	last map[int64]time.Time
}

func newRecheckDedupe() *recheckDedupe {
	return &recheckDedupe{last: make(map[int64]time.Time)}
}

// claim reports whether id may be signalled at now, and records it when
// so. Entries past the window are dropped on the way, so the map holds
// at most a minute of requests.
func (d *recheckDedupe) claim(id int64, now time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	for k, at := range d.last {
		if now.Sub(at) >= recheckWindow {
			delete(d.last, k)
		}
	}

	if _, ok := d.last[id]; ok {
		return false
	}

	d.last[id] = now

	return true
}

// release forgets a claim whose signal failed, so a retry is not
// swallowed by the window.
func (d *recheckDedupe) release(id int64) {
	d.mu.Lock()
	defer d.mu.Unlock()

	delete(d.last, id)
}

// EvaluateRepository asks for a repository to be evaluated now
// (DESIGN-0032 § API): visibility first, as for the GET on the same
// repository, then 501 without a Temporal client, 409 for a parked
// repository or a missing workflow, and 202 otherwise, including a
// request the window suppresses.
func (s *server) EvaluateRepository(
	ctx context.Context, req gen.EvaluateRepositoryRequestObject,
) (gen.EvaluateRepositoryResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}

	d, err := s.opts.Reader.Repository(ctx, p.Visible, req.Id)
	if err != nil {
		return nil, readErr(err)
	}

	if s.opts.Rechecker == nil {
		return nil, statusError(http.StatusNotImplemented, "this api replica has no Temporal client")
	}

	if !d.Repository.Active {
		return nil, typedError(http.StatusConflict, problemParked, "the repository is parked")
	}

	if !s.rechecks.claim(req.Id, s.opts.Now()) {
		return gen.EvaluateRepository202Response{}, nil
	}

	if err := s.opts.Rechecker.Recheck(ctx, req.Id); err != nil {
		s.rechecks.release(req.Id)

		if errors.Is(err, ErrWorkflowMissing) {
			return nil, typedError(http.StatusConflict, problemWorkflowMissing, "no evaluation workflow is running for the repository")
		}

		return nil, err
	}

	return gen.EvaluateRepository202Response{}, nil
}

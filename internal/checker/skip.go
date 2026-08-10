package checker

import (
	"errors"
	"fmt"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
)

// Skip reasons. Durable ones park the repo_state row; transient ones do
// not. See SkippedError.
const (
	SkipArchived = "archived"
	SkipFork     = "fork"
	SkipEmpty    = "empty"
)

// SkippedError reports that a repository was skipped before any rule was
// evaluated. It is returned from CheckRepo only for durable conditions,
// so that the worker can park the row instead of paying for the repo on
// every sweep forever.
//
// Durable means the condition is also filtered by discovery, which is
// what makes parking stable: discovery is the only thing that can
// un-park a row, so parking on a condition discovery does NOT filter
// would have the row reactivated on the next discovery pass and parked
// again on the next sweep, churning at the discovery interval rather
// than settling.
//
//	archived  discovery skips it  -> durable
//	fork      discovery skips it  -> durable
//	empty     discovery keeps it  -> NOT durable
//
// The two durable reasons are exact rather than coincidental: this and
// Discoverer.discoverInstallation both read the same
// policyCfg.Guardian.SkipArchived / SkipForks, so with the flag off
// neither skips and nothing is parked, and with it on both agree.
//
// If a skip condition is ever added here, check discoverInstallation
// before marking it durable.
type SkippedError struct {
	Reason string
}

func (e *SkippedError) Error() string {
	return fmt.Sprintf("repository skipped: %s", e.Reason)
}

// AsSkipped reports whether err is a durable skip, and which one.
func AsSkipped(err error) (*SkippedError, bool) {
	var skip *SkippedError
	if errors.As(err, &skip) {
		return skip, true
	}

	return nil, false
}

// skipReason returns why the repository should be skipped, or nil if it
// should be checked. A durable reason is returned as an error by
// CheckRepo; a transient one is only logged.
func (e *Engine) skipReason(repo *ghclient.Repository) (reason string, durable bool) {
	switch {
	case e.skipArchived && repo.Archived:
		return SkipArchived, true
	case e.skipForks && repo.Fork:
		return SkipFork, true
	case !repo.HasBranch || repo.DefaultRef == "":
		// Transient by nature: the first push gives the repo a default
		// branch, and the push webhook enqueues it directly. Parking it
		// would fight discovery, which does not filter empty repos.
		return SkipEmpty, false
	default:
		return "", false
	}
}

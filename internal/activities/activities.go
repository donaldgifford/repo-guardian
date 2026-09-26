// Package activities holds the Temporal activities behind repo-guardian's
// workflows (DESIGN-0026 § Activities, IMPL-0025 OQ12). Activities do
// all the I/O — GitHub, the engine and Postgres — so the workflows
// package can stay deterministic. Their inputs and results are the
// workflows package's payload types, and they register under the
// workflows package's names.
package activities

import (
	"context"
	"log/slog"

	"go.temporal.io/sdk/activity"

	"github.com/donaldgifford/repo-guardian/internal/checker"
	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// Engine runs one repository check. *checker.Engine satisfies it.
type Engine interface {
	CheckRepo(ctx context.Context, client ghclient.Client, owner, repo string) (*checker.CheckResult, error)
}

// Store is the v2 store the activities read and write.
type Store interface {
	store.Writer
	store.Reader
}

// ClientFactory creates installation-scoped GitHub clients. The app
// client (ghclient.Client) satisfies it.
type ClientFactory interface {
	CreateInstallationClient(ctx context.Context, installationID int64) (ghclient.Client, error)
}

// Activities is the set of activities a worker registers. It is safe for
// concurrent use: it holds no per-check state.
type Activities struct {
	engine        Engine
	store         Store
	github        ClientFactory
	policyVersion string
	logger        *slog.Logger
}

// New returns the activities for one worker. policyVersion is the v2
// policy version the worker loaded.
func New(engine Engine, st Store, github ClientFactory, policyVersion string, logger *slog.Logger) *Activities {
	return &Activities{
		engine:        engine,
		store:         st,
		github:        github,
		policyVersion: policyVersion,
		logger:        logger,
	}
}

// Registry is the part of a Temporal worker activities register with.
type Registry interface {
	RegisterActivityWithOptions(a any, options activity.RegisterOptions)
}

// Register registers every activity under its workflows package name.
func (a *Activities) Register(r Registry) {
	r.RegisterActivityWithOptions(a.CheckRepo, activity.RegisterOptions{Name: workflows.CheckRepoActivity})
	r.RegisterActivityWithOptions(a.RecordCheck, activity.RegisterOptions{Name: workflows.RecordCheckActivity})
	r.RegisterActivityWithOptions(a.RecordCheckError, activity.RegisterOptions{Name: workflows.RecordCheckErrorActivity})
	r.RegisterActivityWithOptions(a.Park, activity.RegisterOptions{Name: workflows.ParkActivity})
}

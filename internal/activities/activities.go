// Package activities holds the Temporal activities behind repo-guardian's
// workflows (DESIGN-0032 § Evaluation). Activities do all the I/O —
// GitHub, the controls and Postgres — so the workflows package can stay
// deterministic. Their inputs and results are the workflows package's
// payload types, and they register under the workflows package's names.
package activities

import (
	"context"

	"go.temporal.io/sdk/activity"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
)

// ClientFactory creates installation-scoped GitHub clients. The app
// client (ghclient.Client) satisfies it.
type ClientFactory interface {
	CreateInstallationClient(ctx context.Context, installationID int64) (ghclient.Client, error)
}

// Lister lists the App's installations and an installation's
// repositories. The app client (ghclient.Client) satisfies it.
type Lister interface {
	ListInstallations(ctx context.Context) ([]*ghclient.Installation, error)
	ListInstallationRepos(ctx context.Context, installationID int64) ([]*ghclient.Repository, error)
}

// Registry is the part of a Temporal worker activities register with.
type Registry interface {
	RegisterActivityWithOptions(a any, options activity.RegisterOptions)
}

package dashboard

import (
	"fmt"
	"regexp"

	sdk "github.com/grafana/grafana-foundation-sdk/go/dashboard"

	"github.com/donaldgifford/repo-guardian/internal/monitoring"
)

// Dashboard is one generated dashboard plus the identity the emitter
// needs and the SDK builder does not carry.
type Dashboard struct {
	// Slug is the filename stem AND the GrafanaDashboard CR's
	// metadata.name, so it must satisfy the narrower of the two: RFC
	// 1123. ValidateSuite enforces that.
	Slug string

	// Title is the human name, duplicated here so the emitter can log
	// and index dashboards without building them.
	Title string

	// Folder is the Grafana folder the CR asks the operator to file it
	// under. Empty means the operator's default.
	Folder string

	//nolint:staticcheck // SA1019: schema v1 is what the panel builders produce, see New
	Builder *sdk.DashboardBuilder
}

// Suite returns every dashboard the generator emits, in a fixed order.
//
// This is the whole seam between dashboard content and dashboard
// emission: adding a dashboard is a line here, and the emitter never
// learns how many there are or what they chart.
//
// The four dashboards (E1 KPI, E2 detail, E3 system, E4 Loki) are
// authored in IMPL-0023 Phase 6. Until then this returns nothing, which
// is why the drift gate asserts a non-zero artifact count rather than
// trusting that a green run wrote something — an emitter with an empty
// suite is exactly the vacuous pass promtool's "0 rules found" trap
// taught us to guard against.
func Suite(_ *monitoring.Model, _ Datasources) []Dashboard {
	return nil
}

// rfc1123 is the Kubernetes object-name grammar.
var rfc1123 = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// maxNameLen is the Kubernetes limit for a metadata.name.
const maxNameLen = 253

// ValidateSuite refuses a dashboard whose slug cannot be a filename and
// a Kubernetes object name at once.
//
// Refuses rather than sanitizes, for the reason report.filename does:
// any sanitizing rule can map two distinct slugs onto one name, and the
// second dashboard would then overwrite the first with nothing said.
func ValidateSuite(dashboards []Dashboard) error {
	seen := make(map[string]struct{}, len(dashboards))

	for i := range dashboards {
		d := &dashboards[i]

		switch {
		case d.Slug == "":
			return fmt.Errorf("dashboard: %q has no slug", d.Title)
		case len(d.Slug) > maxNameLen:
			return fmt.Errorf("dashboard: slug %q is longer than %d characters", d.Slug, maxNameLen)
		case !rfc1123.MatchString(d.Slug):
			return fmt.Errorf(
				"dashboard: slug %q is not a valid Kubernetes object name; want lowercase alphanumerics and dashes",
				d.Slug)
		case d.Builder == nil:
			return fmt.Errorf("dashboard: %q has no builder", d.Slug)
		}

		if _, dup := seen[d.Slug]; dup {
			return fmt.Errorf("dashboard: duplicate slug %q", d.Slug)
		}

		seen[d.Slug] = struct{}{}
	}

	return nil
}

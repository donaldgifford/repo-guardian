package dashboard

import (
	"fmt"
	"regexp"

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

	Builder *Builder
}

// Suite returns every dashboard the generator emits, in a fixed order.
//
// This is the whole seam between dashboard content and dashboard
// emission: adding a dashboard is a line here, and the emitter never
// learns how many there are or what they chart.
//
// The four dashboards are the Finding I tiers, in the order an incident
// is actually worked: E1 says a rule is failing, E2 says which
// organisation, E3 says whether the service itself is healthy, and E4
// says which repository and why. E4 is the only one that reads Loki, and
// it is not optional garnish — the "which repository" answer cannot
// exist in a metric, because a repo label would be unbounded
// cardinality (Finding G).
func Suite(m *monitoring.Model, ds Datasources, id Identity) []Dashboard {
	id = id.WithDefaults()

	return []Dashboard{
		e1KPI(m, ds, id),
		e2Detail(m, ds, id),
		e3System(m, ds, id),
		e4Loki(m, ds, id),
	}
}

// Each dashboard's kind (its slug suffix) and title label.
const (
	kindKPI     = "kpi"
	labelKPI    = "KPI"
	kindDetail  = "detail"
	labelDetail = "detail"
	kindSystem  = "system"
	labelSystem = "system"
	kindLogs    = "logs"
	labelLogs   = "logs"
)

// Identity defaults.
const (
	// DefaultName prefixes every dashboard's slug, uid and title.
	DefaultName = "repo-guardian"

	// GrafanaFolder is the folder the generated dashboards ask the
	// operator to file them under.
	GrafanaFolder = "repo-guardian"
)

// Identity names a generated suite, so two installs generated into one
// Grafana (prod and dev, say) get disjoint slugs, uids and titles
// instead of overwriting each other.
type Identity struct {
	// Name prefixes each dashboard's slug (which is also its Grafana
	// uid and its CR metadata.name) and its title.
	Name string

	// Folder is the Grafana folder the CRs ask for.
	Folder string
}

// WithDefaults fills empty fields with the defaults.
func (id Identity) WithDefaults() Identity {
	if id.Name == "" {
		id.Name = DefaultName
	}

	if id.Folder == "" {
		id.Folder = GrafanaFolder
	}

	return id
}

// slug is one dashboard's slug and uid: the name plus its kind.
func (id Identity) slug(kind string) string { return id.Name + "-" + kind }

// title is one dashboard's human name.
func (id Identity) title(label string) string { return id.Name + " — " + label }

// dashboard assembles a Dashboard of this suite.
func (id Identity) dashboard(kind, label string, b *Builder) Dashboard {
	return Dashboard{Slug: id.slug(kind), Title: id.title(label), Folder: id.Folder, Builder: b}
}

// Tags every generated dashboard carries.
//
// tagGenerated is the one that matters: it is how an operator tells a
// dashboard that will be overwritten on the next `monitoring generate`
// from one they are free to edit.
const (
	tagProject   = "repo-guardian"
	tagGenerated = "generated"
)

// legendP99 labels a 99th-percentile series.
const legendP99 = "p99"

// rfc1123 is the Kubernetes object-name grammar.
var rfc1123 = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// maxUIDLen is Grafana's limit for a dashboard uid, which is the slug.
// It is far below the Kubernetes metadata.name limit, so it is the one
// that binds.
const maxUIDLen = 40

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
		case len(d.Slug) > maxUIDLen:
			return fmt.Errorf("dashboard: slug %q is longer than %d characters, Grafana's uid limit; shorten --name",
				d.Slug, maxUIDLen)
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

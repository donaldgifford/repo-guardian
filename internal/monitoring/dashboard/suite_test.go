package dashboard_test

import (
	"strings"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/monitoring"
	"github.com/donaldgifford/repo-guardian/internal/monitoring/dashboard"
)

// TestValidateSuite_AcceptsTheSuite pins that whatever Suite returns is
// emittable.
//
// Phase 6 authors the four dashboards by adding to Suite, and the slug
// doubles as a filename stem and a Kubernetes object name — grammars
// that differ, so an eyeballed slug is not a checked one. This is the
// test that turns a bad Phase 6 slug into a red build rather than a CR
// the API server rejects on apply.
func TestValidateSuite_AcceptsTheSuite(t *testing.T) {
	t.Parallel()

	suite := dashboard.Suite(&monitoring.Model{}, dashboard.Datasources{}.WithDefaults())

	if err := dashboard.ValidateSuite(suite); err != nil {
		t.Errorf("ValidateSuite(Suite(...)) = %v, want nil", err)
	}
}

// TestValidateSuite_Rejections pins what a bad slug costs.
func TestValidateSuite_Rejections(t *testing.T) {
	t.Parallel()

	builder := func() *dashboard.Dashboard {
		return &dashboard.Dashboard{Builder: dashboard.New("x", "X", "", nil)}
	}

	tests := []struct {
		name string
		in   []dashboard.Dashboard
		want string
	}{
		{
			name: "no slug",
			in:   []dashboard.Dashboard{{Title: "First", Builder: dashboard.New("x", "X", "", nil)}},
			want: "no slug",
		},
		{
			name: "underscores are legal in a filename and not in an object name",
			in:   []dashboard.Dashboard{{Slug: "rg_first", Builder: builder().Builder}},
			want: "not a valid Kubernetes object name",
		},
		{
			name: "uppercase",
			in:   []dashboard.Dashboard{{Slug: "RGFirst", Builder: builder().Builder}},
			want: "not a valid Kubernetes object name",
		},
		{
			name: "path separator",
			in:   []dashboard.Dashboard{{Slug: "../escaped", Builder: builder().Builder}},
			want: "not a valid Kubernetes object name",
		},
		{
			name: "trailing dash",
			in:   []dashboard.Dashboard{{Slug: "rg-first-", Builder: builder().Builder}},
			want: "not a valid Kubernetes object name",
		},
		{
			name: "over the length limit",
			in:   []dashboard.Dashboard{{Slug: strings.Repeat("a", 254), Builder: builder().Builder}},
			want: "longer than 253",
		},
		{
			name: "no builder",
			in:   []dashboard.Dashboard{{Slug: "rg-first"}},
			want: "no builder",
		},
		{
			// Two dashboards sharing a slug means the second overwrites
			// the first, silently, in both output formats.
			name: "duplicate slug",
			in: []dashboard.Dashboard{
				{Slug: "rg-first", Builder: builder().Builder},
				{Slug: "rg-first", Builder: builder().Builder},
			},
			want: "duplicate slug",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := dashboard.ValidateSuite(tt.in)
			if err == nil {
				t.Fatalf("ValidateSuite() = nil, want an error containing %q", tt.want)
			}

			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("ValidateSuite() = %v, want an error containing %q", err, tt.want)
			}
		})
	}
}

// TestValidateSuite_AcceptsGoodSlugs pins the positive case.
func TestValidateSuite_AcceptsGoodSlugs(t *testing.T) {
	t.Parallel()

	in := []dashboard.Dashboard{
		{Slug: "e1-kpi", Builder: dashboard.New("e1", "E1", "", nil)},
		{Slug: "e2-detail", Builder: dashboard.New("e2", "E2", "", nil)},
		{Slug: "e3system", Builder: dashboard.New("e3", "E3", "", nil)},
		{Slug: "e4", Builder: dashboard.New("e4", "E4", "", nil)},
	}

	if err := dashboard.ValidateSuite(in); err != nil {
		t.Errorf("ValidateSuite() = %v, want nil", err)
	}
}

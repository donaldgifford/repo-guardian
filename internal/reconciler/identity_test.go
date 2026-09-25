package reconciler

import "testing"

// TestPRIdentity_IsFrozen locks the branch names and titles the custom
// properties reconciler finds its own PRs by. v1 and v2 adopt each
// other's open PRs only while these match (DESIGN-0026 § Cutover from
// v1); do not update a literal to make a change pass.
func TestPRIdentity_IsFrozen(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name, got, want string
	}{
		{"properties branch", PropertiesBranchName, "repo-guardian/set-custom-properties"},
		{"catalog-info branch", CatalogInfoBranchName, "repo-guardian/add-catalog-info"},
		{"properties title", PropertiesPRTitle, "chore: set repository custom properties"},
		{"catalog-info title", CatalogInfoPRTitle, "chore: add catalog-info.yaml"},
	} {
		if tt.got != tt.want {
			t.Errorf("%s = %q, want %q", tt.name, tt.got, tt.want)
		}
	}
}

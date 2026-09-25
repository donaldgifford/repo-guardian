package config

// Role is a v2 process role (DESIGN-0026 § Roles). A process runs one
// or more; `all` runs every one.
type Role uint8

// Roles, combinable as a bit set.
const (
	RoleIngest Role = 1 << iota
	RoleWorker
	RoleAPI

	RoleAll = RoleIngest | RoleWorker | RoleAPI
)

// Has reports whether r includes every role in other.
func (r Role) Has(other Role) bool { return r&other == other }

// ValidateRole checks the configuration a role needs. Until Phase 12's
// per-role rules land it accepts everything.
func (*Config) ValidateRole(Role) error { return nil }

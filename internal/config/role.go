package config

import "errors"

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

// ValidateRole checks the configuration role needs. A process running
// several roles needs the union.
//
// ingest alone is the internet-facing role, so it refuses to start when
// the GitHub App key or STORE_DSN is present: a compromised ingest pod
// must hold neither. That refusal does not apply to all, which runs the
// worker in the same process.
func (c *Config) ValidateRole(role Role) error {
	var errs []error

	// The api role reads Temporal only for the optional status backlog.
	if c.TemporalAddress == "" && (role.Has(RoleIngest) || role.Has(RoleWorker)) {
		errs = append(errs, errors.New("TEMPORAL_ADDRESS is required"))
	}

	if role.Has(RoleIngest) && c.GitHubWebhookSecret == "" {
		errs = append(errs, errors.New("GITHUB_WEBHOOK_SECRET is required for the ingest role"))
	}

	if role == RoleIngest {
		if c.GitHubPrivateKeyPath != "" || c.GitHubPrivateKey != "" {
			errs = append(errs, errors.New("the ingest role must not hold the GitHub App key: unset GITHUB_PRIVATE_KEY_PATH and GITHUB_PRIVATE_KEY"))
		}

		if c.StoreDSN != "" {
			errs = append(errs, errors.New("the ingest role must not hold database credentials: unset STORE_DSN"))
		}
	}

	if role.Has(RoleWorker) {
		errs = append(errs, c.validateWorker()...)
	}

	if role.Has(RoleAPI) {
		errs = append(errs, c.validateAPI(role)...)
	}

	return errors.Join(errs...)
}

func (c *Config) validateWorker() []error {
	var errs []error

	if c.GitHubAppID == 0 {
		errs = append(errs, errors.New("GITHUB_APP_ID is required for the worker role"))
	}

	switch {
	case c.GitHubPrivateKeyPath == "" && c.GitHubPrivateKey == "":
		errs = append(errs, errors.New("one of GITHUB_PRIVATE_KEY_PATH or GITHUB_PRIVATE_KEY is required for the worker role"))
	case c.GitHubPrivateKeyPath != "" && c.GitHubPrivateKey != "":
		errs = append(errs, errors.New("GITHUB_PRIVATE_KEY_PATH and GITHUB_PRIVATE_KEY are mutually exclusive"))
	}

	if c.StoreDSN == "" {
		errs = append(errs, errors.New("STORE_DSN is required for the worker role"))
	}

	if c.GuardianConfigPath == "" {
		errs = append(errs, errors.New("GUARDIAN_CONFIG is required for the worker role"))
	}

	return errs
}

package config

import (
	"errors"
	"os"
	"time"
)

// APIConfig configures the read-only api role (DESIGN-0027).
type APIConfig struct {
	// ListenAddr is API_LISTEN_ADDR; see Config.APIListenAddr.
	ListenAddr string
	// StoreRODSN is STORE_RO_DSN, the read-only role's DSN. Required when
	// the api role runs alone; in all it may fall back to STORE_DSN
	// (DESIGN-0027 OQ11), keeping the read-only session settings.
	StoreRODSN string

	// AuthEnabled is API_AUTH_ENABLED. Default true. Disabled auth grants
	// every caller every org; the chart refuses to render an Ingress so.
	AuthEnabled bool
	// OIDCIssuer and OIDCAudience are OIDC_ISSUER and OIDC_AUDIENCE.
	OIDCIssuer   string
	OIDCAudience string
	// OIDCNameClaim and OIDCGroupsClaim name the display-name and group
	// claims. Defaults "preferred_username" and "groups".
	OIDCNameClaim   string
	OIDCGroupsClaim string
	// AuthzConfigPath is API_AUTHZ_CONFIG, the groups → orgs file.
	AuthzConfigPath string

	// PRStaleAfter is PR_STALE_AFTER: an open repo-guardian PR older
	// than this is stale. Default 720h (DESIGN-0027 OQ14).
	PRStaleAfter time.Duration
}

const defaultPRStaleAfter = 720 * time.Hour

// Listener defaults. The api role alone takes the main port; beside
// other roles it takes the next one.
const (
	defaultListenAddr     = ":8080"
	defaultAPISidecarAddr = ":8081"
)

func loadAPIConfig(cfg *Config) error {
	enabled, err := envOrDefaultBool("API_AUTH_ENABLED", true)
	if err != nil {
		return err
	}

	stale, err := envOrDefaultDuration("PR_STALE_AFTER", defaultPRStaleAfter)
	if err != nil {
		return err
	}

	cfg.API = APIConfig{
		ListenAddr:      os.Getenv("API_LISTEN_ADDR"),
		StoreRODSN:      os.Getenv("STORE_RO_DSN"),
		AuthEnabled:     enabled,
		OIDCIssuer:      os.Getenv("OIDC_ISSUER"),
		OIDCAudience:    os.Getenv("OIDC_AUDIENCE"),
		OIDCNameClaim:   envOrDefault("OIDC_NAME_CLAIM", "preferred_username"),
		OIDCGroupsClaim: envOrDefault("OIDC_GROUPS_CLAIM", "groups"),
		AuthzConfigPath: os.Getenv("API_AUTHZ_CONFIG"),
		PRStaleAfter:    stale,
	}

	return nil
}

// APIListenAddr is API_LISTEN_ADDR, defaulting to ":8080" when the api
// role runs alone (its own pod) and ":8081" beside other roles, where
// the main listener already holds ":8080".
func (c *Config) APIListenAddr(role Role) string {
	switch {
	case c.API.ListenAddr != "":
		return c.API.ListenAddr
	case role == RoleAPI:
		return defaultListenAddr
	default:
		return defaultAPISidecarAddr
	}
}

// APIStoreDSN is the DSN the api role reads with: STORE_RO_DSN, or in
// all STORE_DSN when it is unset.
func (c *Config) APIStoreDSN(role Role) string {
	if c.API.StoreRODSN != "" || role == RoleAPI {
		return c.API.StoreRODSN
	}

	return c.StoreDSN
}

func (c *Config) validateAPI(role Role) []error {
	var errs []error

	if role != RoleAPI && c.APIListenAddr(role) == c.ListenAddr {
		errs = append(errs, errors.New("API_LISTEN_ADDR must differ from LISTEN_ADDR when the api role runs beside others"))
	}

	if c.APIStoreDSN(role) == "" {
		errs = append(errs, errors.New("STORE_RO_DSN is required for the api role"))
	}

	if !c.API.AuthEnabled {
		return errs
	}

	if c.API.OIDCIssuer == "" {
		errs = append(errs, errors.New("OIDC_ISSUER is required for the api role unless API_AUTH_ENABLED=false"))
	}

	if c.API.OIDCAudience == "" {
		errs = append(errs, errors.New("OIDC_AUDIENCE is required for the api role unless API_AUTH_ENABLED=false"))
	}

	if c.API.AuthzConfigPath == "" {
		errs = append(errs, errors.New("API_AUTHZ_CONFIG is required for the api role unless API_AUTH_ENABLED=false"))
	}

	return errs
}

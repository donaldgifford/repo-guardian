package config

import (
	"errors"
	"os"
	"strings"
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
	// OIDCClientClaim is OIDC_CLIENT_CLAIM, the top-level claim naming a
	// machine client for authz `clients` (azp on Keycloak and Auth0, cid
	// on Okta, appid on Entra ID v1). Default "azp"; set but blank is a
	// startup error, never a silent "no client".
	OIDCClientClaim string
	// AuthzConfigPath is API_AUTHZ_CONFIG, the groups → orgs file.
	AuthzConfigPath string

	// StatusPublic is STATUS_PUBLIC, default true: the status page needs
	// no token. False puts it behind authentication like every route.
	StatusPublic bool
}

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

	statusPublic, err := envOrDefaultBool("STATUS_PUBLIC", true)
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
		OIDCClientClaim: clientClaimFromEnv(),
		AuthzConfigPath: os.Getenv("API_AUTHZ_CONFIG"),
		StatusPublic:    statusPublic,
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

	if c.API.OIDCClientClaim == "" {
		errs = append(errs, errors.New("OIDC_CLIENT_CLAIM must name a claim when set (default azp)"))
	}

	if c.API.AuthzConfigPath == "" {
		errs = append(errs, errors.New("API_AUTHZ_CONFIG is required for the api role unless API_AUTH_ENABLED=false"))
	}

	return errs
}

// clientClaimFromEnv reads OIDC_CLIENT_CLAIM, defaulting to "azp" only
// when the variable is unset, so a blank value reaches validation.
func clientClaimFromEnv() string {
	v, ok := os.LookupEnv("OIDC_CLIENT_CLAIM")
	if !ok {
		return "azp"
	}

	return strings.TrimSpace(v)
}

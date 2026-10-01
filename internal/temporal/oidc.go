package temporal

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"

	"github.com/donaldgifford/repo-guardian/internal/metrics"
)

const (
	// tokenRefreshEarly renews a token this long before it expires, so a
	// call never carries one that lapses in flight.
	tokenRefreshEarly = time.Minute
	// tokenFetchTimeout bounds one request to the token endpoint.
	tokenFetchTimeout = 10 * time.Second
)

// OIDCConfig is an OAuth2 client-credentials grant whose access token
// is sent to the frontend as a bearer token, for clusters that
// authorize with JWTs (Keycloak and the like) rather than mTLS.
type OIDCConfig struct {
	// TokenURL is the IdP's token endpoint (TEMPORAL_OIDC_TOKEN_URL).
	TokenURL string
	// ClientID is TEMPORAL_OIDC_CLIENT_ID.
	ClientID string
	// ClientSecretPath is a file holding the client secret
	// (TEMPORAL_OIDC_CLIENT_SECRET_PATH), so the secret is never in the
	// environment.
	ClientSecretPath string
	// Scopes are requested with the token (TEMPORAL_OIDC_SCOPES,
	// space-separated). Optional.
	Scopes []string
	// Audience is sent as the `audience` parameter for IdPs that take
	// one (TEMPORAL_OIDC_AUDIENCE). Keycloak sets the audience with a
	// client-scope mapper instead and ignores it. Optional.
	Audience string
}

// oidcFromEnv reads the TEMPORAL_OIDC_* variables; nil when none is set.
func oidcFromEnv() *OIDCConfig {
	o := &OIDCConfig{
		TokenURL:         os.Getenv("TEMPORAL_OIDC_TOKEN_URL"),
		ClientID:         os.Getenv("TEMPORAL_OIDC_CLIENT_ID"),
		ClientSecretPath: os.Getenv("TEMPORAL_OIDC_CLIENT_SECRET_PATH"),
		Scopes:           strings.Fields(os.Getenv("TEMPORAL_OIDC_SCOPES")),
		Audience:         os.Getenv("TEMPORAL_OIDC_AUDIENCE"),
	}

	if o.TokenURL == "" && o.ClientID == "" && o.ClientSecretPath == "" && len(o.Scopes) == 0 && o.Audience == "" {
		return nil
	}

	return o
}

func (o *OIDCConfig) validate() []error {
	var errs []error

	if o.TokenURL == "" || o.ClientID == "" || o.ClientSecretPath == "" {
		errs = append(errs, errors.New(
			"TEMPORAL_OIDC_TOKEN_URL, TEMPORAL_OIDC_CLIENT_ID and TEMPORAL_OIDC_CLIENT_SECRET_PATH must be set together"))
	}

	if o.TokenURL != "" {
		if u, err := url.Parse(o.TokenURL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			errs = append(errs, fmt.Errorf("TEMPORAL_OIDC_TOKEN_URL %q is not an http(s) URL", o.TokenURL))
		}
	}

	return errs
}

// tokenSource returns a cached, self-renewing source of access tokens.
// The client secret is read here, so a missing file fails startup, and
// again on every token fetch, so a rotated secret is used without a
// restart (DESIGN-0028). Fetches happen once per token lifetime, not
// per call: the reuse wrapper caches the token.
func (o *OIDCConfig) tokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	secret, err := o.readSecret()
	if err != nil {
		return nil, err
	}

	ctx = context.WithValue(ctx, oauth2.HTTPClient, &http.Client{Timeout: tokenFetchTimeout})

	return oauth2.ReuseTokenSourceWithExpiry(nil, &secretFileSource{ctx: ctx, oidc: o, last: secret}, tokenRefreshEarly), nil
}

// readSecret reads and trims the client secret file.
func (o *OIDCConfig) readSecret() (string, error) {
	secret, err := os.ReadFile(o.ClientSecretPath)
	if err != nil {
		return "", fmt.Errorf("temporal: reading OIDC client secret: %w", err)
	}

	return strings.TrimSpace(string(secret)), nil
}

// secretFileSource fetches a client-credentials token with the secret
// currently on disk.
type secretFileSource struct {
	ctx  context.Context //nolint:containedctx // oauth2.TokenSource has no context parameter; this carries the HTTP client
	oidc *OIDCConfig

	mu   sync.Mutex
	last string // the secret used for the previous fetch
}

// Token reads the secret and fetches a token. A read failure fails the
// fetch: the SDK retries, and a cached token keeps working until it
// expires.
func (s *secretFileSource) Token() (*oauth2.Token, error) {
	secret, err := s.oidc.readSecret()
	if err != nil {
		metrics.TemporalCredentialReloadsTotal.WithLabelValues(metrics.CredentialOIDCSecret, metrics.OutcomeError).Inc()

		return nil, err
	}

	s.mu.Lock()
	if secret != s.last {
		s.last = secret
		metrics.TemporalCredentialReloadsTotal.WithLabelValues(metrics.CredentialOIDCSecret, metrics.OutcomeChanged).Inc()
	}
	s.mu.Unlock()

	cc := &clientcredentials.Config{
		ClientID:     s.oidc.ClientID,
		ClientSecret: secret,
		TokenURL:     s.oidc.TokenURL,
		Scopes:       s.oidc.Scopes,
	}

	if s.oidc.Audience != "" {
		cc.EndpointParams = url.Values{"audience": {s.oidc.Audience}}
	}

	return cc.Token(s.ctx)
}

// tokenCallback adapts ts to the SDK's API-key credentials, which send
// the returned value as `Authorization: Bearer <token>` on every call.
func tokenCallback(ts oauth2.TokenSource, logger *slog.Logger) func(context.Context) (string, error) {
	return func(context.Context) (string, error) {
		tok, err := ts.Token()
		if err != nil {
			logger.Warn("temporal: fetching OIDC access token failed", "error", err)

			return "", fmt.Errorf("temporal: fetching OIDC access token: %w", err)
		}

		return tok.AccessToken, nil
	}
}

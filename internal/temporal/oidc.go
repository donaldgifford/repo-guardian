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
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
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
// The client secret is read once, here; rotating it needs a restart,
// like the TLS files.
func (o *OIDCConfig) tokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	secret, err := os.ReadFile(o.ClientSecretPath)
	if err != nil {
		return nil, fmt.Errorf("temporal: reading OIDC client secret: %w", err)
	}

	cc := &clientcredentials.Config{
		ClientID:     o.ClientID,
		ClientSecret: strings.TrimSpace(string(secret)),
		TokenURL:     o.TokenURL,
		Scopes:       o.Scopes,
	}

	if o.Audience != "" {
		cc.EndpointParams = url.Values{"audience": {o.Audience}}
	}

	ctx = context.WithValue(ctx, oauth2.HTTPClient, &http.Client{Timeout: tokenFetchTimeout})

	return oauth2.ReuseTokenSourceWithExpiry(nil, cc.TokenSource(ctx), tokenRefreshEarly), nil
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

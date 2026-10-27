package ghapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/bradleyfalzon/ghinstallation/v2"
	gh "github.com/google/go-github/v75/github"
)

const (
	// searchSpacing keeps search under its 30-requests-per-minute limit.
	searchSpacing = 2 * time.Second
	// retryMargin is added to a rate-limit wait so the retry lands after
	// go-github's own client-side block has lifted.
	retryMargin = 500 * time.Millisecond
	// defaultSecondaryWait applies when a secondary limit names no
	// Retry-After.
	defaultSecondaryWait = 60 * time.Second
)

// ErrNotInstalled means the App has no installation on the org.
var ErrNotInstalled = errors.New("the App is not installed on this org")

// Options configures [New]. Every field is optional.
type Options struct {
	Logger *slog.Logger
	// UserAgent is sent on every request, e.g. "rgctl/v0.1.0".
	UserAgent string
	// Transport is the base round tripper; nil means http.DefaultTransport.
	Transport http.RoundTripper
	// Now and Sleep drive search pacing and rate-limit waits; tests inject
	// them.
	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration) error
}

// Client talks to GitHub for one run of rgctl. It is safe for concurrent use.
type Client struct {
	mode     Mode
	log      *slog.Logger
	opts     Options
	apiBase  string // GitHub Enterprise Server API base; empty for github.com
	botLogin string

	appsTr *ghinstallation.AppsTransport // ModeApp
	token  *gh.Client                    // ModeToken

	mu         sync.Mutex
	installs   map[string]int64 // lower-cased account login → installation id
	orgs       map[int64]*gh.Client
	lastSearch time.Time
}

// New builds a client. Under App auth it reads the private key and calls
// GET /app to learn the bot login.
func New(ctx context.Context, creds *Credentials, opts Options) (*Client, error) {
	mode, warning, err := creds.Resolve()
	if err != nil {
		return nil, err
	}
	c := &Client{mode: mode, opts: withDefaults(opts), orgs: make(map[int64]*gh.Client)}
	c.log = c.opts.Logger
	if warning != "" {
		c.log.Warn(warning)
	}
	if c.apiBase, err = apiBase(creds.Host); err != nil {
		return nil, err
	}
	if mode == ModeToken {
		err = c.initToken(creds)
	} else {
		err = c.initApp(ctx, creds)
	}
	if err != nil {
		return nil, err
	}
	return c, nil
}

func withDefaults(opts Options) Options {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if opts.Transport == nil {
		opts.Transport = http.DefaultTransport
	}
	if opts.UserAgent != "" {
		// Wrapped at the base so installation-token requests, which
		// ghinstallation sends itself, carry it too.
		opts.Transport = userAgentTransport{base: opts.Transport, ua: opts.UserAgent}
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Sleep == nil {
		opts.Sleep = sleep
	}
	return opts
}

func (c *Client) initToken(creds *Credentials) error {
	c.botLogin = creds.BotLogin
	cl, err := c.newGH(c.opts.Transport)
	if err != nil {
		return err
	}
	c.token = cl.WithAuthToken(creds.Token)
	return nil
}

func (c *Client) initApp(ctx context.Context, creds *Credentials) error {
	var err error
	c.appsTr, err = ghinstallation.NewAppsTransportKeyFromFile(c.opts.Transport, creds.AppID, creds.PrivateKeyFile)
	if err != nil {
		return fmt.Errorf("app credentials: %w", err)
	}
	app, err := c.newGH(c.appsTr)
	if err != nil {
		return err
	}
	// ghinstallation mints installation tokens at its own BaseURL. Take it
	// from go-github's resolved API URL, which carries the /api/v3 path an
	// Enterprise Server host needs.
	c.appsTr.BaseURL = strings.TrimSuffix(app.BaseURL.String(), "/")

	var info *gh.App
	if err := c.call(ctx, "get app", func() (err error) {
		info, _, err = app.Apps.Get(ctx, "")
		return err
	}); err != nil {
		return err
	}
	c.botLogin = info.GetSlug() + "[bot]"
	if creds.BotLogin != "" && !strings.EqualFold(creds.BotLogin, c.botLogin) {
		c.log.Warn("ignoring --bot-login under App auth", "given", creds.BotLogin, "discovered", c.botLogin)
	}
	c.installs, err = c.listInstallations(ctx, app)
	return err
}

// Mode reports the credential shape in use.
func (c *Client) Mode() Mode { return c.mode }

// BotLogin is the account whose pull requests are repo-guardian's,
// <slug>[bot].
func (c *Client) BotLogin() string { return c.botLogin }

// Installations lists the App's installations. Token auth has none to list.
func (c *Client) Installations() ([]Installation, error) {
	if c.mode != ModeApp {
		return nil, errors.New("listing the App's installations needs App credentials")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Installation, 0, len(c.installs))
	for login, id := range c.installs {
		out = append(out, Installation{ID: id, Account: login})
	}
	return out, nil
}

func (c *Client) listInstallations(ctx context.Context, app *gh.Client) (map[string]int64, error) {
	installs := make(map[string]int64)
	opts := &gh.ListOptions{PerPage: 100}
	for {
		var page []*gh.Installation
		var resp *gh.Response
		if err := c.call(ctx, "list installations", func() (err error) {
			page, resp, err = app.Apps.ListInstallations(ctx, opts)
			return err
		}); err != nil {
			return nil, err
		}
		for _, in := range page {
			installs[strings.ToLower(in.GetAccount().GetLogin())] = in.GetID()
		}
		if resp.NextPage == 0 {
			return installs, nil
		}
		opts.Page = resp.NextPage
	}
}

// clientFor returns the client that can act on owner's repositories: the
// operator token, or the installation client for owner's org.
func (c *Client) clientFor(owner string) (*gh.Client, error) {
	if c.mode == ModeToken {
		return c.token, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	id, ok := c.installs[strings.ToLower(owner)]
	if !ok {
		return nil, fmt.Errorf("%s: %w", owner, ErrNotInstalled)
	}
	if cl, ok := c.orgs[id]; ok {
		return cl, nil
	}
	cl, err := c.newGH(ghinstallation.NewFromAppsTransport(c.appsTr, id))
	if err != nil {
		return nil, err
	}
	c.orgs[id] = cl
	return cl, nil
}

func (c *Client) newGH(rt http.RoundTripper) (*gh.Client, error) {
	cl := gh.NewClient(&http.Client{Transport: rt})
	if c.apiBase != "" {
		var err error
		if cl, err = cl.WithEnterpriseURLs(c.apiBase, c.apiBase); err != nil {
			return nil, fmt.Errorf("github host: %w", err)
		}
	}
	if c.opts.UserAgent != "" {
		cl.UserAgent = c.opts.UserAgent
	}
	return cl, nil
}

// apiBase turns --github-host into a GitHub Enterprise Server base URL, or
// "" for github.com.
func apiBase(host string) (string, error) {
	switch strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(host, "https://"), "http://"), "/") {
	case "", "github.com", "api.github.com":
		return "", nil
	}
	if !strings.Contains(host, "://") {
		host = "https://" + host
	}
	u, err := url.Parse(host)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("github host %q is not a host name or URL", host)
	}
	if !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
	}
	return u.String(), nil
}

// call runs fn and, when it fails on a GitHub rate limit, waits for the reset
// or Retry-After and runs it once more.
func (c *Client) call(ctx context.Context, what string, fn func() error) error {
	err := fn()
	wait, limited := rateLimitWait(err, c.opts.Now())
	if !limited {
		return wrap(what, err)
	}
	c.log.Warn("GitHub rate limit, retrying once", "call", what, "wait", wait.Round(time.Second).String())
	if serr := c.opts.Sleep(ctx, wait); serr != nil {
		return wrap(what, serr)
	}
	return wrap(what, fn())
}

func wrap(what string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", what, err)
}

// rateLimitWait reports how long to wait before retrying err, when err is a
// primary or secondary rate limit.
func rateLimitWait(err error, now time.Time) (time.Duration, bool) {
	if rl, ok := errors.AsType[*gh.RateLimitError](err); ok {
		return max(rl.Rate.Reset.Sub(now), 0) + retryMargin, true
	}
	if ab, ok := errors.AsType[*gh.AbuseRateLimitError](err); ok {
		if ab.RetryAfter == nil {
			return defaultSecondaryWait, true
		}
		return *ab.RetryAfter + retryMargin, true
	}
	return 0, false
}

// paceSearch blocks until searchSpacing has passed since the previous search.
func (c *Client) paceSearch(ctx context.Context) error {
	c.mu.Lock()
	wait := c.lastSearch.Add(searchSpacing).Sub(c.opts.Now())
	c.mu.Unlock()
	if wait > 0 {
		if err := c.opts.Sleep(ctx, wait); err != nil {
			return err
		}
	}
	c.mu.Lock()
	c.lastSearch = c.opts.Now()
	c.mu.Unlock()
	return nil
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// userAgentTransport sets User-Agent on every request.
type userAgentTransport struct {
	base http.RoundTripper
	ua   string
}

func (t userAgentTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("User-Agent", t.ua)
	return t.base.RoundTrip(r)
}

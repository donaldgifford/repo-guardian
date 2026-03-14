package webhook

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/metrics"
)

const (
	defaultMetaURL     = "https://api.github.com/meta"
	metaFetchTimeout   = 10 * time.Second
	refreshInterval    = 24 * time.Hour
	reasonIPNotAllowed = "ip_not_allowed"
	reasonUnavailable  = "allowlist_unavailable"
)

// metaResponse is the relevant subset of the GitHub /meta API response.
type metaResponse struct {
	Hooks []string `json:"hooks"`
}

// GitHubIPAllowlist checks incoming request IPs against GitHub's published
// webhook CIDR ranges. It fetches ranges from the /meta API and caches them.
type GitHubIPAllowlist struct {
	mu         sync.RWMutex
	networks   []*net.IPNet
	loaded     bool
	failOpen   bool
	trustProxy bool
	logger     *slog.Logger
	metaURL    string
}

// NewGitHubIPAllowlist creates a new allowlist with the given configuration.
func NewGitHubIPAllowlist(failOpen, trustProxy bool, logger *slog.Logger) *GitHubIPAllowlist {
	return &GitHubIPAllowlist{
		failOpen:   failOpen,
		trustProxy: trustProxy,
		logger:     logger,
		metaURL:    defaultMetaURL,
	}
}

// fetchRanges retrieves and parses GitHub's webhook IP CIDR ranges.
func (a *GitHubIPAllowlist) fetchRanges(ctx context.Context) ([]*net.IPNet, error) {
	ctx, cancel := context.WithTimeout(ctx, metaFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.metaURL, http.NoBody)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var meta metaResponse
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return nil, err
	}

	networks := make([]*net.IPNet, 0, len(meta.Hooks))

	for _, cidr := range meta.Hooks {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			a.logger.Warn("skipping invalid CIDR from GitHub meta", "cidr", cidr, "error", err)
			continue
		}

		networks = append(networks, network)
	}

	return networks, nil
}

// Refresh fetches the latest IP ranges and updates the cache.
// On failure, previous ranges are kept intact.
func (a *GitHubIPAllowlist) Refresh(ctx context.Context) error {
	networks, err := a.fetchRanges(ctx)
	if err != nil {
		a.logger.Error("failed to refresh GitHub IP ranges", "error", err)
		return err
	}

	a.mu.Lock()
	a.networks = networks
	a.loaded = true
	a.mu.Unlock()

	a.logger.Info("refreshed GitHub webhook IP ranges", "count", len(networks))

	return nil
}

// StartRefresh performs an initial synchronous fetch and then refreshes
// periodically in a background goroutine. Stops when ctx is cancelled.
func (a *GitHubIPAllowlist) StartRefresh(ctx context.Context) {
	if err := a.Refresh(ctx); err != nil {
		a.logger.Warn("initial GitHub IP range fetch failed, will retry", "error", err)
	}

	go func() {
		ticker := time.NewTicker(refreshInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := a.Refresh(ctx); err != nil {
					a.logger.Warn("periodic GitHub IP range refresh failed", "error", err)
				}
			}
		}
	}()
}

// IsAllowed checks whether the given IP is within GitHub's webhook CIDR ranges.
func (a *GitHubIPAllowlist) IsAllowed(ip net.IP) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if !a.loaded {
		return a.failOpen
	}

	for _, network := range a.networks {
		if network.Contains(ip) {
			return true
		}
	}

	return false
}

// extractIP extracts the client IP from the request.
// When trustProxy is true and X-Forwarded-For is present, the leftmost IP is used.
// Otherwise, the IP is parsed from r.RemoteAddr.
func (a *GitHubIPAllowlist) extractIP(r *http.Request) net.IP {
	if a.trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first, _, _ := strings.Cut(xff, ",")
			ip := net.ParseIP(strings.TrimSpace(first))
			if ip != nil {
				return ip
			}
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return nil
	}

	return net.ParseIP(host)
}

// Middleware returns an http.Handler that rejects requests from IPs not in
// GitHub's webhook CIDR ranges.
func (a *GitHubIPAllowlist) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := a.extractIP(r)
		if ip == nil {
			a.logger.Warn("could not extract IP from request",
				"remote_addr", r.RemoteAddr,
				"path", r.URL.Path,
			)
			metrics.WebhookRejectedTotal.WithLabelValues(reasonIPNotAllowed).Inc()
			http.Error(w, "forbidden", http.StatusForbidden)

			return
		}

		if !a.IsAllowed(ip) {
			reason := reasonIPNotAllowed

			a.mu.RLock()
			if !a.loaded {
				reason = reasonUnavailable
			}
			a.mu.RUnlock()

			a.logger.Warn("rejected request from non-GitHub IP",
				"remote_ip", ip.String(),
				"path", r.URL.Path,
			)
			metrics.WebhookRejectedTotal.WithLabelValues(reason).Inc()
			http.Error(w, "forbidden", http.StatusForbidden)

			return
		}

		next.ServeHTTP(w, r)
	})
}

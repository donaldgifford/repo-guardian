package activities

import (
	"crypto/rand"
	"math/big"
	"time"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// causeMaxRunes clips error text carried in workflow history.
const causeMaxRunes = 1024

// Deferral backoff when a throttle has no usable reset: base 1m
// doubling per consecutive deferral to a 30m cap (IMPL-0022 OQ3). A
// secondary rate limit without Retry-After lands here, and GitHub asks
// for at least a minute before retrying one (issue #235).
const (
	backoffBase = time.Minute
	backoffCap  = 30 * time.Minute
)

// deferUntil returns when a throttled evaluation may run again: the reset
// time when it is still ahead, otherwise the backoff for deferrals,
// plus jitter of up to min(delay/4, 60s).
func deferUntil(now, resetAt time.Time, deferrals int) time.Time {
	delay := resetAt.Sub(now)
	if delay <= 0 {
		delay = backoffBase

		for range deferrals {
			delay *= 2
			if delay >= backoffCap {
				delay = backoffCap

				break
			}
		}
	}

	return now.Add(delay + jitter(min(delay/4, time.Minute)))
}

// jitter returns a uniform draw from [0, span). A reader failure means no
// jitter, which is safe for a load-spreading optimization.
func jitter(span time.Duration) time.Duration {
	if span <= 0 {
		return 0
	}

	n, err := rand.Int(rand.Reader, big.NewInt(int64(span)))
	if err != nil {
		return 0
	}

	return time.Duration(n.Int64())
}

func toRate(r *ghclient.RateObservation) *workflows.Rate {
	if r == nil {
		return nil
	}

	return &workflows.Rate{Limit: r.Limit, Remaining: r.Remaining, ResetAt: r.ResetAt, ObservedAt: r.ObservedAt}
}

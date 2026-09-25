package github

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestUsage_CountsSentRequestsAndKeepsLastRate(t *testing.T) {
	t.Parallel()

	reset := time.Now().Add(time.Hour).Truncate(time.Second)

	var n atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		withRateLimitHeaders(w, 1000-int(n.Add(1)), 5000, reset)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := &http.Client{Transport: newRateLimitTransport(http.DefaultTransport, slog.Default(), 0.10)}
	ctx, usage := WithUsage(context.Background())

	for range 3 {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, http.NoBody)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}

		resp.Body.Close()
	}

	if got := usage.Calls(); got != 3 {
		t.Errorf("Calls = %d, want 3", got)
	}

	rate := usage.Rate()
	if rate == nil {
		t.Fatal("Rate = nil, want the last headers")
	}

	if rate.Remaining != 997 || rate.Limit != 5000 || !rate.ResetAt.Equal(reset) || rate.ObservedAt.IsZero() {
		t.Errorf("Rate = %+v, want remaining 997, limit 5000, reset %v", rate, reset)
	}
}

func TestUsage_ThrottledRequestIsNotCounted(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		withRateLimitHeaders(w, 1, 5000, time.Now().Add(time.Hour))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := &http.Client{Transport: newRateLimitTransport(http.DefaultTransport, slog.Default(), 0.10)}
	ctx, usage := WithUsage(context.Background())

	for range 2 {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, http.NoBody)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}

		if resp, err := client.Do(req); err == nil {
			resp.Body.Close()
		}
	}

	// The first request is sent and sees remaining=1; the second is
	// refused below the reserve and never reaches GitHub.
	if got := usage.Calls(); got != 1 {
		t.Errorf("Calls = %d, want 1", got)
	}
}

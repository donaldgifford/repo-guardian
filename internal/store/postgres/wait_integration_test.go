//go:build integration

package postgres_test

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/store/postgres"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres/pgtest"
)

// TestWaitReady_RefusedThenReady is IMPL-0028 task 8.2: the migrate Job
// starts before Postgres accepts connections. The DSN points at a port
// nothing listens on (a real refusal); a second later a proxy to a live
// Postgres opens there, and WaitReady gets through.
func TestWaitReady_RefusedThenReady(t *testing.T) {
	upstream, err := url.Parse(pgtest.Start(t))
	if err != nil {
		t.Fatal(err)
	}

	addr := freeAddr(t)
	dsn := *upstream
	dsn.Host = addr

	db, err := postgres.OpenDB(dsn.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	go func() {
		time.Sleep(time.Second)
		proxy(t, addr, upstream.Host)
	}()

	start := time.Now()
	if err := postgres.WaitReady(t.Context(), db, 30*time.Second, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("WaitReady() = %v, want nil once the proxy opens", err)
	}

	if took := time.Since(start); took < time.Second {
		t.Errorf("WaitReady() returned after %s, before the database could answer", took)
	}
}

// freeAddr returns a loopback address with nothing listening on it.
func freeAddr(t *testing.T) string {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	return addr
}

// proxy forwards connections on addr to upstream until the test ends.
func proxy(t *testing.T, addr, upstream string) {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		t.Errorf("proxy listen: %v", err)

		return
	}

	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		conns []net.Conn
	)

	// Close every proxied connection too: the *sql.DB's pooled one would
	// otherwise hold its pipe open past the test.
	t.Cleanup(func() {
		_ = l.Close()

		mu.Lock()
		for _, c := range conns {
			_ = c.Close()
		}
		mu.Unlock()

		wg.Wait()
	})

	for {
		c, err := l.Accept()
		if errors.Is(err, net.ErrClosed) {
			return
		}

		if err != nil {
			t.Errorf("proxy accept: %v", err)

			return
		}

		mu.Lock()
		conns = append(conns, c)
		mu.Unlock()

		wg.Go(func() { pipe(c, upstream) })
	}
}

func pipe(c net.Conn, upstream string) {
	defer c.Close()

	u, err := net.Dial("tcp", upstream)
	if err != nil {
		return
	}
	defer u.Close()

	done := make(chan struct{}, 2)

	go func() { _, _ = io.Copy(u, c); done <- struct{}{} }()
	go func() { _, _ = io.Copy(c, u); done <- struct{}{} }()

	<-done
}

package postgres

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

var (
	errRefused  = &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
	errStarting = &pgconn.PgError{Code: sqlStateCannotConnectNow, Message: "the database system is starting up"}
	errPassword = &pgconn.PgError{Code: "28P01", Message: "password authentication failed"}
)

// pings returns a ping that fails with errs in order, then succeeds, and
// a pointer to its call count.
func pings(errs ...error) (func(context.Context) error, *int) {
	calls := 0

	return func(context.Context) error {
		calls++
		if calls <= len(errs) {
			return errs[calls-1]
		}

		return nil
	}, &calls
}

func TestWaitReady(t *testing.T) {
	tests := []struct {
		name      string
		errs      []error
		wait      time.Duration
		wantErr   error
		wantCalls int
	}{
		{name: "ready at once", wait: time.Second, wantCalls: 1},
		{name: "refused then ready", errs: []error{errRefused, errRefused}, wait: time.Second, wantCalls: 3},
		{name: "starting up then ready", errs: []error{errStarting}, wait: time.Second, wantCalls: 2},
		{name: "wrong password fails at once", errs: []error{errPassword, errPassword}, wait: time.Second, wantErr: errPassword, wantCalls: 1},
		{name: "no wait pings once", errs: []error{errRefused}, wantErr: syscall.ECONNREFUSED, wantCalls: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ping, calls := pings(tt.errs...)

			err := waitReady(t.Context(), ping, tt.wait, time.Millisecond, slog.New(slog.DiscardHandler))

			if tt.wantErr == nil && err != nil {
				t.Fatalf("waitReady() = %v, want nil", err)
			}

			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("waitReady() = %v, want %v", err, tt.wantErr)
			}

			if *calls != tt.wantCalls {
				t.Errorf("pings = %d, want %d", *calls, tt.wantCalls)
			}
		})
	}
}

func TestWaitReady_GivesUpAtTheDeadline(t *testing.T) {
	always := func(context.Context) error { return errRefused }

	start := time.Now()
	err := waitReady(t.Context(), always, 30*time.Millisecond, 5*time.Millisecond, slog.New(slog.DiscardHandler))

	if !errors.Is(err, syscall.ECONNREFUSED) {
		t.Fatalf("waitReady() = %v, want the last refusal", err)
	}

	if took := time.Since(start); took > time.Second {
		t.Errorf("waitReady() took %s, want about the 30ms wait", took)
	}
}

func TestNotReady(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "refused", err: errRefused, want: true},
		{name: "dns", err: &net.DNSError{Err: "no such host", Name: "pg", IsNotFound: true}, want: true},
		{name: "ping timeout", err: context.DeadlineExceeded, want: true},
		{name: "starting up", err: errStarting, want: true},
		{name: "wrong password", err: errPassword, want: false},
		{name: "other", err: errors.New("tls: bad certificate"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := notReady(tt.err); got != tt.want {
				t.Errorf("notReady(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

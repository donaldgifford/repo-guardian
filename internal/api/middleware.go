package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"regexp"
	"time"
)

const requestIDHeader = "X-Request-Id"

type requestIDKey struct{}

// validRequestID bounds an inbound X-Request-Id so a client cannot write
// arbitrary text into our logs.
var validRequestID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

func requestIDFrom(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey{}).(string); ok {
		return id
	}

	return ""
}

// statusRecorder captures the response status for the access log.
type statusRecorder struct {
	http.ResponseWriter

	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}

	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}

	return s.ResponseWriter.Write(b)
}

// requestLog assigns a request ID (reusing a well-formed inbound one),
// echoes it, and writes one access-log line per request.
func requestLog(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(requestIDHeader)
			if !validRequestID.MatchString(id) {
				id = newRequestID()
			}

			w.Header().Set(requestIDHeader, id)

			rec := &statusRecorder{ResponseWriter: w}
			start := time.Now()

			next.ServeHTTP(rec, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))

			logger.InfoContext(r.Context(), "api request",
				"request_id", id, "method", r.Method, "path", r.URL.Path,
				"status", rec.status, "duration", time.Since(start))
		})
	}
}

func newRequestID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)

	return hex.EncodeToString(b)
}

// recoverer turns a handler panic into a logged 500 problem.
func recoverer(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() { //nolint:contextcheck // the deferred recovery writes to r, which carries the context
				if v := recover(); v != nil {
					if v == http.ErrAbortHandler { //nolint:errorlint // sentinel compared as net/http does
						panic(v)
					}

					logger.ErrorContext(r.Context(), "api handler panic", "request_id", requestIDFrom(r.Context()), "panic", v)
					writeProblem(w, r, http.StatusInternalServerError, "")
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}

// authorize resolves the authenticated claims to a Principal.
func authorize(authz *AuthzConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := claimsFrom(r.Context())
			if !ok {
				writeProblem(w, r, http.StatusInternalServerError, "")

				return
			}

			p := &Principal{Claims: *claims, Visible: authz.Scope(claims)}
			next.ServeHTTP(w, r.WithContext(withPrincipal(r.Context(), p)))
		})
	}
}

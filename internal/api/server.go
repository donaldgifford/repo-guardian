package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"

	"github.com/donaldgifford/repo-guardian/internal/api/gen"
	"github.com/donaldgifford/repo-guardian/internal/findings"
	"github.com/donaldgifford/repo-guardian/internal/observability"
	"github.com/donaldgifford/repo-guardian/internal/store"
)

// BaseURL is where the API is mounted; it matches the spec's server URL.
const BaseURL = "/api/v1"

// Reader is the API's read side. *postgres.APIReader satisfies it.
//
// Every method is scoped in SQL by scope. Methods that read one resource
// return store.ErrNotFound when it is unknown or outside scope; the two
// are indistinguishable by design.
type Reader interface {
	Ping(ctx context.Context) error
	Summary(ctx context.Context, scope store.APIScope) (*store.Summary, error)
	Rules(ctx context.Context, scope store.APIScope) (*store.ComplianceReport, error)
	Rule(ctx context.Context, scope store.APIScope, kind findings.RuleKind, name string, staleBefore time.Time) (*store.RuleView, error)
	Orgs(ctx context.Context, scope store.APIScope) (*store.OrgsView, error)
	Org(ctx context.Context, scope store.APIScope, org string) (*store.OrgView, error)
	Findings(ctx context.Context, scope store.APIScope, f *store.FindingFilter) (store.Page[store.APIFinding], error)
	Repositories(ctx context.Context, scope store.APIScope, f *store.RepositoryFilter) (store.Page[store.Repository], error)
	Repository(ctx context.Context, scope store.APIScope, id int64, staleBefore time.Time) (*store.RepositoryDetail, error)
	RepositoryChecks(ctx context.Context, scope store.APIScope, id, afterID int64, limit int) (store.Page[store.Check], error)
	RepositoryEvents(ctx context.Context, scope store.APIScope, id int64, after *store.EventKey, limit int) (store.Page[store.Event], error)
	ComplianceHistory(ctx context.Context, scope store.APIScope, f *store.HistoryFilter) (store.Page[store.ComplianceSnapshot], error)
	Installations(ctx context.Context, scope store.APIScope, afterID int64, limit int) (store.Page[store.InstallationStatus], error)
	Policy(ctx context.Context) (*store.CurrentPolicy, error)
}

// Options configures the API handler.
type Options struct {
	Reader Reader

	// Authn verifies tokens. Nil disables authentication: every caller
	// is an anonymous principal who sees every org (API_AUTH_ENABLED=false).
	Authn *Authenticator
	Authz *AuthzConfig

	// StaleAfter is PR_STALE_AFTER.
	StaleAfter time.Duration

	Logger *slog.Logger
	// Now overrides the clock in tests.
	Now func() time.Time
}

// New returns the API handler, middleware outermost first (DESIGN-0027
// § The api role):
//
//  1. otelhttp;
//  2. request ID and access log;
//  3. panic recovery;
//  4. authentication;
//  5. authorization.
//
// The operations the spec declares with `security: []` are served
// around steps 4 and 5; every other route passes both. Authentication
// runs outside the generated wrapper so an anonymous request is refused
// 401 before parameter binding can answer 400.
func New(opts *Options) (http.Handler, error) {
	o := *opts
	if o.Now == nil {
		o.Now = time.Now
	}

	public, err := publicRoutes()
	if err != nil {
		return nil, err
	}

	strict := gen.NewStrictHandlerWithOptions(&server{opts: o}, []gen.StrictMiddlewareFunc{requireVisibleOrgs},
		gen.StrictHTTPServerOptions{
			RequestErrorHandlerFunc:  badRequest,
			ResponseErrorHandlerFunc: responseError(o.Logger),
		})

	inner := http.NewServeMux()
	inner.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { writeProblem(w, r, http.StatusNotFound, "") })

	routes := gen.HandlerWithOptions(strict, gen.StdHTTPServerOptions{
		BaseURL:          BaseURL,
		BaseRouter:       inner,
		Middlewares:      []gen.MiddlewareFunc{labelRoute},
		ErrorHandlerFunc: badRequest,
	})

	var protected http.Handler
	if o.Authn != nil {
		protected = o.Authn.Middleware(authorize(o.Authz)(routes))
	} else {
		protected = anonymous(routes)
	}

	outer := http.NewServeMux()
	outer.Handle("/", protected)

	for _, route := range public {
		outer.Handle(route, routes)
	}

	var h http.Handler = outer
	h = recoverer(o.Logger)(h)
	h = requestLog(o.Logger)(h)

	return observability.Handler(h, "api"), nil
}

// publicRoutes lists the spec's operations declared `security: []` as
// ServeMux patterns under BaseURL.
func publicRoutes() ([]string, error) {
	spec, err := gen.GetSpec()
	if err != nil {
		return nil, err
	}

	var out []string

	for path, item := range spec.Paths.Map() {
		for method, op := range item.Operations() {
			if op.Security != nil && len(*op.Security) == 0 {
				out = append(out, method+" "+BaseURL+path)
			}
		}
	}

	return out, nil
}

// labelRoute puts the matched pattern on the request's otelhttp
// metrics. The outer otelhttp handler sees its own copy of the request,
// on which the inner mux never set Pattern.
func labelRoute(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if l, ok := otelhttp.LabelerFromContext(r.Context()); ok && r.Pattern != "" {
			l.Add(attribute.String("http.route", r.Pattern))
		}

		next.ServeHTTP(w, r)
	})
}

// anonymous is the disabled-auth principal: it sees every org.
func anonymous(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := &Principal{Claims: Claims{Subject: "anonymous"}, Visible: store.ScopeAll()}
		next.ServeHTTP(w, r.WithContext(withPrincipal(r.Context(), p)))
	})
}

// requireVisibleOrgs refuses a principal that can see no org, on every
// operation but getMe, which tells the UI to show "ask for access".
func requireVisibleOrgs(f gen.StrictHandlerFunc, operationID string) gen.StrictHandlerFunc {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, req any) (any, error) {
		if p, ok := principalFrom(ctx); ok && p.Visible.Empty() && operationID != "GetMe" {
			return nil, statusError(http.StatusForbidden, "no visible organizations")
		}

		return f(ctx, w, r, req)
	}
}

// httpError is a handler failure with a status the client should see.
type httpError struct {
	status int
	detail string
}

func (e *httpError) Error() string { return e.detail }

func statusError(status int, detail string) error { return &httpError{status: status, detail: detail} }

func badRequest(w http.ResponseWriter, r *http.Request, err error) {
	writeProblem(w, r, http.StatusBadRequest, err.Error())
}

// responseError writes a handler error as a problem. Only an httpError's
// detail reaches the client; anything else is logged and answered 500.
func responseError(logger *slog.Logger) func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		var he *httpError
		if errors.As(err, &he) {
			writeProblem(w, r, he.status, he.detail)

			return
		}

		logger.ErrorContext(r.Context(), "api handler failed", "request_id", requestIDFrom(r.Context()), "error", err)
		writeProblem(w, r, http.StatusInternalServerError, "")
	}
}

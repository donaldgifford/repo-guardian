package api

import (
	"bytes"
	"context"
	"net/http"

	"github.com/donaldgifford/repo-guardian/api"
	"github.com/donaldgifford/repo-guardian/internal/api/gen"
)

// server implements the generated strict interface.
type server struct {
	opts     Options
	rechecks *recheckDedupe
}

var _ gen.StrictServerInterface = (*server)(nil)

// principal returns the request's principal; its absence is a wiring
// bug answered 500, never a wider scope.
func principal(ctx context.Context) (*Principal, error) {
	p, ok := principalFrom(ctx)
	if !ok {
		return nil, statusError(http.StatusInternalServerError, "")
	}

	return p, nil
}

// GetMe returns the caller and its visible orgs.
func (*server) GetMe(ctx context.Context, _ gen.GetMeRequestObject) (gen.GetMeResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}

	me := gen.GetMe200JSONResponse{
		Subject: p.Subject, Name: p.Name, AllOrgs: p.Visible.All(), Orgs: p.Visible.Orgs(),
	}

	if p.Client != "" {
		client := p.Client
		me.Client = &client
	}

	return me, nil
}

// GetStatus serves the status page from its cache; it never queries.
func (s *server) GetStatus(context.Context, gen.GetStatusRequestObject) (gen.GetStatusResponseObject, error) {
	if s.opts.Status == nil {
		return gen.GetStatus200JSONResponse(unknownStatus(s.opts.Now())), nil
	}

	return gen.GetStatus200JSONResponse(s.opts.Status.Current()), nil
}

// GetOpenAPI serves the contract as written.
func (*server) GetOpenAPI(context.Context, gen.GetOpenAPIRequestObject) (gen.GetOpenAPIResponseObject, error) {
	return gen.GetOpenAPI200ApplicationyamlResponse{Body: bytes.NewReader(api.Spec), ContentLength: int64(len(api.Spec))}, nil
}

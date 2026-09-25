package api

import (
	"bytes"
	"context"
	"net/http"
	"time"

	"github.com/donaldgifford/repo-guardian/api"
	"github.com/donaldgifford/repo-guardian/internal/api/gen"
	"github.com/donaldgifford/repo-guardian/internal/metrics"
)

// server implements the generated strict interface.
type server struct {
	opts Options
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

// prAgeBuckets is the order open PRs are reported in.
var prAgeBuckets = []string{metrics.PRAgeBucketLT1d, metrics.PRAgeBucket1To7, metrics.PRAgeBucket7To30, metrics.PRAgeBucketGT30}

// GetSummary returns the fleet summary over the visible orgs.
func (s *server) GetSummary(ctx context.Context, req gen.GetSummaryRequestObject) (gen.GetSummaryResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}

	staleAfter := s.opts.StaleAfter
	if req.Params.StaleAfter != nil {
		if staleAfter, err = time.ParseDuration(*req.Params.StaleAfter); err != nil || staleAfter <= 0 {
			return nil, statusError(http.StatusBadRequest, "stale_after must be a positive duration such as 720h")
		}
	}

	sum, err := s.opts.Reader.Summary(ctx, p.Visible)
	if err != nil {
		return nil, err
	}

	out := gen.GetSummary200JSONResponse{
		Tracked:          sum.Tracked,
		Parked:           make([]gen.ParkedCount, 0, len(sum.Parked)),
		CompliantPercent: sum.CompliantPercent,
		Findings: gen.StatusCounts{
			Compliant: sum.Findings.Compliant, NonCompliant: sum.Findings.NonCompliant,
			NotApplicable: sum.Findings.NotApplicable, Unknown: sum.Findings.Unknown,
		},
		OpenPrs:    make([]gen.PRAgeBucket, len(prAgeBuckets)),
		StaleAfter: staleAfter.String(),
	}

	for _, pc := range sum.Parked {
		out.Parked = append(out.Parked, gen.ParkedCount{Reason: string(pc.Reason), Count: pc.Count})
	}

	counts := map[string]int{}
	now := s.opts.Now()

	for _, created := range sum.OpenPRs {
		age := now.Sub(created)
		counts[metrics.PRAgeBucket(age.Hours()/24)]++

		if age > staleAfter {
			out.StalePrs++
		}
	}

	for i, b := range prAgeBuckets {
		out.OpenPrs[i] = gen.PRAgeBucket{Bucket: b, Count: counts[b]}
	}

	return out, nil
}

// GetStatus is the public status page.
//
// TODO(IMPL-0025 P15): compute the components and fleet compliance
// into the 30s cache; until then every component is unknown.
func (s *server) GetStatus(context.Context, gen.GetStatusRequestObject) (gen.GetStatusResponseObject, error) {
	return gen.GetStatus200JSONResponse{
		State:      "unknown",
		UpdatedAt:  s.opts.Now().UTC(),
		Components: []gen.Component{},
		Compliance: gen.Compliance{Measured: false},
	}, nil
}

// GetOpenAPI serves the contract as written.
func (*server) GetOpenAPI(context.Context, gen.GetOpenAPIRequestObject) (gen.GetOpenAPIResponseObject, error) {
	return gen.GetOpenAPI200ApplicationyamlResponse{Body: bytes.NewReader(api.Spec), ContentLength: int64(len(api.Spec))}, nil
}

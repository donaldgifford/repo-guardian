package activities

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	gh "github.com/google/go-github/v68/github"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/activity"

	"github.com/donaldgifford/repo-guardian/internal/checker"
	"github.com/donaldgifford/repo-guardian/internal/findings"
	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	ghmocks "github.com/donaldgifford/repo-guardian/internal/github/mocks"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/store/mocks"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

const (
	testRepoID       int64 = 42
	testInstallation int64 = 7
	testKey                = "repo/42/run/0"
	testVersion            = "v2:test"
)

// fakeGitHub is the installation client factory. The client it hands
// out embeds the generated mock, so any GitHub call the engine fake
// does not expect panics instead of returning zero values.
type fakeGitHub struct {
	err error
}

func (f *fakeGitHub) CreateInstallationClient(context.Context, int64) (ghclient.Client, error) {
	if f.err != nil {
		return nil, f.err
	}

	return &ghmocks.MockClient{}, nil
}

// fakeEngine answers CheckRepo with a scripted result.
type fakeEngine struct {
	res *checker.CheckResult
	err error
}

func (f *fakeEngine) CheckRepo(context.Context, ghclient.Client, string, string) (*checker.CheckResult, error) {
	return f.res, f.err
}

// fakeStore is the v2 store built from the generated Writer and Reader
// mocks.
type fakeStore struct {
	*mocks.MockWriter
	*mocks.MockReader
}

func newStore(t *testing.T) fakeStore {
	t.Helper()

	st := fakeStore{MockWriter: mocks.NewMockWriter(t), MockReader: mocks.NewMockReader(t)}
	st.MockReader.EXPECT().GetRepository(mock.Anything, testRepoID).
		Return(&store.Repository{ID: testRepoID, Org: "acme", Name: "widgets", InstallationID: testInstallation}, nil).Maybe()

	return st
}

// logRecorder captures log records for the E4 contract.
type logRecorder struct {
	mu      sync.Mutex
	records []slog.Record
}

func (*logRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (h *logRecorder) Handle(_ context.Context, r slog.Record) error { //nolint:gocritic // slog.Handler's signature
	h.mu.Lock()
	defer h.mu.Unlock()

	h.records = append(h.records, r)

	return nil
}

func (h *logRecorder) WithAttrs(attrs []slog.Attr) slog.Handler { return &attrHandler{h, attrs} }
func (h *logRecorder) WithGroup(string) slog.Handler            { return h }

// attrHandler folds With attributes into each record.
type attrHandler struct {
	*logRecorder

	attrs []slog.Attr
}

func (a *attrHandler) Handle(ctx context.Context, r slog.Record) error { //nolint:gocritic // slog.Handler's signature
	r.AddAttrs(a.attrs...)

	return a.logRecorder.Handle(ctx, r)
}

func (a *attrHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &attrHandler{a.logRecorder, append(append([]slog.Attr{}, a.attrs...), attrs...)}
}

func (h *logRecorder) find(msg string) (slog.Record, map[string]string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for i := range h.records {
		r := &h.records[i]
		if r.Message != msg {
			continue
		}

		attrs := map[string]string{}
		r.Attrs(func(a slog.Attr) bool {
			attrs[a.Key] = a.Value.String()

			return true
		})

		return *r, attrs, true
	}

	return slog.Record{}, nil, false
}

func newActivities(eng Engine, st Store, github ClientFactory) (*Activities, *logRecorder) {
	logs := &logRecorder{}

	return New(eng, st, github, testVersion, slog.New(logs)), logs
}

func checkInput() *workflows.CheckRepoInput {
	return &workflows.CheckRepoInput{RepositoryID: testRepoID, CheckKey: testKey, Trigger: workflows.TriggerPush}
}

func apiErr(status int) error {
	return fmt.Errorf("getting repository info: %w", &gh.ErrorResponse{Response: &http.Response{StatusCode: status}})
}

func TestCheckRepo_Classification(t *testing.T) {
	t.Parallel()

	retryAfter := 2 * time.Minute
	resetAt := time.Now().Add(time.Hour)

	tests := []struct {
		name      string
		err       error
		wantKind  workflows.CheckKind
		wantPark  string
		wantClear bool
		wantCause bool
		wantErr   bool
	}{
		{
			name:     "primary throttle defers",
			err:      fmt.Errorf("check: %w", &ghclient.ThrottledError{ResetAt: resetAt, Limit: 5000}),
			wantKind: workflows.CheckDeferred,
		},
		{
			name: "secondary-rate-limit 403 defers, never parks",
			err: fmt.Errorf("getting repository info: %w", &gh.AbuseRateLimitError{
				Response:   &http.Response{StatusCode: http.StatusForbidden},
				RetryAfter: &retryAfter,
			}),
			wantKind: workflows.CheckDeferred,
		},
		{
			name:     "go-github pre-check defers",
			err:      &gh.RateLimitError{Rate: gh.Rate{Limit: 5000, Reset: gh.Timestamp{Time: resetAt}}},
			wantKind: workflows.CheckDeferred,
		},
		{
			name:      "403 parks access_denied, findings kept",
			err:       apiErr(http.StatusForbidden),
			wantKind:  workflows.CheckParked,
			wantPark:  workflows.ParkAccessDenied,
			wantCause: true,
		},
		{
			name:      "404 parks access_denied, findings kept",
			err:       apiErr(http.StatusNotFound),
			wantKind:  workflows.CheckParked,
			wantPark:  workflows.ParkAccessDenied,
			wantCause: true,
		},
		{
			name:      "archived parks, findings cleared",
			err:       &checker.SkippedError{Reason: workflows.ParkArchived},
			wantKind:  workflows.CheckParked,
			wantPark:  workflows.ParkArchived,
			wantClear: true,
		},
		{
			name:      "fork parks, findings cleared",
			err:       &checker.SkippedError{Reason: workflows.ParkFork},
			wantKind:  workflows.CheckParked,
			wantPark:  workflows.ParkFork,
			wantClear: true,
		},
		{name: "500 is an error for the activity to retry", err: apiErr(http.StatusInternalServerError), wantErr: true},
		{name: "network failure is an error", err: errors.New("dial tcp: connection refused"), wantErr: true},
	}

	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// No StageCheck expectation: a check that did not finish
			// stages nothing.
			a, _ := newActivities(&fakeEngine{err: tt.err}, newStore(t), &fakeGitHub{})

			res, err := a.CheckRepo(context.Background(), checkInput())
			if tt.wantErr {
				if err == nil || res != nil {
					t.Fatalf("CheckRepo = %+v, %v; want an error", res, err)
				}

				return
			}

			if err != nil {
				t.Fatalf("CheckRepo: %v", err)
			}

			if res.Kind != tt.wantKind || res.ParkReason != tt.wantPark || res.ClearFindings != tt.wantClear ||
				(res.Cause != "") != tt.wantCause || res.CheckKey != testKey {
				t.Errorf("result = %+v", res)
			}

			if tt.wantKind == workflows.CheckDeferred && !res.Until.After(time.Now()) {
				t.Errorf("Until = %v, want in the future", res.Until)
			}
		})
	}
}

func TestCheckRepo_InstallationClientErrorRetries(t *testing.T) {
	t.Parallel()

	a, _ := newActivities(&fakeEngine{}, newStore(t), &fakeGitHub{err: errors.New("token exchange failed")})

	if _, err := a.CheckRepo(context.Background(), checkInput()); err == nil {
		t.Fatal("CheckRepo = nil error, want the client error for the activity to retry")
	}
}

func TestCheckRepo_StageThenRecordHandoff(t *testing.T) {
	t.Parallel()

	ok := true
	st := newStore(t)
	eng := &fakeEngine{res: &checker.CheckResult{
		Outcomes: []checker.RuleOutcome{{
			RuleName: "codeowners", Kind: findings.RuleKindFile,
			Status: findings.StatusNonCompliant, Reason: findings.ReasonFileMissing, Remediation: findings.RemediationPROpen,
			Evidence: findings.FileMissingEvidence{PathsChecked: []string{"CODEOWNERS"}},
			PR:       &findings.PREvidence{Number: 12, URL: "https://github.com/acme/widgets/pull/12"},
		}},
		CatalogParseOK: &ok,
		Repository:     &checker.RepositoryIdentity{ID: 9001, Owner: "acme", Name: "gadgets"},
	}}

	var staged *store.CheckRecord

	st.MockWriter.EXPECT().StageCheck(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, c *store.CheckRecord) error {
			staged = c

			return nil
		}).Once()

	a, _ := newActivities(eng, st, &fakeGitHub{})

	res, err := a.CheckRepo(context.Background(), checkInput())
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}

	if res.Kind != workflows.CheckChecked || res.Org != "acme" || res.Name != "gadgets" ||
		res.ProviderRepoID == nil || *res.ProviderRepoID != 9001 || res.PolicyVersion != testVersion {
		t.Fatalf("result = %+v", res)
	}

	if staged.Key != testKey || staged.RepositoryID != testRepoID || staged.Trigger != store.TriggerPush ||
		len(staged.Outcomes) != 1 || staged.Outcomes[0].PR == nil || staged.Outcomes[0].Remediation != findings.RemediationPROpen {
		t.Fatalf("staged = %+v", staged)
	}

	var recorded *store.CheckRecord

	st.MockWriter.EXPECT().RecordCheck(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, c *store.CheckRecord) (*store.CheckApplied, error) {
			recorded = c

			return &store.CheckApplied{Transitions: make([]store.FindingTransition, 1)}, nil
		}).Once()

	res.Rate = &workflows.Rate{Limit: 5000, Remaining: 4990, ObservedAt: time.Now()}

	summary, err := a.RecordCheck(context.Background(), &workflows.RecordCheckInput{
		RepositoryID: testRepoID, Trigger: workflows.TriggerPush, Result: *res,
	})
	if err != nil {
		t.Fatalf("RecordCheck: %v", err)
	}

	// Outcomes nil: RecordCheck reads the staged payload under the key.
	if recorded.Key != staged.Key || recorded.Outcomes != nil || recorded.Name != "gadgets" ||
		recorded.CatalogParseOK != &ok || recorded.Rate == nil || recorded.Rate.Remaining != 4990 {
		t.Errorf("recorded = %+v", recorded)
	}

	if summary.Transitions != 1 {
		t.Errorf("summary = %+v", summary)
	}
}

// parkLine is v1's parking log line; E4's LogQL matches it verbatim.
const parkLine = "parking repository until discovery sees it again"

func TestPark_ReEmitsV1LogLine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		in        workflows.ParkInput
		wantLevel slog.Level
	}{
		{
			name: "access denied logs the cause at error",
			in: workflows.ParkInput{
				RepositoryID: testRepoID, CheckKey: testKey, Trigger: workflows.TriggerSchedule,
				Reason: workflows.ParkAccessDenied, Cause: "GET .../widgets: 403",
			},
			wantLevel: slog.LevelError,
		},
		{
			name: "archived logs at info with no error",
			in: workflows.ParkInput{
				RepositoryID: testRepoID, CheckKey: testKey, Trigger: workflows.TriggerSchedule,
				Reason: workflows.ParkArchived, ClearFindings: true,
			},
			wantLevel: slog.LevelInfo,
		},
	}

	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			st := newStore(t)
			st.MockWriter.EXPECT().Park(mock.Anything, testRepoID, store.ParkReason(tt.in.Reason), tt.in.ClearFindings).Return(nil).Once()

			a, logs := newActivities(&fakeEngine{}, st, &fakeGitHub{})
			if err := a.Park(context.Background(), &tt.in); err != nil {
				t.Fatalf("Park: %v", err)
			}

			rec, attrs, ok := logs.find(parkLine)
			if !ok {
				t.Fatalf("no %q line", parkLine)
			}

			if rec.Level != tt.wantLevel {
				t.Errorf("level = %v, want %v", rec.Level, tt.wantLevel)
			}

			want := map[string]string{
				"owner": "acme", "repo": "widgets", "trigger": workflows.TriggerSchedule,
				"installation_id": "7", "job_id": testKey, "reason": tt.in.Reason,
			}
			for k, v := range want {
				if attrs[k] != v {
					t.Errorf("%s = %q, want %q", k, attrs[k], v)
				}
			}

			if got, has := attrs["error"]; has != (tt.in.Cause != "") || got != tt.in.Cause {
				t.Errorf("error = %q (present %v), want %q", got, has, tt.in.Cause)
			}
		})
	}
}

func TestDeferUntil(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		reset     time.Time
		deferrals int
		want      time.Duration
	}{
		{"future reset", now.Add(10 * time.Minute), 3, 10 * time.Minute},
		{"stale reset backs off from 30s", now.Add(-time.Minute), 0, 30 * time.Second},
		{"backoff doubles", time.Time{}, 2, 2 * time.Minute},
		{"backoff caps at 30m", time.Time{}, 20, 30 * time.Minute},
	}

	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := deferUntil(now, tt.reset, tt.deferrals).Sub(now)
			if got < tt.want || got >= tt.want+min(tt.want/4, time.Minute) {
				t.Errorf("delay = %v, want %v plus jitter", got, tt.want)
			}
		})
	}
}

func TestRegister_UsesWorkflowNames(t *testing.T) {
	t.Parallel()

	reg := &nameRecorder{}
	(&Activities{}).Register(reg)

	got := strings.Join(reg.names, ",")
	if want := "CheckRepo,RecordCheck,RecordCheckError,Park"; got != want {
		t.Errorf("registered %s, want %s", got, want)
	}
}

type nameRecorder struct {
	names []string
}

func (r *nameRecorder) RegisterActivityWithOptions(_ any, o activity.RegisterOptions) {
	r.names = append(r.names, o.Name)
}

func TestAcquireResult_Label(t *testing.T) {
	t.Parallel()

	tests := map[string]workflows.AcquireResult{
		"granted":    {Granted: true},
		"optimistic": {Granted: true, Optimistic: true},
		"wait":       {WaitUntil: time.Now()},
	}

	for want, r := range tests {
		if got := acquireResult(&r); got != want {
			t.Errorf("acquireResult(%+v) = %q, want %q", r, got, want)
		}
	}
}

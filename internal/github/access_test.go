package github_test

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	gh "github.com/google/go-github/v68/github"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
)

// respErr builds the shape go-github returns for a failed API call.
func respErr(status int) error {
	return &gh.ErrorResponse{
		Response: &http.Response{StatusCode: status},
		Message:  http.StatusText(status),
	}
}

func TestIsAccessDenied(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{
			// GitHub answers "you may not see this" with 404, so a repo
			// outside the installation looks exactly like a deleted one.
			// Both are permanent from our side.
			name: "404 not found",
			err:  respErr(http.StatusNotFound),
			want: true,
		},
		{"403 forbidden", respErr(http.StatusForbidden), true},
		{
			// The classifier must see through wrapping: CheckRepo returns
			// fmt.Errorf("getting repository info: %w", err).
			name: "wrapped 404",
			err:  fmt.Errorf("getting repository info: %w", respErr(http.StatusNotFound)),
			want: true,
		},
		{
			// The trap this classifier exists alongside: a secondary rate
			// limit is also a 403, and is emphatically not permanent.
			// Treating it as access denial would park a repo for an hour
			// of throttling.
			name: "throttle is never access denial",
			err:  &ghclient.ThrottledError{ResetAt: time.Now().Add(time.Hour), Remaining: 0, Limit: 5000},
			want: false,
		},
		{
			name: "wrapped throttle is never access denial",
			err:  fmt.Errorf("check repo: %w", &ghclient.ThrottledError{ResetAt: time.Now(), Limit: 5000}),
			want: false,
		},
		{"500 is transient", respErr(http.StatusInternalServerError), false},
		{"422 is a real request error", respErr(http.StatusUnprocessableEntity), false},
		{"non-API error", errors.New("dial tcp: connection refused"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := ghclient.IsAccessDenied(tt.err); got != tt.want {
				t.Errorf("IsAccessDenied(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

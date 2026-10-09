package metrics

import (
	"os"
	"regexp"
	"slices"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestPRAgeBucket(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		ageDays float64
		want    string
	}{
		{"zero days", 0, PRAgeBucketLT1d},
		{"under one day", 0.5, PRAgeBucketLT1d},
		{"exactly one day", 1, PRAgeBucket1To7},
		{"three days", 3, PRAgeBucket1To7},
		{"under seven days", 6.99, PRAgeBucket1To7},
		{"exactly seven days", 7, PRAgeBucket7To30},
		{"two weeks", 14, PRAgeBucket7To30},
		{"under thirty days", 29.99, PRAgeBucket7To30},
		{"exactly thirty days", 30, PRAgeBucketGT30},
		{"sixty days", 60, PRAgeBucketGT30},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := PRAgeBucket(tt.ageDays); got != tt.want {
				t.Errorf("PRAgeBucket(%v) = %q, want %q", tt.ageDays, got, tt.want)
			}
		})
	}
}

func TestPRAgeBuckets_AllCovered(t *testing.T) {
	t.Parallel()

	want := map[string]bool{
		PRAgeBucketLT1d:  true,
		PRAgeBucket1To7:  true,
		PRAgeBucket7To30: true,
		PRAgeBucketGT30:  true,
	}
	if len(PRAgeBuckets) != len(want) {
		t.Fatalf("PRAgeBuckets length = %d, want %d", len(PRAgeBuckets), len(want))
	}
	for _, b := range PRAgeBuckets {
		if !want[b] {
			t.Errorf("PRAgeBuckets contains unexpected label %q", b)
		}
		delete(want, b)
	}
	if len(want) != 0 {
		t.Errorf("PRAgeBuckets missing labels: %v", want)
	}
}

// TestSetInstallationInfo_DropsBlankOrg pins the guard in
// SetInstallationInfo. A blank org reaches it whenever the caller's
// source of truth is empty — a webhook payload missing its account, a
// queue.Job built from a partial event — and an
// installation_info{org=""} series is worse than none: `group_left`
// against it silently attaches an empty org to every joined series, so
// rate-limit rows render under a nameless org rather than visibly
// failing to render.
func TestSetInstallationInfo_DropsBlankOrg(t *testing.T) {
	InstallationInfo.Reset()

	SetInstallationInfo(42, "")

	if n := testutil.CollectAndCount(InstallationInfo); n != 0 {
		t.Errorf("installation_info has %d series after a blank-org call, want 0", n)
	}

	SetInstallationInfo(42, "octo")

	if got := testutil.ToFloat64(InstallationInfo.WithLabelValues("42", "octo")); got != 1 {
		t.Errorf(`installation_info{installation_id="42", org="octo"} = %v, want 1`, got)
	}
}

// TestMetricNames_ExactSet pins every series this package registers.
// Deleting the v1 runtime removed the queue, scheduler, posture and
// write-back series (IMPL-0028 task 1.3); a name added or removed
// without updating this list fails here, so a dashboard or alert built
// on a series cannot lose its producer unnoticed.
//
// It reads the Name literals from metrics.go: promauto registers into
// the default registry, which cannot list its collectors, and a vector
// with no series yet is absent from Gather.
func TestMetricNames_ExactSet(t *testing.T) {
	t.Parallel()

	want := []string{
		"repo_guardian_api_auth_failures_total",
		"repo_guardian_api_status_refresh_seconds",
		"repo_guardian_branch_protection_checked_total",
		"repo_guardian_branch_protection_remediated_total",
		"repo_guardian_budget_acquire_total",
		"repo_guardian_catalog_parse_failed_total",
		"repo_guardian_check_duration_seconds",
		"repo_guardian_checks_total",
		"repo_guardian_custom_property_cleared_total",
		"repo_guardian_custom_property_missing_schema_total",
		"repo_guardian_discovery_api_calls_total",
		"repo_guardian_errors_total",
		"repo_guardian_files_forbidden_present_total",
		"repo_guardian_files_missing_total",
		"repo_guardian_ignored_total",
		"repo_guardian_installation_info",
		"repo_guardian_open_prs_by_rule",
		"repo_guardian_out_of_scope_total",
		"repo_guardian_pr_open_with_empty_actionable_total",
		"repo_guardian_pr_orphan_left_total",
		"repo_guardian_property_schema_missing",
		"repo_guardian_prs_closed_total",
		"repo_guardian_prs_created_total",
		"repo_guardian_prs_updated_total",
		"repo_guardian_rate_limit_remaining",
		"repo_guardian_repo_discovered_total",
		"repo_guardian_repos_checked_total",
		"repo_guardian_repos_parked_total",
		"repo_guardian_rule_gate_closed_total",
		"repo_guardian_settings_checked_total",
		"repo_guardian_settings_mismatched_total",
		"repo_guardian_settings_remediated_total",
		"repo_guardian_store_query_seconds",
		"repo_guardian_webhook_received_total",
		"repo_guardian_webhook_rejected_total",
		"repo_guardian_webhook_temporal_errors_total",
	}

	src, err := os.ReadFile("metrics.go")
	if err != nil {
		t.Fatal(err)
	}

	matches := regexp.MustCompile(`Name:\s+"([a-z_]+)"`).FindAllStringSubmatch(string(src), -1)
	got := make([]string, 0, len(matches))

	for _, m := range matches {
		got = append(got, m[1])
	}

	slices.Sort(got)

	if !slices.Equal(got, want) {
		t.Errorf("registered metric names = %v\nwant %v", got, want)
	}
}

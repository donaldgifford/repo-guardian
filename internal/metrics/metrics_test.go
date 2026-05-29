package metrics

import "testing"

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

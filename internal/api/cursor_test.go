package api

import (
	"encoding/base64"
	"errors"
	"testing"
	"time"
)

type testFilters struct {
	Org    string `json:"org"`
	Status string `json:"status"`
}

type testKey struct {
	At time.Time `json:"at"`
	ID int64     `json:"id"`
}

func TestCursor_RoundTrip(t *testing.T) {
	t.Parallel()

	want := testKey{At: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC), ID: 42}
	filters := testFilters{Org: "acme", Status: "non_compliant"}

	s, err := encodeCursor(want, filters)
	if err != nil {
		t.Fatal(err)
	}

	var got testKey

	ok, err := decodeCursor(&s, &got, filters)
	if err != nil || !ok {
		t.Fatalf("decodeCursor = %v, %v", ok, err)
	}

	if !got.At.Equal(want.At) || got.ID != want.ID {
		t.Errorf("key = %+v, want %+v", got, want)
	}
}

func TestCursor_Refusals(t *testing.T) {
	t.Parallel()

	filters := testFilters{Org: "acme"}

	valid, err := encodeCursor(testKey{ID: 1}, filters)
	if err != nil {
		t.Fatal(err)
	}

	long := string(make([]byte, maxCursorLen+1))

	tests := []struct {
		name    string
		cursor  string
		filters testFilters
		want    error
	}{
		{name: "different filters", cursor: valid, filters: testFilters{Org: "other"}, want: errCursorFilters},
		{name: "not base64", cursor: "!!!", filters: filters, want: errCursor},
		{name: "not json", cursor: base64.RawURLEncoding.EncodeToString([]byte("nope")), filters: filters, want: errCursor},
		{name: "no key", cursor: base64.RawURLEncoding.EncodeToString([]byte(`{"f":"x"}`)), filters: filters, want: errCursor},
		{name: "too long", cursor: long, filters: filters, want: errCursor},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var key testKey

			if _, err := decodeCursor(&tt.cursor, &key, tt.filters); !errors.Is(err, tt.want) {
				t.Errorf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestCursor_AbsentIsFirstPage(t *testing.T) {
	t.Parallel()

	empty := ""

	for _, s := range []*string{nil, &empty} {
		var key testKey

		ok, err := decodeCursor(s, &key, testFilters{})
		if ok || err != nil {
			t.Errorf("decodeCursor(%v) = %v, %v; want false, nil", s, ok, err)
		}
	}
}

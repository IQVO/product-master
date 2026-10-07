package analyticsstore

import (
	"testing"
	"time"
)

func TestMemory_Contract(t *testing.T) {
	runContract(t, func(*testing.T) store { return NewMemory() })
}

func TestEarliestIgnoresNilLikeSQLLeast(t *testing.T) {
	a, b := at("2026-10-05T00:00:00Z"), at("2026-10-06T00:00:00Z")
	if earliest(nil, nil) != nil || !earliest(nil, &a).Equal(a) || !earliest(&a, nil).Equal(a) ||
		!earliest(&a, &b).Equal(a) || !earliest(&b, &a).Equal(a) {
		t.Fatal("earliest is not LEAST")
	}
	if got := earliest(&a, &a); got != &a {
		t.Fatal("a tie keeps the stored value")
	}
}

func TestLatestBefore(t *testing.T) {
	states := map[int64]memState{
		1: {at: at("2026-10-05T00:00:00Z")},
		3: {at: at("2026-10-07T00:00:00Z")},
		2: {at: at("2026-10-06T00:00:00Z")},
	}
	for end, want := range map[string]time.Time{
		"2026-10-06T00:00:01Z": at("2026-10-06T00:00:00Z"),
		"2026-10-06T00:00:00Z": at("2026-10-05T00:00:00Z"), // strictly before
		"2026-10-08T00:00:00Z": at("2026-10-07T00:00:00Z"),
	} {
		if got := latestBefore(states, at(end)); !got.at.Equal(want) {
			t.Errorf("latestBefore(%s) = %v, want %v", end, got.at, want)
		}
	}
	if got := latestBefore(states, at("2026-10-05T00:00:00Z")); !got.at.IsZero() {
		t.Errorf("nothing before the first state, got %v", got.at)
	}
}

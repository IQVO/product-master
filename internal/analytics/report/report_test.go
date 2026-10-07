package report

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

func ts(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestParseRange(t *testing.T) {
	cases := []struct {
		name, from, to string
		want           Range
		wantErr        error
	}{
		{"both omitted: the 30 days ending now", "", "", Range{t0.Add(-30 * 24 * time.Hour), t0}, nil},
		{"only to", "", "2026-10-01T00:00:00Z", Range{ts("2026-09-01T00:00:00Z"), ts("2026-10-01T00:00:00Z")}, nil},
		{"only from", "2026-10-06T00:00:00Z", "", Range{ts("2026-10-06T00:00:00Z"), t0}, nil},
		{"offsets are normalised to UTC", "2026-10-06T00:00:00-03:00", "2026-10-07T00:00:00-03:00",
			Range{ts("2026-10-06T03:00:00Z"), ts("2026-10-07T03:00:00Z")}, nil},
		{"exactly 366 days is allowed", "2025-10-06T00:00:00Z", "2026-10-07T00:00:00Z",
			Range{ts("2025-10-06T00:00:00Z"), ts("2026-10-07T00:00:00Z")}, nil},
		{"one second more than 366 days", "2025-10-05T23:59:59Z", "2026-10-07T00:00:00Z", Range{}, ErrRangeTooLarge},
		{"from == to is empty", "2026-10-06T00:00:00Z", "2026-10-06T00:00:00Z", Range{}, ErrEmptyRange},
		{"inverted", "2026-10-07T00:00:00Z", "2026-10-06T00:00:00Z", Range{}, ErrEmptyRange},
		{"bad from", "yesterday", "", Range{}, ErrInvalidFrom},
		{"bad to", "", "2026-10-06", Range{}, ErrInvalidTo},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseRange(tc.from, tc.to, t0.In(time.FixedZone("x", 3600)))
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if !got.From.Equal(tc.want.From) || !got.To.Equal(tc.want.To) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			if err == nil && (got.From.Location() != time.UTC || got.To.Location() != time.UTC) {
				t.Fatalf("range not in UTC: %v", got)
			}
		})
	}
}

func TestWindowsArePinned(t *testing.T) {
	if DefaultWindow() != 720*time.Hour || MaxWindow() != 8784*time.Hour {
		t.Fatalf("default %v, max %v", DefaultWindow(), MaxWindow())
	}
}

func TestDayWindows(t *testing.T) {
	cases := []struct {
		name string
		r    Range
		want []DayWindow
	}{
		{"whole days", Range{ts("2026-10-05T00:00:00Z"), ts("2026-10-07T00:00:00Z")}, []DayWindow{
			{ts("2026-10-05T00:00:00Z"), ts("2026-10-05T00:00:00Z"), ts("2026-10-06T00:00:00Z")},
			{ts("2026-10-06T00:00:00Z"), ts("2026-10-06T00:00:00Z"), ts("2026-10-07T00:00:00Z")},
		}},
		{"partial first and last day", Range{ts("2026-10-05T10:00:00Z"), ts("2026-10-07T06:30:00Z")}, []DayWindow{
			{ts("2026-10-05T00:00:00Z"), ts("2026-10-05T10:00:00Z"), ts("2026-10-06T00:00:00Z")},
			{ts("2026-10-06T00:00:00Z"), ts("2026-10-06T00:00:00Z"), ts("2026-10-07T00:00:00Z")},
			{ts("2026-10-07T00:00:00Z"), ts("2026-10-07T00:00:00Z"), ts("2026-10-07T06:30:00Z")},
		}},
		{"inside one day", Range{ts("2026-10-05T10:00:00Z"), ts("2026-10-05T11:00:00Z")}, []DayWindow{
			{ts("2026-10-05T00:00:00Z"), ts("2026-10-05T10:00:00Z"), ts("2026-10-05T11:00:00Z")},
		}},
		{"non-UTC bounds", Range{ts("2026-10-05T22:00:00-03:00"), ts("2026-10-06T02:00:00Z")}, []DayWindow{
			{ts("2026-10-06T00:00:00Z"), ts("2026-10-06T01:00:00Z"), ts("2026-10-06T02:00:00Z")},
		}},
		{"empty", Range{ts("2026-10-05T10:00:00Z"), ts("2026-10-05T10:00:00Z")}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DayWindows(tc.r)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d windows %v, want %d", len(got), got, len(tc.want))
			}
			for i := range got {
				if !got[i].Day.Equal(tc.want[i].Day) || !got[i].From.Equal(tc.want[i].From) || !got[i].To.Equal(tc.want[i].To) {
					t.Fatalf("window %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestLaterEarlierTies(t *testing.T) {
	a, b := ts("2026-10-05T00:00:00Z"), ts("2026-10-06T00:00:00Z")
	if !later(a, b).Equal(b) || !later(b, a).Equal(b) || !earlier(a, b).Equal(a) || !earlier(b, a).Equal(a) {
		t.Fatal("later/earlier picked the wrong bound")
	}
	tie := a.In(time.FixedZone("x", 3600))
	if later(tie, a).Location() != tie.Location() || earlier(tie, a).Location() != tie.Location() {
		t.Fatal("on a tie the first argument wins")
	}
}

func TestComputeCoverage(t *testing.T) {
	got := ComputeCoverage(CoverageCounts{Products: 8, Classified: 6, DimensionsDeclared: 4, Measured: 2, OpenDiscrepancies: 1})
	want := Coverage{
		CoverageCounts:  CoverageCounts{Products: 8, Classified: 6, DimensionsDeclared: 4, Measured: 2, OpenDiscrepancies: 1},
		ClassifiedRatio: 0.75, DimensionsDeclaredRatio: 0.5, MeasuredRatio: 0.25, DiscrepancyRatio: 0.5,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if empty := ComputeCoverage(CoverageCounts{}); !reflect.DeepEqual(empty, Coverage{}) {
		t.Fatalf("an empty catalogue has zero ratios, got %+v", empty)
	}
}

func TestRate(t *testing.T) {
	if Rate(1, 4) != 0.25 || Rate(3, 0) != 0 || Rate(3, -1) != 0 || Rate(0, 1) != 0 || Rate(1, 1) != 1 {
		t.Fatal("Rate is wrong")
	}
}

func TestComputeFreshness(t *testing.T) {
	if f := ComputeFreshness(nil, t0); f.AsOf != nil || f.LagSeconds != nil {
		t.Fatalf("nothing applied: %+v", f)
	}
	at := t0.Add(-90 * time.Second).In(time.FixedZone("x", 3600))
	f := ComputeFreshness(&at, t0)
	if !f.AsOf.Equal(at) || f.AsOf.Location() != time.UTC || *f.LagSeconds != 90 {
		t.Fatalf("got %v %v", f.AsOf, *f.LagSeconds)
	}
	ahead := t0.Add(time.Minute)
	if f := ComputeFreshness(&ahead, t0); *f.LagSeconds != 0 {
		t.Fatalf("clock skew must read as zero lag, got %v", *f.LagSeconds)
	}
}

func TestKinds(t *testing.T) {
	want := []Kind{"ProductRegistered", "ProductDescriptionChanged", "ProductClassified", "ProductDimensionsDeclared", "ProductMeasured"}
	if !reflect.DeepEqual(Kinds(), want) {
		t.Fatalf("Kinds() = %v", Kinds())
	}
	for _, k := range want {
		if k.IsProfile() != (k == KindDimensionsDeclared || k == KindMeasured) {
			t.Fatalf("%s.IsProfile() = %v", k, k.IsProfile())
		}
	}
}

func TestFirstsOf(t *testing.T) {
	at := t0.In(time.FixedZone("x", 3600))
	cases := map[Kind]Firsts{
		KindRegistered:         {Registered: &t0},
		KindClassified:         {Classified: &t0},
		KindDimensionsDeclared: {DimensionsDeclared: &t0},
		KindMeasured:           {Measured: &t0},
		KindDescriptionChanged: {},
	}
	for kind, want := range cases {
		got := FirstsOf(ProductEvent{Kind: kind, At: at})
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %+v, want %+v", kind, got, want)
		}
	}
}

func valid(kind Kind) ProductEvent {
	e := ProductEvent{Kind: kind, EventID: "id-1", At: t0, SKU: "SKU-1", Version: 1}
	switch kind {
	case KindDimensionsDeclared:
		e.Profile = &ProfileState{HasDeclared: true}
	case KindMeasured:
		e.Profile = &ProfileState{HasDeclared: true, HasMeasured: true, Discrepancy: true}
	}
	return e
}

// invalidEvents breaks exactly one invariant each.
var invalidEvents = map[string]func() ProductEvent{
	"unknown kind": func() ProductEvent { e := valid(KindRegistered); e.Kind = "ProductDeleted"; return e },
	"no id":        func() ProductEvent { e := valid(KindRegistered); e.EventID = ""; return e },
	"no sku":       func() ProductEvent { e := valid(KindRegistered); e.SKU = ""; return e },
	"version 0":    func() ProductEvent { e := valid(KindRegistered); e.Version = 0; return e },
	"no time":      func() ProductEvent { e := valid(KindClassified); e.At = time.Time{}; return e },
	"profile on a non-profile kind": func() ProductEvent {
		e := valid(KindClassified)
		e.Profile = &ProfileState{}
		return e
	},
	"profile kind without profile": func() ProductEvent { e := valid(KindMeasured); e.Profile = nil; return e },
	"declared without declared": func() ProductEvent {
		e := valid(KindDimensionsDeclared)
		e.Profile = &ProfileState{HasMeasured: true}
		return e
	},
	"measured without measured": func() ProductEvent {
		e := valid(KindMeasured)
		e.Profile = &ProfileState{HasDeclared: true}
		return e
	},
	"discrepancy without declared": func() ProductEvent {
		e := valid(KindMeasured)
		e.Profile = &ProfileState{HasMeasured: true, Discrepancy: true}
		return e
	},
	"discrepancy without measured": func() ProductEvent {
		e := valid(KindDimensionsDeclared)
		e.Profile = &ProfileState{HasDeclared: true, Discrepancy: true}
		return e
	},
}

func TestValidate(t *testing.T) {
	for _, k := range Kinds() {
		if err := valid(k).Validate(); err != nil {
			t.Errorf("valid %s rejected: %v", k, err)
		}
	}
	measuredOnly := valid(KindMeasured)
	measuredOnly.Profile = &ProfileState{HasMeasured: true}
	if err := measuredOnly.Validate(); err != nil {
		t.Errorf("a measurement without declared dimensions is valid: %v", err)
	}
	for name, build := range invalidEvents {
		if err := build().Validate(); !errors.Is(err, ErrInvalidEvent) {
			t.Errorf("%s: err = %v, want ErrInvalidEvent", name, err)
		}
	}
}

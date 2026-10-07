package analyticsstore

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/product-master/internal/analytics/report"
)

// store is what the contract exercises: the writer and the reader over one
// model.
type store interface {
	report.Projection
	report.Reader
}

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func ev(id string, kind report.Kind, sku string, version int64, when string) report.ProductEvent {
	return report.ProductEvent{Kind: kind, EventID: id, At: at(when), SKU: sku, Version: version}
}

func profileEv(id string, kind report.Kind, sku string, version int64, when string, declared, measured, discrepancy bool) report.ProductEvent {
	e := ev(id, kind, sku, version, when)
	e.Profile = &report.ProfileState{HasDeclared: declared, HasMeasured: measured, Discrepancy: discrepancy}
	return e
}

func mustApply(t *testing.T, s store, events ...report.ProductEvent) {
	t.Helper()
	for _, e := range events {
		if _, err := s.Apply(context.Background(), e); err != nil {
			t.Fatalf("apply %s %s: %v", e.Kind, e.EventID, err)
		}
	}
}

func days(t *testing.T, s store, from, to string) []report.QualityDay {
	t.Helper()
	got, err := s.QualityDays(context.Background(), report.Range{From: at(from), To: at(to)})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func day(d string, registered, classified, declared, measured, open int) report.QualityDay {
	return report.QualityDay{Day: at(d + "T00:00:00Z"), Registered: registered, Classified: classified,
		DimensionsDeclared: declared, Measured: measured, OpenDiscrepancies: open}
}

func assertDays(t *testing.T, got, want []report.QualityDay) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d days %+v, want %d %+v", len(got), got, len(want), want)
	}
	for i := range got {
		g, w := got[i], want[i]
		if !g.Day.Equal(w.Day) || g.Day.Location() != time.UTC {
			t.Fatalf("day %d = %v, want %v (UTC)", i, g.Day, w.Day)
		}
		g.Day = w.Day
		if !reflect.DeepEqual(g, w) {
			t.Fatalf("day %d = %+v, want %+v", i, got[i], w)
		}
	}
}

// contractScenarios are run against every store (Memory in the unit tests,
// Postgres in the integration tests): both must give the same answers.
var contractScenarios = []struct {
	name string
	run  func(t *testing.T, s store)
}{
	{"an empty projection reports zeros, dense days and no freshness", func(t *testing.T, s store) {
		assertDays(t, days(t, s, "2026-10-05T00:00:00Z", "2026-10-07T00:00:00Z"),
			[]report.QualityDay{day("2026-10-05", 0, 0, 0, 0, 0), day("2026-10-06", 0, 0, 0, 0, 0)})
		c, err := s.Coverage(context.Background())
		if err != nil || c != (report.CoverageCounts{}) {
			t.Fatalf("coverage = %+v, %v", c, err)
		}
		last, err := s.LastEventAt(context.Background())
		if err != nil || last != nil {
			t.Fatalf("last = %v, %v", last, err)
		}
	}},
	{"a replayed id is a no-op", func(t *testing.T, s store) {
		e := ev("e1", report.KindRegistered, "SKU-1", 1, "2026-10-05T10:00:00Z")
		if applied, err := s.Apply(context.Background(), e); err != nil || !applied {
			t.Fatalf("first apply = %v, %v", applied, err)
		}
		again := e
		again.SKU = "SKU-OTHER" // the same id is never applied twice, whatever it carries
		if applied, err := s.Apply(context.Background(), again); err != nil || applied {
			t.Fatalf("replay = %v, %v; want false, nil", applied, err)
		}
		if c, _ := s.Coverage(context.Background()); c.Products != 1 {
			t.Fatalf("products = %d, want 1", c.Products)
		}
	}},
	{"daily counts use the first milestone per SKU, in any arrival order", func(t *testing.T, s store) {
		mustApply(t, s,
			ev("c2", report.KindClassified, "SKU-1", 3, "2026-10-06T09:00:00Z"), // a reclassification arrives first
			ev("r1", report.KindRegistered, "SKU-1", 1, "2026-10-05T08:00:00Z"),
			ev("c1", report.KindClassified, "SKU-1", 2, "2026-10-05T09:00:00Z"),
			ev("r2", report.KindRegistered, "SKU-2", 1, "2026-10-06T23:59:59Z"),
			ev("d2", report.KindDescriptionChanged, "SKU-3", 5, "2026-10-06T12:00:00Z"),
			profileEv("p1", report.KindDimensionsDeclared, "SKU-2", 2, "2026-10-07T00:00:00Z", true, false, false),
		)
		assertDays(t, days(t, s, "2026-10-05T00:00:00Z", "2026-10-08T00:00:00Z"), []report.QualityDay{
			day("2026-10-05", 1, 1, 0, 0, 0),
			day("2026-10-06", 1, 0, 0, 0, 0),
			day("2026-10-07", 0, 0, 1, 0, 0),
		})
		c, _ := s.Coverage(context.Background())
		if want := (report.CoverageCounts{Products: 3, Classified: 1, DimensionsDeclared: 1}); c != want {
			t.Fatalf("coverage = %+v, want %+v (a ProductDescriptionChanged makes SKU-3 known)", c, want)
		}
	}},
	{"the range is half-open and clips the first and last day", func(t *testing.T, s store) {
		mustApply(t, s,
			ev("a", report.KindRegistered, "A", 1, "2026-10-05T09:59:59Z"),
			ev("b", report.KindRegistered, "B", 1, "2026-10-05T10:00:00Z"), // == from: in
			ev("c", report.KindRegistered, "C", 1, "2026-10-06T05:59:59Z"),
			ev("d", report.KindRegistered, "D", 1, "2026-10-06T06:00:00Z"), // == to: out
		)
		assertDays(t, days(t, s, "2026-10-05T10:00:00Z", "2026-10-06T06:00:00Z"),
			[]report.QualityDay{day("2026-10-05", 1, 0, 0, 0, 0), day("2026-10-06", 1, 0, 0, 0, 0)})
	}},
	{"open discrepancies follow the profile history; the current state follows the version", func(t *testing.T, s store) {
		mustApply(t, s,
			// SKU-1: declared day 5, measured with a discrepancy day 5, re-declared to agree day 7.
			profileEv("v2", report.KindDimensionsDeclared, "SKU-1", 2, "2026-10-05T08:00:00Z", true, false, false),
			profileEv("v4", report.KindDimensionsDeclared, "SKU-1", 4, "2026-10-07T08:00:00Z", true, true, false), // arrives before v3
			profileEv("v3", report.KindMeasured, "SKU-1", 3, "2026-10-05T09:00:00Z", true, true, true),
			// SKU-2: measured only (no declared), no discrepancy possible.
			profileEv("w1", report.KindMeasured, "SKU-2", 2, "2026-10-06T10:00:00Z", false, true, false),
			// SKU-3: a discrepancy that is still open.
			profileEv("x1", report.KindMeasured, "SKU-3", 3, "2026-10-06T11:00:00Z", true, true, true),
		)
		assertDays(t, days(t, s, "2026-10-05T00:00:00Z", "2026-10-08T00:00:00Z"), []report.QualityDay{
			day("2026-10-05", 0, 0, 1, 1, 1),
			day("2026-10-06", 0, 0, 0, 2, 2),
			day("2026-10-07", 0, 0, 0, 0, 1),
		})
		// A window ending mid-day sees the state at its end.
		assertDays(t, days(t, s, "2026-10-05T00:00:00Z", "2026-10-05T08:30:00Z"),
			[]report.QualityDay{day("2026-10-05", 0, 0, 1, 0, 0)})
		c, _ := s.Coverage(context.Background())
		want := report.CoverageCounts{Products: 3, DimensionsDeclared: 2, Measured: 3, OpenDiscrepancies: 1}
		if c != want {
			t.Fatalf("coverage = %+v, want %+v (the late v3 must not roll SKU-1 back)", c, want)
		}
	}},
	{"freshness is the newest applied CloudEvents time", func(t *testing.T, s store) {
		mustApply(t, s,
			ev("n2", report.KindRegistered, "B", 1, "2026-10-06T10:00:00Z"),
			ev("n1", report.KindRegistered, "A", 1, "2026-10-05T10:00:00Z"),
		)
		last, err := s.LastEventAt(context.Background())
		if err != nil || last == nil || !last.Equal(at("2026-10-06T10:00:00Z")) || last.Location() != time.UTC {
			t.Fatalf("last = %v, %v", last, err)
		}
	}},
	{"a profile state the schema forbids is rejected and leaves nothing behind", func(t *testing.T, s store) {
		bad := profileEv("bad", report.KindMeasured, "SKU-9", 2, "2026-10-05T10:00:00Z", false, true, true)
		if _, err := s.Apply(context.Background(), bad); !errors.Is(err, report.ErrRejected) {
			t.Fatalf("err = %v, want ErrRejected", err)
		}
		if c, _ := s.Coverage(context.Background()); c.Products != 0 {
			t.Fatalf("the rejected event left a fact: %+v", c)
		}
		if last, _ := s.LastEventAt(context.Background()); last != nil {
			t.Fatalf("the rejected event was marked processed: %v", last)
		}
		good := bad
		good.Profile = &report.ProfileState{HasMeasured: true}
		if applied, err := s.Apply(context.Background(), good); err != nil || !applied {
			t.Fatalf("the same id must be applicable once fixed: %v, %v", applied, err)
		}
	}},
}

func runContract(t *testing.T, newStore func(t *testing.T) store) {
	for _, sc := range contractScenarios {
		t.Run(sc.name, func(t *testing.T) { sc.run(t, newStore(t)) })
	}
}

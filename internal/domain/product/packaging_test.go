package product

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/product-master/internal/domain/shared/gs1"
)

// Fixture GTINs, all built through gs1.NewGTIN so the check digits are real.
var (
	gtinEach  = mustGTIN("400638133393")   // GTIN-13 4006381333931
	gtinInner = mustGTIN("1400638133393")  // GTIN-14, indicator 1
	gtinCase  = mustGTIN("2400638133393")  // GTIN-14, indicator 2
	gtinCase2 = mustGTIN("3400638133393")  // GTIN-14, indicator 3
	gtinPlt   = mustGTIN("5400638133393")  // GTIN-14, indicator 5
	gtinVar   = mustGTIN("9400638133393")  // GTIN-14, indicator 9 (variable measure)
	gtinOther = mustGTIN("590123412345")   // an unrelated EAN-13
	t1        = t0.Add(48 * time.Hour)     // a later command time
	t2        = t0.Add(96 * time.Hour)     // a later command time
	dims1     = mustDims(100, 50, 40, 500) // one each: 500 g
)

func mustGTIN(payload string) gs1.GTIN {
	g, err := gs1.NewGTIN(payload)
	if err != nil {
		panic(err)
	}
	return g
}

func mustDims(l, w, h, g int64) UnitDimensions {
	d, err := NewUnitDimensions(l, w, h, g)
	if err != nil {
		panic(err)
	}
	return d
}

func eachSpec() PackLevelSpec {
	return PackLevelSpec{Level: PackLevelEach, GTIN: gtinEach, IsOrderable: true}
}

func caseSpec(gtin gs1.GTIN, qty int) PackLevelSpec {
	return PackLevelSpec{Level: PackLevelCase, GTIN: gtin, ChildGTIN: gtinEach, QtyOfChild: qty, IsOrderable: true, IsDespatchUnit: true}
}

// withEach returns a product with an active EACH level declared at t0 (version 2).
func withEach(t *testing.T) *Product {
	t.Helper()
	p := registered(t)
	if _, err := p.DeclarePackLevel(eachSpec(), t0); err != nil {
		t.Fatalf("declare each: %v", err)
	}
	return p
}

// withCase returns a product with EACH and CASE (12 per case) declared at t0 (version 3).
func withCase(t *testing.T) *Product {
	t.Helper()
	p := withEach(t)
	if _, err := p.DeclarePackLevel(caseSpec(gtinCase, 12), t0); err != nil {
		t.Fatalf("declare case: %v", err)
	}
	return p
}

func TestParsePackLevel(t *testing.T) {
	for _, l := range []PackLevel{PackLevelEach, PackLevelInner, PackLevelCase, PackLevelPallet} {
		got, err := ParsePackLevel(string(l))
		if err != nil || got != l {
			t.Fatalf("ParsePackLevel(%q) = %q, %v", l, got, err)
		}
	}
	for _, bad := range []string{"", "case", "Each", "MASTER", " CASE", "CASE "} {
		if got, err := ParsePackLevel(bad); !errors.Is(err, ErrInvalidPackLevel) || got != "" {
			t.Fatalf("ParsePackLevel(%q) = %q, %v; want ErrInvalidPackLevel", bad, got, err)
		}
	}
}

func TestPackLevelOrder(t *testing.T) {
	order := []PackLevel{PackLevelEach, PackLevelInner, PackLevelCase, PackLevelPallet}
	for i, lo := range order {
		for j, hi := range order {
			if got := lo.Below(hi); got != (i < j) {
				t.Fatalf("%s.Below(%s) = %v", lo, hi, got)
			}
		}
	}
	if PackLevel("BOGUS").Below(PackLevelPallet) || PackLevelEach.Below("BOGUS") {
		t.Fatal("an unknown level is below / above nothing")
	}
}

func TestParseGTIN(t *testing.T) {
	g, err := ParseGTIN("4006381333931")
	if err != nil || g.String() != "04006381333931" {
		t.Fatalf("GTIN-13 = %q, %v; want the 14-digit form", g, err)
	}
	for _, bad := range []string{"", "123", "4006381333930", "4006381333931x", "00000000000000"} {
		if g, err := ParseGTIN(bad); !errors.Is(err, ErrInvalidGTIN) || !g.IsZero() {
			t.Fatalf("ParseGTIN(%q) = %q, %v; want ErrInvalidGTIN", bad, g, err)
		}
	}
	if g, err := ParseGTIN(gtinVar.String()); !errors.Is(err, ErrVariableMeasureUnsupported) || !g.IsZero() {
		t.Fatalf("variable measure = %q, %v; want ErrVariableMeasureUnsupported", g, err)
	}
	// a bad check digit on an indicator-9 number is an invalid GTIN first
	if _, err := ParseGTIN("94006381333930"); !errors.Is(err, ErrInvalidGTIN) {
		t.Fatalf("bad check digit on indicator 9 = %v", err)
	}
}

// declareEachWithDims declares the EACH level, despatchable and with dims1, at t1.
func declareEachWithDims(t *testing.T) (*Product, ProductPackLevelDeclared) {
	t.Helper()
	p := registered(t)
	spec := eachSpec()
	spec.IsDespatchUnit = true
	dims := dims1
	spec.Dims = &dims
	events, err := p.DeclarePackLevel(spec, t1)
	if err != nil {
		t.Fatal(err)
	}
	if p.Version() != 2 || len(events) != 1 {
		t.Fatalf("version %d, events %v", p.Version(), events)
	}
	ev, ok := events[0].(ProductPackLevelDeclared)
	if !ok {
		t.Fatalf("event = %+v", events[0])
	}
	return p, ev
}

func TestDeclareEachLevelRaisesTheEvent(t *testing.T) {
	_, ev := declareEachWithDims(t)
	if ev.EventName() != "ProductPackLevelDeclared" || ev.ProductSKU() != "SKU-1" || ev.ProductVersion() != 2 || !ev.OccurredAt().Equal(t1) {
		t.Fatalf("event = %+v", ev)
	}
}

func TestDeclareEachLevelHasNoMeasurement(t *testing.T) {
	_, ev := declareEachWithDims(t)
	if _, ok := ev.Def.Measurement(); ok {
		t.Fatal("a new level has no measurement")
	}
}

func TestDeclareEachLevelDefinition(t *testing.T) {
	p, ev := declareEachWithDims(t)
	d := ev.Def
	if d.Level() != PackLevelEach || d.GTIN() != gtinEach || !d.ChildGTIN().IsZero() || d.QtyOfChild() != 0 || d.UnitsPerPack() != 1 {
		t.Fatalf("def identity = %+v", d)
	}
	if !d.IsOrderable() || !d.IsDespatchUnit() || !d.EffectiveFrom().Equal(t1) || !d.Active() {
		t.Fatalf("def flags = %+v", d)
	}
	if _, ended := d.EffectiveTo(); ended {
		t.Fatal("a new level has no effectiveTo")
	}
	if got, ok := d.DeclaredDimensions(); !ok || got != dims1 {
		t.Fatalf("declared dims = %+v %v", got, ok)
	}
	if got, ok := p.PackLevels().Active(PackLevelEach); !ok || got.GTIN() != gtinEach {
		t.Fatalf("Active(EACH) = %+v %v", got, ok)
	}
}

func TestDeclareEachUnitsPerPack(t *testing.T) {
	for _, u := range []int{0, 1} {
		p := registered(t)
		s := eachSpec()
		s.UnitsPerPack = u
		if _, err := p.DeclarePackLevel(s, t0); err != nil {
			t.Fatalf("unitsPerPack %d: %v", u, err)
		}
		d, _ := p.PackLevels().Active(PackLevelEach)
		if d.UnitsPerPack() != 1 {
			t.Fatalf("EACH unitsPerPack = %d", d.UnitsPerPack())
		}
	}
	bad := []struct {
		name string
		mut  func(*PackLevelSpec)
	}{
		{"units 2", func(s *PackLevelSpec) { s.UnitsPerPack = 2 }},
		{"negative units", func(s *PackLevelSpec) { s.UnitsPerPack = -1 }},
		{"qtyOfChild given", func(s *PackLevelSpec) { s.QtyOfChild = 2 }},
		{"childGtin given", func(s *PackLevelSpec) { s.ChildGTIN = gtinOther }},
	}
	for _, tt := range bad {
		t.Run(tt.name, func(t *testing.T) {
			p := registered(t)
			s := eachSpec()
			tt.mut(&s)
			events, err := p.DeclarePackLevel(s, t0)
			if !errors.Is(err, ErrInvalidPackQuantity) || events != nil || p.Version() != 1 {
				t.Fatalf("events=%v err=%v version=%d", events, err, p.Version())
			}
		})
	}
}

func TestDeclareRejectsBadGTINAndLevel(t *testing.T) {
	p := registered(t)
	s := eachSpec()
	s.GTIN = gs1.GTIN{}
	if _, err := p.DeclarePackLevel(s, t0); !errors.Is(err, ErrInvalidGTIN) {
		t.Fatalf("zero GTIN = %v", err)
	}
	s = eachSpec()
	s.GTIN = gtinVar
	if _, err := p.DeclarePackLevel(s, t0); !errors.Is(err, ErrVariableMeasureUnsupported) {
		t.Fatalf("variable measure = %v", err)
	}
	s = eachSpec()
	s.Level = "BOGUS"
	if _, err := p.DeclarePackLevel(s, t0); !errors.Is(err, ErrInvalidPackLevel) {
		t.Fatalf("bogus level = %v", err)
	}
	if p.Version() != 1 {
		t.Fatalf("version moved to %d", p.Version())
	}
}

func TestDeclareCaseNeedsALowerChildLevel(t *testing.T) {
	t.Run("no child gtin", func(t *testing.T) {
		p := withEach(t)
		s := caseSpec(gtinCase, 12)
		s.ChildGTIN = gs1.GTIN{}
		events, err := p.DeclarePackLevel(s, t0)
		if !errors.Is(err, ErrPackLevelOrder) || events != nil || p.Version() != 2 {
			t.Fatalf("events=%v err=%v version=%d", events, err, p.Version())
		}
	})
	t.Run("child gtin not on this product", func(t *testing.T) {
		p := withEach(t)
		s := caseSpec(gtinCase, 12)
		s.ChildGTIN = gtinOther
		if _, err := p.DeclarePackLevel(s, t0); !errors.Is(err, ErrPackLevelOrder) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("child on an empty product", func(t *testing.T) {
		p := registered(t)
		if _, err := p.DeclarePackLevel(caseSpec(gtinCase, 12), t0); !errors.Is(err, ErrPackLevelOrder) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("child at a higher level", func(t *testing.T) {
		p := withCase(t)
		s := PackLevelSpec{Level: PackLevelInner, GTIN: gtinInner, ChildGTIN: gtinCase, QtyOfChild: 2}
		if _, err := p.DeclarePackLevel(s, t0); !errors.Is(err, ErrPackLevelOrder) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("child at the same level", func(t *testing.T) {
		p := withCase(t)
		s := PackLevelSpec{Level: PackLevelCase, GTIN: gtinCase2, ChildGTIN: gtinCase, QtyOfChild: 2}
		if _, err := p.DeclarePackLevel(s, t0); !errors.Is(err, ErrPackLevelOrder) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("child level is retired", func(t *testing.T) {
		p := withEach(t)
		if _, err := p.RetirePackLevel(PackLevelEach, "gone", t1); err != nil {
			t.Fatal(err)
		}
		if _, err := p.DeclarePackLevel(caseSpec(gtinCase, 12), t1); !errors.Is(err, ErrPackLevelOrder) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("child may skip a level", func(t *testing.T) {
		p := withEach(t)
		if _, err := p.DeclarePackLevel(PackLevelSpec{Level: PackLevelPallet, GTIN: gtinPlt, ChildGTIN: gtinEach, QtyOfChild: 600}, t0); err != nil {
			t.Fatalf("pallet of eaches: %v", err)
		}
	})
}

func TestDeclareQuantityBounds(t *testing.T) {
	tests := []struct {
		name  string
		qty   int
		units int
		ok    bool
	}{
		{"qty 2", 2, 0, true},
		{"qty maximum", MaxPackQuantity, 0, true},
		{"qty 1", 1, 0, false},
		{"qty 0", 0, 0, false},
		{"qty negative", -4, 0, false},
		{"qty over the maximum", MaxPackQuantity + 1, 0, false},
		{"units matches", 12, 12, true},
		{"units at the maximum", MaxPackQuantity, MaxPackQuantity, true},
		{"units over the maximum", 12, MaxPackQuantity + 1, false},
		{"units too small", 12, 11, false},
		{"units too large", 12, 13, false},
		{"units negative", 12, -12, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := withEach(t)
			s := caseSpec(gtinCase, tt.qty)
			s.UnitsPerPack = tt.units
			_, err := p.DeclarePackLevel(s, t0)
			if tt.ok && err != nil {
				t.Fatalf("err = %v", err)
			}
			if !tt.ok && !errors.Is(err, ErrInvalidPackQuantity) {
				t.Fatalf("err = %v, want ErrInvalidPackQuantity", err)
			}
		})
	}
}

func TestDeclareDerivesUnitsPerPackFromTheChildFactor(t *testing.T) {
	p := withEach(t)
	if _, err := p.DeclarePackLevel(PackLevelSpec{Level: PackLevelInner, GTIN: gtinInner, ChildGTIN: gtinEach, QtyOfChild: 6}, t0); err != nil {
		t.Fatal(err)
	}
	events, err := p.DeclarePackLevel(PackLevelSpec{Level: PackLevelCase, GTIN: gtinCase, ChildGTIN: gtinInner, QtyOfChild: 4}, t0)
	if err != nil {
		t.Fatal(err)
	}
	d := events[0].(ProductPackLevelDeclared).Def
	if d.QtyOfChild() != 4 || d.UnitsPerPack() != 24 || d.ChildGTIN() != gtinInner {
		t.Fatalf("case def = qty %d units %d child %s", d.QtyOfChild(), d.UnitsPerPack(), d.ChildGTIN())
	}
	// the stored direct factor is checked against the chain, not recomputed on read
	bad := PackLevelSpec{Level: PackLevelPallet, GTIN: gtinPlt, ChildGTIN: gtinCase, QtyOfChild: 10, UnitsPerPack: 100}
	if _, err := p.DeclarePackLevel(bad, t0); !errors.Is(err, ErrInvalidPackQuantity) {
		t.Fatalf("pallet with a wrong direct factor = %v", err)
	}
	good := bad
	good.UnitsPerPack = 240
	events, err = p.DeclarePackLevel(good, t0)
	if err != nil || events[0].(ProductPackLevelDeclared).Def.UnitsPerPack() != 240 {
		t.Fatalf("pallet = %v %v", events, err)
	}
}

func TestDeclareUnitsPerPackOverTheMaximumIsRejected(t *testing.T) {
	p := withEach(t)
	if _, err := p.DeclarePackLevel(caseSpec(gtinCase, MaxPackQuantity), t0); err != nil {
		t.Fatal(err)
	}
	s := PackLevelSpec{Level: PackLevelPallet, GTIN: gtinPlt, ChildGTIN: gtinCase, QtyOfChild: 2}
	if _, err := p.DeclarePackLevel(s, t0); !errors.Is(err, ErrInvalidPackQuantity) {
		t.Fatalf("2 x %d eaches = %v", MaxPackQuantity, err)
	}
}

func TestDeclareSameLevelAgainIsIdempotent(t *testing.T) {
	p := withCase(t)
	events, err := p.DeclarePackLevel(caseSpec(gtinCase, 12), t2)
	if err != nil || events != nil || p.Version() != 3 {
		t.Fatalf("events=%v err=%v version=%d", events, err, p.Version())
	}
	// an explicit, matching direct factor is the same declaration
	s := caseSpec(gtinCase, 12)
	s.UnitsPerPack = 12
	if events, err := p.DeclarePackLevel(s, t2); err != nil || events != nil || p.Version() != 3 {
		t.Fatalf("explicit factor: events=%v err=%v version=%d", events, err, p.Version())
	}
}

func TestDeclareChangingTheQuantityOfAnActiveLevelIsRejected(t *testing.T) {
	tests := []struct {
		name string
		mut  func(*PackLevelSpec)
	}{
		{"qtyOfChild", func(s *PackLevelSpec) { s.QtyOfChild = 24 }},
		{"unitsPerPack", func(s *PackLevelSpec) { s.UnitsPerPack = 24 }},
		{"qtyOfChild and unitsPerPack", func(s *PackLevelSpec) { s.QtyOfChild, s.UnitsPerPack = 24, 24 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := withCase(t)
			s := caseSpec(gtinCase, 12)
			tt.mut(&s)
			events, err := p.DeclarePackLevel(s, t1)
			if !errors.Is(err, ErrPackQuantityImmutable) || events != nil || p.Version() != 3 {
				t.Fatalf("events=%v err=%v version=%d", events, err, p.Version())
			}
			if d, _ := p.PackLevels().Active(PackLevelCase); d.QtyOfChild() != 12 || d.UnitsPerPack() != 12 {
				t.Fatalf("the active level was edited: %+v", d)
			}
		})
	}
	t.Run("childGtin", func(t *testing.T) {
		p := withCase(t)
		if _, err := p.DeclarePackLevel(PackLevelSpec{Level: PackLevelInner, GTIN: gtinInner, ChildGTIN: gtinEach, QtyOfChild: 6}, t0); err != nil {
			t.Fatal(err)
		}
		s := caseSpec(gtinCase, 12)
		s.ChildGTIN = gtinInner
		if _, err := p.DeclarePackLevel(s, t1); !errors.Is(err, ErrPackQuantityImmutable) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestDeclareChangesOnlyTheMutableAttributesInPlace(t *testing.T) {
	p := withCase(t)
	s := caseSpec(gtinCase, 12)
	s.IsOrderable, s.IsDespatchUnit = false, false
	dims := mustDims(400, 300, 200, 7000)
	s.Dims = &dims
	events, err := p.DeclarePackLevel(s, t2)
	if err != nil {
		t.Fatal(err)
	}
	ev := onlyEvent(t, events).(ProductPackLevelDeclared)
	if ev.ProductVersion() != 4 || p.Version() != 4 {
		t.Fatalf("version = %d / %d", ev.ProductVersion(), p.Version())
	}
	d := ev.Def
	if d.IsOrderable() || d.IsDespatchUnit() || !d.EffectiveFrom().Equal(t0) || d.QtyOfChild() != 12 {
		t.Fatalf("def = %+v", d)
	}
	if got, ok := d.DeclaredDimensions(); !ok || got != dims {
		t.Fatalf("dims = %+v %v", got, ok)
	}
	if n := len(p.PackLevels().Levels()); n != 2 {
		t.Fatalf("%d levels, an in-place change must not add one", n)
	}
	// repeating it is no change
	if events, err := p.DeclarePackLevel(s, t2); err != nil || events != nil || p.Version() != 4 {
		t.Fatalf("repeat: events=%v err=%v version=%d", events, err, p.Version())
	}
}

func assertSupersedeEvents(t *testing.T, events []Event) {
	t.Helper()
	retired, ok := events[0].(ProductPackLevelRetired)
	if !ok || retired.ProductVersion() != 4 || retired.Level != PackLevelCase || retired.GTIN != gtinCase || retired.Reason != ReasonSuperseded || !retired.EffectiveTo.Equal(t1) || !retired.OccurredAt().Equal(t1) {
		t.Fatalf("first event = %+v", events[0])
	}
	declared, ok := events[1].(ProductPackLevelDeclared)
	if !ok || declared.ProductVersion() != 5 || declared.Def.GTIN() != gtinCase2 || declared.Def.QtyOfChild() != 24 || declared.Def.UnitsPerPack() != 24 || !declared.Def.EffectiveFrom().Equal(t1) {
		t.Fatalf("second event = %+v", events[1])
	}
}

func TestDeclareANewGTINAtAnActiveLevelEndDatesTheOldOne(t *testing.T) {
	p := withCase(t)
	events, err := p.DeclarePackLevel(caseSpec(gtinCase2, 24), t1)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || p.Version() != 5 {
		t.Fatalf("events %v version %d", events, p.Version())
	}
	assertSupersedeEvents(t, events)
	h := p.PackLevels()
	if active, _ := h.Active(PackLevelCase); active.GTIN() != gtinCase2 {
		t.Fatalf("active case = %s", active.GTIN())
	}
	old, found := h.ByGTIN(gtinCase)
	if !found || old.Active() || old.QtyOfChild() != 12 || old.UnitsPerPack() != 12 {
		t.Fatalf("old def = %+v found=%v: it must be kept, unedited and end-dated", old, found)
	}
	if to, ended := old.EffectiveTo(); !ended || !to.Equal(t1) {
		t.Fatalf("old effectiveTo = %v %v", to, ended)
	}
	// both are listed, lower levels first, the old one before the new one
	var order []string
	for _, d := range h.Levels() {
		order = append(order, d.GTIN().String())
	}
	if strings.Join(order, ",") != strings.Join([]string{gtinEach.String(), gtinCase.String(), gtinCase2.String()}, ",") {
		t.Fatalf("Levels() order = %v", order)
	}
}

func TestDeclareRejectsAGTINAlreadyOnThisProduct(t *testing.T) {
	t.Run("the each GTIN at the case level", func(t *testing.T) {
		p := withEach(t)
		s := caseSpec(gtinEach, 12)
		if _, err := p.DeclarePackLevel(s, t0); !errors.Is(err, ErrGtinAlreadyAssigned) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a case GTIN reused at the pallet level", func(t *testing.T) {
		p := withCase(t)
		s := PackLevelSpec{Level: PackLevelPallet, GTIN: gtinCase, ChildGTIN: gtinEach, QtyOfChild: 100}
		if _, err := p.DeclarePackLevel(s, t0); !errors.Is(err, ErrGtinAlreadyAssigned) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a retired GTIN is never reused, not even at its own level", func(t *testing.T) {
		p := withCase(t)
		if _, err := p.DeclarePackLevel(caseSpec(gtinCase2, 24), t1); err != nil {
			t.Fatal(err)
		}
		before := p.Version()
		events, err := p.DeclarePackLevel(caseSpec(gtinCase, 12), t2)
		if !errors.Is(err, ErrGtinAlreadyAssigned) || events != nil || p.Version() != before {
			t.Fatalf("events=%v err=%v version=%d", events, err, p.Version())
		}
	})
}

// withEachDims is a product whose EACH level declares dims1 (500 g each).
func withEachDims(t *testing.T) *Product {
	t.Helper()
	p := registered(t)
	s := eachSpec()
	d := dims1
	s.Dims = &d
	if _, err := p.DeclarePackLevel(s, t0); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDeclareWeightMustCoverTheContents(t *testing.T) {
	each := withEachDims
	tests := []struct {
		name   string
		weight int64
		ok     bool
	}{
		{"exactly the contents", 6000, true},
		{"one gram below the contents", 5999, false},
		{"heavier than the contents", 6500, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := each(t)
			s := caseSpec(gtinCase, 12)
			d := mustDims(400, 300, 200, tt.weight)
			s.Dims = &d
			_, err := p.DeclarePackLevel(s, t1)
			if tt.ok && err != nil {
				t.Fatalf("err = %v", err)
			}
			if !tt.ok && (!errors.Is(err, ErrPackDimensionsBelowContents) || p.Version() != 2) {
				t.Fatalf("err = %v version = %d", err, p.Version())
			}
		})
	}
}

func TestDeclareWeightWithoutAKnownChildWeight(t *testing.T) {
	each := withEachDims
	t.Run("the child weight is unknown", func(t *testing.T) {
		p := withEach(t) // no dims on the each level, no physical profile
		s := caseSpec(gtinCase, 12)
		d := mustDims(400, 300, 200, 1)
		s.Dims = &d
		if _, err := p.DeclarePackLevel(s, t1); err != nil {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("the case has no dimensions", func(t *testing.T) {
		if _, err := each(t).DeclarePackLevel(caseSpec(gtinCase, 12), t1); err != nil {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestDeclareWeightTakesTheChildWeightFromTheBestSource(t *testing.T) {
	each := withEachDims
	t.Run("the each weight comes from the product's physical profile when the level has none", func(t *testing.T) {
		p := withEach(t)
		p.DeclareDimensions(mustDims(100, 50, 40, 500), t0)
		s := caseSpec(gtinCase, 12)
		d := mustDims(400, 300, 200, 5999)
		s.Dims = &d
		if _, err := p.DeclarePackLevel(s, t1); !errors.Is(err, ErrPackDimensionsBelowContents) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a measured each weight wins over a declared one", func(t *testing.T) {
		p := each(t)
		m, err := NewMeasurement(mustDims(100, 50, 40, 1000), t0, "", t1)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.RecordPackLevelMeasurement(PackLevelEach, m, t1); err != nil {
			t.Fatal(err)
		}
		s := caseSpec(gtinCase, 12)
		d := mustDims(400, 300, 200, 11999)
		s.Dims = &d
		if _, err := p.DeclarePackLevel(s, t1); !errors.Is(err, ErrPackDimensionsBelowContents) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestRetirePackLevel(t *testing.T) {
	p := withCase(t)
	events, err := p.RetirePackLevel(PackLevelCase, "discontinued", t1)
	if err != nil {
		t.Fatal(err)
	}
	ev := onlyEvent(t, events).(ProductPackLevelRetired)
	if ev.EventName() != "ProductPackLevelRetired" || ev.ProductVersion() != 4 || p.Version() != 4 || ev.Level != PackLevelCase || ev.GTIN != gtinCase || ev.Reason != "discontinued" || !ev.EffectiveTo.Equal(t1) || ev.EffectiveTo.Location() != time.UTC {
		t.Fatalf("event = %+v version %d", ev, p.Version())
	}
	if _, ok := p.PackLevels().Active(PackLevelCase); ok {
		t.Fatal("the case level is still active")
	}
	d, found := p.PackLevels().ByGTIN(gtinCase)
	if !found || d.Active() {
		t.Fatalf("retired def = %+v found=%v: it must stay resolvable", d, found)
	}
	if to, ended := d.EffectiveTo(); !ended || !to.Equal(t1) {
		t.Fatalf("effectiveTo = %v %v", to, ended)
	}
}

func TestRetirePackLevelEdges(t *testing.T) {
	p := withCase(t)
	if _, err := p.RetirePackLevel(PackLevelCase, "discontinued", t1); err != nil {
		t.Fatal(err)
	}
	t.Run("retiring again is no change", func(t *testing.T) {
		events, err := p.RetirePackLevel(PackLevelCase, "again", t2)
		if err != nil || events != nil || p.Version() != 4 {
			t.Fatalf("events=%v err=%v version=%d", events, err, p.Version())
		}
		if d, _ := p.PackLevels().ByGTIN(gtinCase); func() bool { to, _ := d.EffectiveTo(); return !to.Equal(t1) }() {
			t.Fatal("the first end date was overwritten")
		}
	})
	t.Run("a level never declared", func(t *testing.T) {
		events, err := p.RetirePackLevel(PackLevelPallet, "x", t2)
		if !errors.Is(err, ErrPackLevelNotFound) || events != nil {
			t.Fatalf("events=%v err=%v", events, err)
		}
	})
	t.Run("an unknown level", func(t *testing.T) {
		if _, err := p.RetirePackLevel("BOGUS", "x", t2); !errors.Is(err, ErrInvalidPackLevel) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a level can be declared again with a new GTIN after it was retired", func(t *testing.T) {
		events, err := p.DeclarePackLevel(caseSpec(gtinCase2, 24), t2)
		if err != nil || len(events) != 1 {
			t.Fatalf("events=%v err=%v: nothing is active to end-date", events, err)
		}
		if _, ok := events[0].(ProductPackLevelDeclared); !ok {
			t.Fatalf("event = %+v", events[0])
		}
	})
}

func TestRetireReason(t *testing.T) {
	for _, bad := range []string{"", strings.Repeat("r", MaxReasonChars+1), "line\nbreak", "tab\tbed"} {
		p := withCase(t)
		events, err := p.RetirePackLevel(PackLevelCase, bad, t1)
		if !errors.Is(err, ErrInvalidReason) || events != nil || p.Version() != 3 {
			t.Fatalf("reason %q: events=%v err=%v version=%d", bad, events, err, p.Version())
		}
	}
	p := withCase(t)
	if _, err := p.RetirePackLevel(PackLevelCase, strings.Repeat("r", MaxReasonChars), t1); err != nil {
		t.Fatalf("a reason of exactly the maximum length: %v", err)
	}
}

func TestRecordPackLevelMeasurement(t *testing.T) {
	p := withCase(t)
	m, err := NewMeasurement(mustDims(410, 310, 210, 7000), t1.Add(-time.Hour), "cubiscan-1", t1)
	if err != nil {
		t.Fatal(err)
	}
	events, err := p.RecordPackLevelMeasurement(PackLevelCase, m, t1)
	if err != nil {
		t.Fatal(err)
	}
	ev := onlyEvent(t, events).(ProductPackLevelMeasured)
	if ev.EventName() != "ProductPackLevelMeasured" || ev.ProductVersion() != 4 || p.Version() != 4 || ev.Level != PackLevelCase || ev.GTIN != gtinCase || ev.Measurement != m {
		t.Fatalf("event = %+v", ev)
	}
	d, _ := p.PackLevels().Active(PackLevelCase)
	if got, ok := d.Measurement(); !ok || got != m {
		t.Fatalf("stored measurement = %+v %v", got, ok)
	}
	if eff, ok := d.EffectiveDimensions(); !ok || eff != m.Dimensions() {
		t.Fatalf("effective = %+v %v: measured wins", eff, ok)
	}

}

func TestRecordPackLevelMeasurementEdges(t *testing.T) {
	p := withCase(t)
	m, err := NewMeasurement(mustDims(410, 310, 210, 7000), t1.Add(-time.Hour), "cubiscan-1", t1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.RecordPackLevelMeasurement(PackLevelCase, m, t1); err != nil {
		t.Fatal(err)
	}
	t.Run("the same reading is no change", func(t *testing.T) {
		events, err := p.RecordPackLevelMeasurement(PackLevelCase, m, t2)
		if err != nil || events != nil || p.Version() != 4 {
			t.Fatalf("events=%v err=%v version=%d", events, err, p.Version())
		}
	})
	t.Run("an older reading is stale", func(t *testing.T) {
		old, _ := NewMeasurement(mustDims(410, 310, 210, 7000), t1.Add(-2*time.Hour), "", t1)
		events, err := p.RecordPackLevelMeasurement(PackLevelCase, old, t2)
		if !errors.Is(err, ErrStaleMeasurement) || events != nil || p.Version() != 4 {
			t.Fatalf("events=%v err=%v version=%d", events, err, p.Version())
		}
	})
	t.Run("a newer reading replaces it", func(t *testing.T) {
		newer, _ := NewMeasurement(mustDims(411, 311, 211, 7100), t1, "", t2)
		events, err := p.RecordPackLevelMeasurement(PackLevelCase, newer, t2)
		if err != nil || len(events) != 1 || p.Version() != 5 {
			t.Fatalf("events=%v err=%v version=%d", events, err, p.Version())
		}
	})
}

func TestRecordPackLevelMeasurementNeedsAnActiveLevel(t *testing.T) {
	p := withCase(t)
	m, err := NewMeasurement(mustDims(410, 310, 210, 7000), t1.Add(-time.Hour), "cubiscan-1", t1)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("a level with no active definition", func(t *testing.T) {
		if _, err := p.RecordPackLevelMeasurement(PackLevelPallet, m, t2); !errors.Is(err, ErrPackLevelNotFound) {
			t.Fatalf("never declared: %v", err)
		}
		if _, err := p.RetirePackLevel(PackLevelCase, "x", t2); err != nil {
			t.Fatal(err)
		}
		newest, _ := NewMeasurement(mustDims(411, 311, 211, 7100), t2, "", t2)
		if _, err := p.RecordPackLevelMeasurement(PackLevelCase, newest, t2); !errors.Is(err, ErrPackLevelNotFound) {
			t.Fatalf("retired: %v", err)
		}
	})
	t.Run("an unknown level", func(t *testing.T) {
		if _, err := p.RecordPackLevelMeasurement("BOGUS", m, t2); !errors.Is(err, ErrInvalidPackLevel) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestRecordPackLevelMeasurementWeightMustCoverTheContents(t *testing.T) {
	p := registered(t)
	s := eachSpec()
	d := dims1
	s.Dims = &d
	if _, err := p.DeclarePackLevel(s, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := p.DeclarePackLevel(caseSpec(gtinCase, 12), t0); err != nil {
		t.Fatal(err)
	}
	light, _ := NewMeasurement(mustDims(400, 300, 200, 5999), t1, "", t1)
	if events, err := p.RecordPackLevelMeasurement(PackLevelCase, light, t1); !errors.Is(err, ErrPackDimensionsBelowContents) || events != nil || p.Version() != 3 {
		t.Fatalf("events=%v err=%v version=%d", events, err, p.Version())
	}
	exact, _ := NewMeasurement(mustDims(400, 300, 200, 6000), t1, "", t1)
	if _, err := p.RecordPackLevelMeasurement(PackLevelCase, exact, t1); err != nil {
		t.Fatalf("exactly the contents: %v", err)
	}
	// the each level has no child, so any weight is fine
	tiny, _ := NewMeasurement(mustDims(1, 1, 1, 1), t1, "", t1)
	if _, err := p.RecordPackLevelMeasurement(PackLevelEach, tiny, t1); err != nil {
		t.Fatalf("each: %v", err)
	}
}

func TestPackagingHierarchyAccessorsAreCopies(t *testing.T) {
	p := withCase(t)
	levels := p.PackLevels().Levels()
	if len(levels) != 2 || levels[0].Level() != PackLevelEach || levels[1].Level() != PackLevelCase {
		t.Fatalf("levels = %+v", levels)
	}
	levels[0] = levels[1]
	if p.PackLevels().Levels()[0].Level() != PackLevelEach {
		t.Fatal("Levels() leaked the aggregate's slice")
	}
	if _, ok := p.PackLevels().ByGTIN(gtinOther); ok {
		t.Fatal("ByGTIN found a stranger")
	}
	if _, ok := p.PackLevels().Active(PackLevelPallet); ok {
		t.Fatal("Active found a level that was never declared")
	}
	if len(registered(t).PackLevels().Levels()) != 0 {
		t.Fatal("a new product has pack levels")
	}
}

func TestRehydrateWithPackaging(t *testing.T) {
	p := withCase(t)
	m, _ := NewMeasurement(mustDims(410, 310, 210, 7000), t1, "dev", t1)
	if _, err := p.RecordPackLevelMeasurement(PackLevelCase, m, t1); err != nil {
		t.Fatal(err)
	}
	var defs []PackLevelDef
	for _, d := range p.PackLevels().Levels() {
		var declared *UnitDimensions
		if x, ok := d.DeclaredDimensions(); ok {
			declared = &x
		}
		var measured *Measurement
		if x, ok := d.Measurement(); ok {
			measured = &x
		}
		var to *time.Time
		if x, ended := d.EffectiveTo(); ended {
			to = &x
		}
		defs = append(defs, RehydratePackLevelDef(PackLevelSpec{
			Level: d.Level(), GTIN: d.GTIN(), ChildGTIN: d.ChildGTIN(), QtyOfChild: d.QtyOfChild(), UnitsPerPack: d.UnitsPerPack(),
			Dims: declared, IsOrderable: d.IsOrderable(), IsDespatchUnit: d.IsDespatchUnit(),
		}, measured, d.EffectiveFrom(), to))
	}
	again := RehydrateWithPackaging("SKU-1", "battery", nil, "", PhysicalProfile{}, RehydratePackagingHierarchy(defs), p.Version())
	if again.Version() != p.Version() || len(again.PackLevels().Levels()) != 2 {
		t.Fatalf("rehydrated = %+v", again.PackLevels())
	}
	got, _ := again.PackLevels().Active(PackLevelCase)
	want, _ := p.PackLevels().Active(PackLevelCase)
	if got.GTIN() != want.GTIN() || got.UnitsPerPack() != want.UnitsPerPack() || !got.EffectiveFrom().Equal(want.EffectiveFrom()) {
		t.Fatalf("got %+v want %+v", got, want)
	}
	if gm, _ := got.Measurement(); gm != m {
		t.Fatalf("measurement = %+v", gm)
	}
	// plain Rehydrate keeps meaning "no packaging"
	if n := len(Rehydrate("SKU-1", "battery", nil, "", PhysicalProfile{}, 1).PackLevels().Levels()); n != 0 {
		t.Fatalf("Rehydrate carries %d levels", n)
	}
	// and a rehydrated aggregate keeps enforcing the rules
	if _, err := again.DeclarePackLevel(caseSpec(gtinCase, 99), t2); !errors.Is(err, ErrPackQuantityImmutable) {
		t.Fatalf("err = %v", err)
	}
}

func TestDeclareANewEachGTINSupersedesTheFirstLevel(t *testing.T) {
	p := withEach(t) // the each level is the first definition of the product
	s := eachSpec()
	s.GTIN = gtinOther
	events, err := p.DeclarePackLevel(s, t1)
	if err != nil || len(events) != 2 {
		t.Fatalf("events=%v err=%v", events, err)
	}
	if r, ok := events[0].(ProductPackLevelRetired); !ok || r.GTIN != gtinEach || r.Reason != ReasonSuperseded {
		t.Fatalf("first event = %+v", events[0])
	}
	if d, _ := p.PackLevels().Active(PackLevelEach); d.GTIN() != gtinOther {
		t.Fatalf("active each = %s", d.GTIN())
	}
}

func TestRedeclareInPlaceChecksTheWeightAgainstTheContents(t *testing.T) {
	p := registered(t)
	s := eachSpec()
	d := dims1 // 500 g
	s.Dims = &d
	if _, err := p.DeclarePackLevel(s, t0); err != nil {
		t.Fatal(err)
	}
	cs := caseSpec(gtinCase, 12)
	if _, err := p.DeclarePackLevel(cs, t0); err != nil {
		t.Fatal(err)
	}
	light := mustDims(400, 300, 200, 5999)
	cs.Dims = &light
	events, err := p.DeclarePackLevel(cs, t1)
	if !errors.Is(err, ErrPackDimensionsBelowContents) || events != nil || p.Version() != 3 {
		t.Fatalf("events=%v err=%v version=%d", events, err, p.Version())
	}
	ok := mustDims(400, 300, 200, 6000)
	cs.Dims = &ok
	if _, err := p.DeclarePackLevel(cs, t1); err != nil {
		t.Fatalf("exactly the contents: %v", err)
	}
}

func TestDeclareDoesNotAliasTheCallersDimensions(t *testing.T) {
	p := registered(t)
	s := eachSpec()
	d := dims1
	s.Dims = &d
	if _, err := p.DeclarePackLevel(s, t0); err != nil {
		t.Fatal(err)
	}
	d = mustDims(1, 1, 1, 1) // the caller reuses its variable
	if got, _ := p.PackLevels().Active(PackLevelEach); func() bool { x, _ := got.DeclaredDimensions(); return x != dims1 }() {
		t.Fatal("the stored dimensions followed the caller's variable")
	}
}

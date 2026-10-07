package product

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func registered(t *testing.T) *Product {
	t.Helper()
	p, _, err := Register("SKU-1", "battery", t0)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	return p
}

func onlyEvent(t *testing.T, events []Event) Event {
	t.Helper()
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1: %+v", len(events), events)
	}
	return events[0]
}

func assertHeader(t *testing.T, e Event, name string, version int64) {
	t.Helper()
	if e.EventName() != name || e.ProductSKU() != "SKU-1" || e.ProductVersion() != version || !e.OccurredAt().Equal(t0) {
		t.Fatalf("event = %s sku=%s v=%d at=%v, want %s v=%d", e.EventName(), e.ProductSKU(), e.ProductVersion(), e.OccurredAt(), name, version)
	}
	if e.OccurredAt().Location() != time.UTC {
		t.Fatalf("event time not UTC: %v", e.OccurredAt().Location())
	}
}

func TestRegister(t *testing.T) {
	local := t0.In(time.FixedZone("BRT", -3*3600))
	p, events, err := Register("SKU-1", "battery", local)
	if err != nil {
		t.Fatal(err)
	}
	if p.SKU() != "SKU-1" || p.Description() != "battery" || p.Version() != 1 || p.IsClassified() {
		t.Fatalf("product = %+v", p)
	}
	e := onlyEvent(t, events)
	assertHeader(t, e, "ProductRegistered", 1)
	if e.(ProductRegistered).Description != "battery" {
		t.Fatalf("description = %q", e.(ProductRegistered).Description)
	}
	if p.PhysicalProfile().EffectiveSource() != EffectiveNone {
		t.Fatal("new product has a physical profile")
	}
	if _, _, err := p.Classification(); !errors.Is(err, ErrNotClassified) {
		t.Fatalf("Classification err = %v", err)
	}
}

func TestRegisterRejects(t *testing.T) {
	if p, ev, err := Register("bad sku", "", t0); !errors.Is(err, ErrInvalidSKU) || p != nil || ev != nil {
		t.Fatalf("bad sku: %v %v %v", p, ev, err)
	}
	if p, ev, err := Register("SKU-1", strings.Repeat("a", 201), t0); !errors.Is(err, ErrInvalidDescription) || p != nil || ev != nil {
		t.Fatalf("bad description: %v %v %v", p, ev, err)
	}
}

func TestChangeDescription(t *testing.T) {
	p := registered(t)
	events, err := p.ChangeDescription("battery", t0)
	if err != nil || events != nil || p.Version() != 1 {
		t.Fatalf("same description: events=%v err=%v v=%d", events, err, p.Version())
	}
	events, err = p.ChangeDescription("battery 7Ah", t0)
	if err != nil {
		t.Fatal(err)
	}
	e := onlyEvent(t, events)
	assertHeader(t, e, "ProductDescriptionChanged", 2)
	if e.(ProductDescriptionChanged).Description != "battery 7Ah" || p.Description() != "battery 7Ah" {
		t.Fatal("description not changed")
	}
	if events, err := p.ChangeDescription("x\ny", t0); !errors.Is(err, ErrInvalidDescription) || events != nil || p.Version() != 2 {
		t.Fatalf("invalid description: %v %v v=%d", events, err, p.Version())
	}
}

func TestClassify(t *testing.T) {
	p := registered(t)
	c := mustClassification(t, []HandlingTag{Hazmat}, NoTemperatureClass, 3)
	events, err := p.Classify(c, t0)
	if err != nil {
		t.Fatal(err)
	}
	e := onlyEvent(t, events).(ProductClassified)
	assertHeader(t, e, "ProductClassified", 2)
	if !e.Classification.Equal(c) || e.Source != SourceNative {
		t.Fatalf("event = %+v", e)
	}
	got, src, err := p.Classification()
	if err != nil || !got.Equal(c) || src != SourceNative || !p.IsClassified() {
		t.Fatalf("Classification() = %+v %q %v", got, src, err)
	}

	// Same classification again: no change.
	events, _ = p.Classify(mustClassification(t, []HandlingTag{Hazmat}, NoTemperatureClass, 3), t0)
	if events != nil || p.Version() != 2 {
		t.Fatalf("identical classify: events=%v v=%d", events, p.Version())
	}

	// Replacement.
	c2 := mustClassification(t, []HandlingTag{Fragile}, NoTemperatureClass, NoDOTHazardClass)
	events, _ = p.Classify(c2, t0)
	assertHeader(t, onlyEvent(t, events), "ProductClassified", 3)
	if got, _, _ := p.Classification(); !got.Equal(c2) {
		t.Fatal("classification not replaced")
	}
}

func TestImportLegacyClassification(t *testing.T) {
	legacy := mustClassification(t, []HandlingTag{Hazmat}, NoTemperatureClass, 9)

	t.Run("unclassified product takes the import", func(t *testing.T) {
		p := registered(t)
		e := onlyEvent(t, p.ImportLegacyClassification(legacy, t0)).(ProductClassified)
		assertHeader(t, e, "ProductClassified", 2)
		if e.Source != SourceLegacyImport {
			t.Fatalf("source = %q", e.Source)
		}
		if _, src, _ := p.Classification(); src != SourceLegacyImport {
			t.Fatalf("stored source = %q", src)
		}
	})

	t.Run("identical legacy import is no change", func(t *testing.T) {
		p := registered(t)
		p.ImportLegacyClassification(legacy, t0)
		if events := p.ImportLegacyClassification(legacy, t0); events != nil || p.Version() != 2 {
			t.Fatalf("events=%v v=%d", events, p.Version())
		}
	})

	t.Run("newer legacy import replaces a legacy one", func(t *testing.T) {
		p := registered(t)
		p.ImportLegacyClassification(legacy, t0)
		other := mustClassification(t, []HandlingTag{Fragile}, NoTemperatureClass, NoDOTHazardClass)
		assertHeader(t, onlyEvent(t, p.ImportLegacyClassification(other, t0)), "ProductClassified", 3)
	})

	t.Run("never overwrites a native classification", func(t *testing.T) {
		p := registered(t)
		native := mustClassification(t, []HandlingTag{Fragile}, NoTemperatureClass, NoDOTHazardClass)
		if _, err := p.Classify(native, t0); err != nil {
			t.Fatal(err)
		}
		if events := p.ImportLegacyClassification(legacy, t0); events != nil || p.Version() != 2 {
			t.Fatalf("events=%v v=%d", events, p.Version())
		}
		if got, src, _ := p.Classification(); !got.Equal(native) || src != SourceNative {
			t.Fatal("native classification overwritten")
		}
	})

	t.Run("confirming a legacy classification makes it native", func(t *testing.T) {
		p := registered(t)
		p.ImportLegacyClassification(legacy, t0)
		events, _ := p.Classify(legacy, t0)
		e := onlyEvent(t, events).(ProductClassified)
		if e.Source != SourceNative || e.Version != 3 {
			t.Fatalf("event = %+v", e)
		}
	})
}

func TestDeclareDimensions(t *testing.T) {
	p := registered(t)
	d := dims(t, 200, 120, 80, 1500)
	e := onlyEvent(t, p.DeclareDimensions(d, t0)).(ProductDimensionsDeclared)
	assertHeader(t, e, "ProductDimensionsDeclared", 2)
	if got, ok := e.Profile.Declared(); !ok || got != d || e.Profile.EffectiveSource() != EffectiveDeclared {
		t.Fatalf("event profile = %+v", e.Profile)
	}
	if events := p.DeclareDimensions(dims(t, 200, 120, 80, 1500), t0); events != nil || p.Version() != 2 {
		t.Fatalf("identical declare: events=%v v=%d", events, p.Version())
	}
	assertHeader(t, onlyEvent(t, p.DeclareDimensions(dims(t, 200, 120, 80, 1600), t0)), "ProductDimensionsDeclared", 3)
	if got, _ := p.PhysicalProfile().Declared(); got.WeightG() != 1600 {
		t.Fatal("declared not replaced")
	}
}

func measuredProduct(t *testing.T) *Product {
	t.Helper()
	p := registered(t)
	p.DeclareDimensions(dims(t, 10, 10, 10, 1000), t0)
	events, err := p.RecordMeasurement(RehydrateMeasurement(dims(t, 10, 10, 10, 1200), t0.Add(-time.Hour), "C1"), t0)
	if err != nil {
		t.Fatal(err)
	}
	e := onlyEvent(t, events).(ProductMeasured)
	assertHeader(t, e, "ProductMeasured", 3)
	if !e.Profile.Discrepancy() || e.Profile.EffectiveSource() != EffectiveMeasured {
		t.Fatalf("profile = discrepancy %v source %q", e.Profile.Discrepancy(), e.Profile.EffectiveSource())
	}
	return p
}

func TestRecordMeasurementIdenticalIsNoChange(t *testing.T) {
	p := measuredProduct(t)
	events, err := p.RecordMeasurement(RehydrateMeasurement(dims(t, 10, 10, 10, 1200), t0.Add(-time.Hour), "C1"), t0)
	if err != nil || events != nil || p.Version() != 3 {
		t.Fatalf("identical: events=%v err=%v v=%d", events, err, p.Version())
	}
}

func TestRecordMeasurementCorrectionReplaces(t *testing.T) {
	p := measuredProduct(t)
	events, err := p.RecordMeasurement(RehydrateMeasurement(dims(t, 10, 10, 10, 1050), t0.Add(-time.Hour), "C1"), t0)
	if err != nil {
		t.Fatal(err)
	}
	assertHeader(t, onlyEvent(t, events), "ProductMeasured", 4)
	if p.PhysicalProfile().Discrepancy() {
		t.Fatal("corrected reading still flagged")
	}
}

func TestRecordMeasurementStaleRejected(t *testing.T) {
	p := measuredProduct(t)
	events, err := p.RecordMeasurement(RehydrateMeasurement(dims(t, 10, 10, 10, 999), t0.Add(-2*time.Hour), "C2"), t0)
	if !errors.Is(err, ErrStaleMeasurement) || events != nil || p.Version() != 3 {
		t.Fatalf("stale: events=%v err=%v v=%d", events, err, p.Version())
	}
}

func TestRecordMeasurementNewerWins(t *testing.T) {
	p := measuredProduct(t)
	events, err := p.RecordMeasurement(RehydrateMeasurement(dims(t, 10, 10, 10, 1001), t0, "C2"), t0)
	if err != nil {
		t.Fatal(err)
	}
	assertHeader(t, onlyEvent(t, events), "ProductMeasured", 4)
	if m, _ := p.PhysicalProfile().Measured(); m.DeviceID() != "C2" {
		t.Fatal("newer reading not recorded")
	}
	if d, ok := p.PhysicalProfile().Declared(); !ok || d.WeightG() != 1000 {
		t.Fatal("measurement dropped the declared dimensions")
	}
}

func TestRecordMeasurementWithoutDeclared(t *testing.T) {
	p := registered(t)
	events, err := p.RecordMeasurement(RehydrateMeasurement(dims(t, 1, 1, 1, 1), t0, ""), t0)
	if err != nil {
		t.Fatal(err)
	}
	e := onlyEvent(t, events).(ProductMeasured)
	if e.Profile.Discrepancy() || e.Profile.EffectiveSource() != EffectiveMeasured || e.Version != 2 {
		t.Fatalf("event = %+v", e)
	}
}

func TestRehydrate(t *testing.T) {
	c := RehydrateClassification([]HandlingTag{Fragile}, NoTemperatureClass, NoDOTHazardClass)
	d := dims(t, 1, 2, 3, 4)
	p := Rehydrate("SKU-9", "x", &c, SourceLegacyImport, RehydratePhysicalProfile(&d, nil), 7)
	got, src, err := p.Classification()
	if err != nil || !got.HasTag(Fragile) || src != SourceLegacyImport {
		t.Fatalf("classification = %+v %q %v", got, src, err)
	}
	if p.SKU() != "SKU-9" || p.Description() != "x" || p.Version() != 7 {
		t.Fatalf("product = %+v", p)
	}
	if e, ok := p.PhysicalProfile().Effective(); !ok || e != d {
		t.Fatal("profile lost")
	}
	events, _ := p.ChangeDescription("y", t0)
	if onlyEvent(t, events).ProductVersion() != 8 {
		t.Fatal("version does not continue from the persisted one")
	}
}

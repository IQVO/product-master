package usecases_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/claudioed/product-master/internal/application/repository"
	"github.com/claudioed/product-master/internal/application/usecases"
	"github.com/claudioed/product-master/internal/domain/product"
)

func TestRegisterProduct_CreatesThenChangesDescription(t *testing.T) {
	h := newHarness()
	uc := &usecases.RegisterProduct{Writer: h.writer}
	ctx := context.Background()

	res, err := uc.Handle(ctx, usecases.RegisterProductCommand{SKU: "SKU-1", Description: "Battery"})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if !res.Created || res.Product.Version() != 1 || res.Product.Description() != "Battery" {
		t.Fatalf("got created=%v version=%d desc=%q", res.Created, res.Product.Version(), res.Product.Description())
	}
	if len(h.repo.saves) != 1 || h.repo.saves[0].loadedVersion != 0 {
		t.Fatalf("saves = %+v, want one insert (loaded 0)", h.repo.saves)
	}

	res, err = uc.Handle(ctx, usecases.RegisterProductCommand{SKU: "SKU-1", Description: "Battery 12V"})
	if err != nil {
		t.Fatalf("change: %v", err)
	}
	if res.Created || res.Product.Version() != 2 || res.Product.Description() != "Battery 12V" {
		t.Fatalf("got created=%v version=%d desc=%q", res.Created, res.Product.Version(), res.Product.Description())
	}
	if len(h.repo.saves) != 2 || h.repo.saves[1].loadedVersion != 1 || h.repo.saves[1].version != 2 {
		t.Fatalf("saves = %+v, want an update guarded by version 1", h.repo.saves)
	}
	want := []string{"ProductRegistered", "ProductDescriptionChanged"}
	if got := h.outbox.types(); !reflect.DeepEqual(got, want) {
		t.Fatalf("outbox = %v, want %v", got, want)
	}
}

func TestRegisterProduct_StampsEventsWithTheClock(t *testing.T) {
	h := newHarness()
	if _, err := (&usecases.RegisterProduct{Writer: h.writer}).Handle(context.Background(), usecases.RegisterProductCommand{SKU: "SKU-1"}); err != nil {
		t.Fatal(err)
	}
	if !h.enc.events[0].OccurredAt().Equal(t0) {
		t.Fatalf("event time = %v, want the clock's %v", h.enc.events[0].OccurredAt(), t0)
	}
}

func TestRegisterProduct_SameDescriptionChangesNothing(t *testing.T) {
	h := newHarness()
	h.registered("SKU-1")
	res, err := (&usecases.RegisterProduct{Writer: h.writer}).Handle(context.Background(), usecases.RegisterProductCommand{SKU: "SKU-1", Description: "desc"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Created || res.Product.Version() != 1 {
		t.Fatalf("created=%v version=%d, want an unchanged existing product", res.Created, res.Product.Version())
	}
	if len(h.repo.saves) != 0 || len(h.outbox.msgs) != 0 {
		t.Fatalf("saves=%d outbox=%d, want nothing persisted", len(h.repo.saves), len(h.outbox.msgs))
	}
}

func TestRegisterProduct_Errors(t *testing.T) {
	long := strings.Repeat("x", product.MaxDescriptionLength+1)
	cases := []struct {
		name  string
		setup func(h *harness)
		cmd   usecases.RegisterProductCommand
		want  error
	}{
		{"invalid sku", nil, usecases.RegisterProductCommand{SKU: "bad sku"}, product.ErrInvalidSKU},
		{"invalid description on create", nil, usecases.RegisterProductCommand{SKU: "SKU-1", Description: long}, product.ErrInvalidDescription},
		{"invalid description on change", func(h *harness) { h.registered("SKU-1") }, usecases.RegisterProductCommand{SKU: "SKU-1", Description: long}, product.ErrInvalidDescription},
		{"get fails", func(h *harness) { h.repo.getErr = errBoom }, usecases.RegisterProductCommand{SKU: "SKU-1"}, errBoom},
		{"save loses the race", func(h *harness) { h.repo.saveErr = repository.ErrConcurrentModification }, usecases.RegisterProductCommand{SKU: "SKU-1"}, repository.ErrConcurrentModification},
		{"encode fails", func(h *harness) { h.enc.err = errBoom }, usecases.RegisterProductCommand{SKU: "SKU-1"}, errBoom},
		{"enqueue fails", func(h *harness) { h.outbox.err = errBoom }, usecases.RegisterProductCommand{SKU: "SKU-1"}, errBoom},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness()
			if tc.setup != nil {
				tc.setup(h)
			}
			res, err := (&usecases.RegisterProduct{Writer: h.writer}).Handle(context.Background(), tc.cmd)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if res.Product != nil || res.Created {
				t.Fatalf("result on error = %+v, want zero", res)
			}
			if len(h.outbox.msgs) != 0 {
				t.Fatalf("outbox has %d messages after an error", len(h.outbox.msgs))
			}
		})
	}
}

func TestClassifyProduct_FirstIsCreatedThenReplaced(t *testing.T) {
	h := newHarness()
	h.registered("SKU-1")
	uc := &usecases.ClassifyProduct{Writer: h.writer}
	ctx := context.Background()

	res, err := uc.Handle(ctx, usecases.ClassifyProductCommand{SKU: "SKU-1", HandlingTags: []string{"TemperatureSensitive", "Hazmat"}, TemperatureClass: "Frozen", DOTHazardClass: 3})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || res.Product.Version() != 2 {
		t.Fatalf("created=%v version=%d, want created at version 2", res.Created, res.Product.Version())
	}
	c, src, _ := res.Product.Classification()
	if src != product.SourceNative || c.TemperatureClass() != product.Frozen || c.DOTHazardClass() != 3 {
		t.Fatalf("classification = %v %v %v", src, c.TemperatureClass(), c.DOTHazardClass())
	}

	res, err = uc.Handle(ctx, usecases.ClassifyProductCommand{SKU: "SKU-1", HandlingTags: []string{"Fragile"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Created || res.Product.Version() != 3 {
		t.Fatalf("created=%v version=%d, want replaced at version 3", res.Created, res.Product.Version())
	}
	if got := h.outbox.types(); !reflect.DeepEqual(got, []string{"ProductClassified", "ProductClassified"}) {
		t.Fatalf("outbox = %v", got)
	}

	res, err = uc.Handle(ctx, usecases.ClassifyProductCommand{SKU: "SKU-1", HandlingTags: []string{"Fragile"}})
	if err != nil || res.Created || res.Product.Version() != 3 || len(h.outbox.msgs) != 2 {
		t.Fatalf("identical classify: err=%v created=%v version=%d outbox=%d", err, res.Created, res.Product.Version(), len(h.outbox.msgs))
	}
}

func TestClassifyProduct_ConfirmingALegacyClassificationIsAReplacement(t *testing.T) {
	h := newHarness()
	p := h.registered("SKU-1")
	p.ImportLegacyClassification(mustClassification([]product.HandlingTag{product.Fragile}, "", 0), t0)
	h.repo.put(p)
	res, err := (&usecases.ClassifyProduct{Writer: h.writer}).Handle(context.Background(), usecases.ClassifyProductCommand{SKU: "SKU-1", HandlingTags: []string{"Fragile"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Created || res.Product.Version() != 3 {
		t.Fatalf("created=%v version=%d, want 200 and a version bump", res.Created, res.Product.Version())
	}
}

func TestClassifyProduct_Errors(t *testing.T) {
	cases := []struct {
		name string
		cmd  usecases.ClassifyProductCommand
		want error
	}{
		{"invalid sku", usecases.ClassifyProductCommand{SKU: ""}, product.ErrInvalidSKU},
		{"no tags", usecases.ClassifyProductCommand{SKU: "SKU-1"}, product.ErrNoHandlingTags},
		{"unknown tag", usecases.ClassifyProductCommand{SKU: "SKU-1", HandlingTags: []string{"Sharp"}}, product.ErrUnknownHandlingTag},
		{"duplicate tag", usecases.ClassifyProductCommand{SKU: "SKU-1", HandlingTags: []string{"Fragile", "Fragile"}}, product.ErrDuplicateHandlingTag},
		{"temperature required", usecases.ClassifyProductCommand{SKU: "SKU-1", HandlingTags: []string{"TemperatureSensitive"}}, product.ErrTemperatureClassRequired},
		{"temperature not applicable", usecases.ClassifyProductCommand{SKU: "SKU-1", HandlingTags: []string{"Fragile"}, TemperatureClass: "Frozen"}, product.ErrTemperatureClassNotApplicable},
		{"unknown temperature", usecases.ClassifyProductCommand{SKU: "SKU-1", HandlingTags: []string{"TemperatureSensitive"}, TemperatureClass: "Hot"}, product.ErrUnknownTemperatureClass},
		{"dot out of range", usecases.ClassifyProductCommand{SKU: "SKU-1", HandlingTags: []string{"Hazmat"}, DOTHazardClass: 10}, product.ErrInvalidDOTHazardClass},
		{"dot without hazmat", usecases.ClassifyProductCommand{SKU: "SKU-1", HandlingTags: []string{"Fragile"}, DOTHazardClass: 3}, product.ErrDOTHazardClassNotApplicable},
		{"not registered", usecases.ClassifyProductCommand{SKU: "SKU-2", HandlingTags: []string{"Fragile"}}, repository.ErrProductNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness()
			h.registered("SKU-1")
			res, err := (&usecases.ClassifyProduct{Writer: h.writer}).Handle(context.Background(), tc.cmd)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if res.Product != nil || res.Created || len(h.repo.saves) != 0 {
				t.Fatalf("result=%+v saves=%d, want nothing", res, len(h.repo.saves))
			}
		})
	}
}

func TestDeclareDimensions(t *testing.T) {
	h := newHarness()
	h.registered("SKU-1")
	uc := &usecases.DeclareDimensions{Writer: h.writer}
	ctx := context.Background()
	cmd := usecases.DeclareDimensionsCommand{SKU: "SKU-1", DimensionsInput: usecases.DimensionsInput{LengthMm: 200, WidthMm: 120, HeightMm: 80, WeightG: 1500}}

	p, err := uc.Handle(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	d, ok := p.PhysicalProfile().Declared()
	if !ok || d.VolumeMm3() != 1920000 || p.Version() != 2 {
		t.Fatalf("declared=%v ok=%v version=%d", d, ok, p.Version())
	}
	if p, err = uc.Handle(ctx, cmd); err != nil || p.Version() != 2 || len(h.outbox.msgs) != 1 {
		t.Fatalf("identical declare: err=%v version=%d outbox=%d", err, p.Version(), len(h.outbox.msgs))
	}
	if got := h.outbox.types(); got[0] != "ProductDimensionsDeclared" {
		t.Fatalf("outbox = %v", got)
	}

	for name, tc := range map[string]struct {
		cmd  usecases.DeclareDimensionsCommand
		want error
	}{
		"invalid sku":    {usecases.DeclareDimensionsCommand{SKU: "a/b", DimensionsInput: cmd.DimensionsInput}, product.ErrInvalidSKU},
		"bad dimension":  {usecases.DeclareDimensionsCommand{SKU: "SKU-1", DimensionsInput: usecases.DimensionsInput{LengthMm: 0, WidthMm: 1, HeightMm: 1, WeightG: 1}}, product.ErrInvalidDimension},
		"bad weight":     {usecases.DeclareDimensionsCommand{SKU: "SKU-1", DimensionsInput: usecases.DimensionsInput{LengthMm: 1, WidthMm: 1, HeightMm: 1, WeightG: 0}}, product.ErrInvalidWeight},
		"not registered": {usecases.DeclareDimensionsCommand{SKU: "SKU-9", DimensionsInput: cmd.DimensionsInput}, repository.ErrProductNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			if p, err := uc.Handle(ctx, tc.cmd); !errors.Is(err, tc.want) || p != nil {
				t.Fatalf("got %v, %v; want nil, %v", p, err, tc.want)
			}
		})
	}
}

func TestRecordMeasurement(t *testing.T) {
	h := newHarness()
	h.registered("SKU-1")
	uc := &usecases.RecordMeasurement{Writer: h.writer}
	ctx := context.Background()
	dims := usecases.DimensionsInput{LengthMm: 205, WidthMm: 121, HeightMm: 82, WeightG: 1720}
	at := t0.Add(-time1h)

	p, err := uc.Handle(ctx, usecases.RecordMeasurementCommand{SKU: "SKU-1", DimensionsInput: dims, MeasuredAt: at, DeviceID: "CUBISCAN-03"})
	if err != nil {
		t.Fatal(err)
	}
	m, ok := p.PhysicalProfile().Measured()
	if !ok || !m.MeasuredAt().Equal(at) || m.DeviceID() != "CUBISCAN-03" || p.Version() != 2 {
		t.Fatalf("measured=%v ok=%v version=%d", m, ok, p.Version())
	}
	if got := h.outbox.types(); !reflect.DeepEqual(got, []string{"ProductMeasured"}) {
		t.Fatalf("outbox = %v", got)
	}

	// Exactly the clock's now is not in the future.
	if _, err := uc.Handle(ctx, usecases.RecordMeasurementCommand{SKU: "SKU-1", DimensionsInput: dims, MeasuredAt: t0}); err != nil {
		t.Fatalf("measurement at now: %v", err)
	}

	for name, tc := range map[string]struct {
		cmd  usecases.RecordMeasurementCommand
		want error
	}{
		"in the future":  {usecases.RecordMeasurementCommand{SKU: "SKU-1", DimensionsInput: dims, MeasuredAt: t0.Add(1)}, product.ErrMeasuredAtInFuture},
		"stale":          {usecases.RecordMeasurementCommand{SKU: "SKU-1", DimensionsInput: dims, MeasuredAt: at.Add(-time1h)}, product.ErrStaleMeasurement},
		"missing time":   {usecases.RecordMeasurementCommand{SKU: "SKU-1", DimensionsInput: dims}, product.ErrMissingMeasuredAt},
		"bad dimension":  {usecases.RecordMeasurementCommand{SKU: "SKU-1", DimensionsInput: usecases.DimensionsInput{LengthMm: 20001, WidthMm: 1, HeightMm: 1, WeightG: 1}, MeasuredAt: at}, product.ErrInvalidDimension},
		"invalid sku":    {usecases.RecordMeasurementCommand{SKU: "", DimensionsInput: dims, MeasuredAt: at}, product.ErrInvalidSKU},
		"not registered": {usecases.RecordMeasurementCommand{SKU: "SKU-9", DimensionsInput: dims, MeasuredAt: at}, repository.ErrProductNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			if p, err := uc.Handle(ctx, tc.cmd); !errors.Is(err, tc.want) || p != nil {
				t.Fatalf("got %v, %v; want nil, %v", p, err, tc.want)
			}
		})
	}
}

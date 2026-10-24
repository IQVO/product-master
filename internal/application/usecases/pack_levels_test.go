package usecases_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/product-master/internal/application/repository"
	"github.com/claudioed/product-master/internal/application/usecases"
	"github.com/claudioed/product-master/internal/domain/product"
	"github.com/claudioed/product-master/internal/domain/shared/gs1"
)

// Valid GTINs (mod-10 checked): an EAN-13, three GTIN-14s, an indicator-9
// GTIN-14 and a second EAN-13 for another SKU.
const (
	gtinEach  = "4006381333931"
	gtinCase  = "24006381333935"
	gtinCase2 = "34006381333932"
	gtinVar   = "94006381333934"
	gtinOther = "5901234123457"
)

func eachInput() usecases.PackLevelInput {
	return usecases.PackLevelInput{Level: "EACH", GTIN: gtinEach, IsOrderable: true}
}

func caseInput(gtin string, qty int) usecases.PackLevelInput {
	return usecases.PackLevelInput{Level: "CASE", GTIN: gtin, ChildGTIN: gtinEach, QtyOfChild: qty, IsOrderable: true, IsDespatchUnit: true}
}

func declare(t *testing.T, h *harness, sku string, in usecases.PackLevelInput) usecases.DeclarePackLevelResult {
	t.Helper()
	res, err := (&usecases.DeclarePackLevel{Writer: h.writer}).Handle(context.Background(), usecases.DeclarePackLevelCommand{SKU: sku, PackLevelInput: in})
	if err != nil {
		t.Fatalf("declare %s %s: %v", sku, in.Level, err)
	}
	return res
}

func declareErr(h *harness, sku string, in usecases.PackLevelInput) error {
	_, err := (&usecases.DeclarePackLevel{Writer: h.writer}).Handle(context.Background(), usecases.DeclarePackLevelCommand{SKU: sku, PackLevelInput: in})
	return err
}

func TestDeclarePackLevel_EachThenCase(t *testing.T) {
	h := newHarness()
	h.registered("SKU-1")

	res := declare(t, h, "SKU-1", eachInput())
	if !res.Created || res.Product.Version() != 2 || res.Def.Level() != product.PackLevelEach || res.Def.GTIN().String() != "04006381333931" || res.Def.UnitsPerPack() != 1 {
		t.Fatalf("each: created=%v version=%d def=%+v", res.Created, res.Product.Version(), res.Def)
	}
	res = declare(t, h, "SKU-1", caseInput(gtinCase, 12))
	if !res.Created || res.Product.Version() != 3 || res.Def.UnitsPerPack() != 12 || res.Def.ChildGTIN().String() != "04006381333931" {
		t.Fatalf("case: created=%v version=%d def=%+v", res.Created, res.Product.Version(), res.Def)
	}
	if last := h.repo.saves[len(h.repo.saves)-1]; last.loadedVersion != 2 || last.version != 3 {
		t.Fatalf("save = %+v, want an update guarded by the loaded version 2", last)
	}
	want := []string{"ProductPackLevelDeclared", "ProductPackLevelDeclared"}
	if got := h.outbox.types(); !reflect.DeepEqual(got, want) {
		t.Fatalf("outbox = %v, want %v", got, want)
	}
	if h.uow.runs != 2 || h.uow.failures != 0 {
		t.Fatalf("uow runs=%d failures=%d", h.uow.runs, h.uow.failures)
	}
}

func TestDeclarePackLevel_NormalisesAnyAcceptedGTINLength(t *testing.T) {
	h := newHarness()
	h.registered("SKU-1")
	in := eachInput()
	in.GTIN = "04006381333931" // the same number as gtinEach, in its 14-digit spelling
	res := declare(t, h, "SKU-1", in)
	if res.Def.GTIN().String() != "04006381333931" {
		t.Fatalf("gtin = %s", res.Def.GTIN())
	}
	// 8-digit GTIN-8
	h2 := newHarness()
	h2.registered("SKU-1")
	in.GTIN = "96385074"
	if res := declare(t, h2, "SKU-1", in); res.Def.GTIN().String() != "00000096385074" {
		t.Fatalf("gtin-8 = %s", res.Def.GTIN())
	}
}

func TestDeclarePackLevel_SecondDeclarationIsNotCreatedAndRaisesNothing(t *testing.T) {
	h := newHarness()
	h.registered("SKU-1")
	declare(t, h, "SKU-1", eachInput())
	saves, msgs := len(h.repo.saves), len(h.outbox.msgs)

	res := declare(t, h, "SKU-1", eachInput())
	if res.Created || res.Product.Version() != 2 {
		t.Fatalf("created=%v version=%d", res.Created, res.Product.Version())
	}
	if len(h.repo.saves) != saves || len(h.outbox.msgs) != msgs {
		t.Fatal("an unchanged declaration saved or enqueued something")
	}
}

func TestDeclarePackLevel_NewGTINSupersedesAndRaisesRetiredThenDeclared(t *testing.T) {
	h := newHarness()
	h.registered("SKU-1")
	declare(t, h, "SKU-1", eachInput())
	declare(t, h, "SKU-1", caseInput(gtinCase, 12))

	res := declare(t, h, "SKU-1", caseInput(gtinCase2, 24))
	if res.Created || res.Product.Version() != 5 || res.Def.UnitsPerPack() != 24 {
		t.Fatalf("created=%v version=%d def=%+v", res.Created, res.Product.Version(), res.Def)
	}
	want := []string{"ProductPackLevelDeclared", "ProductPackLevelDeclared", "ProductPackLevelRetired", "ProductPackLevelDeclared"}
	if got := h.outbox.types(); !reflect.DeepEqual(got, want) {
		t.Fatalf("outbox = %v, want %v", got, want)
	}
	stored, _ := h.repo.Get(context.Background(), "SKU-1")
	if old, ok := stored.PackLevels().ByGTIN(mustParse(t, gtinCase)); !ok || old.Active() {
		t.Fatalf("old case = %+v %v, want kept and end-dated", old, ok)
	}
}

func mustParse(t *testing.T, raw string) gs1.GTIN {
	t.Helper()
	g, err := product.ParseGTIN(raw)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestDeclarePackLevel_ChangingTheQuantityOfAnActiveLevelIsRejectedAndSavesNothing(t *testing.T) {
	h := newHarness()
	h.registered("SKU-1")
	declare(t, h, "SKU-1", eachInput())
	declare(t, h, "SKU-1", caseInput(gtinCase, 12))
	saves, msgs := len(h.repo.saves), len(h.outbox.msgs)

	err := declareErr(h, "SKU-1", caseInput(gtinCase, 24))
	if !errors.Is(err, product.ErrPackQuantityImmutable) {
		t.Fatalf("err = %v", err)
	}
	if len(h.repo.saves) != saves || len(h.outbox.msgs) != msgs || h.uow.failures != 1 {
		t.Fatalf("saves %d->%d msgs %d->%d failures %d", saves, len(h.repo.saves), msgs, len(h.outbox.msgs), h.uow.failures)
	}
}

func TestDeclarePackLevel_AGTINHeldByAnotherSKUIsRejectedFleetWide(t *testing.T) {
	h := newHarness()
	h.registered("SKU-1")
	h.registered("SKU-2")
	declare(t, h, "SKU-1", eachInput())
	saves := len(h.repo.saves)

	for _, in := range []usecases.PackLevelInput{
		eachInput(), // the same level, the same number
		{Level: "CASE", GTIN: gtinEach, ChildGTIN: gtinOther, QtyOfChild: 2}, // another level
	} {
		if err := declareErr(h, "SKU-2", in); !errors.Is(err, product.ErrGtinAlreadyAssigned) {
			t.Fatalf("%s: err = %v, want ErrGtinAlreadyAssigned", in.Level, err)
		}
	}
	// the 8-digit-padded / 13-digit spellings are the same GTIN
	in := eachInput()
	in.GTIN = "04006381333931"
	if err := declareErr(h, "SKU-2", in); !errors.Is(err, product.ErrGtinAlreadyAssigned) {
		t.Fatalf("14-digit spelling: err = %v", err)
	}
	if len(h.repo.saves) != saves {
		t.Fatal("a rejected declaration saved something")
	}
	// a retired GTIN still belongs to its SKU
	if _, err := (&usecases.RetirePackLevel{Writer: h.writer}).Handle(context.Background(), usecases.RetirePackLevelCommand{SKU: "SKU-1", Level: "EACH"}); err != nil {
		t.Fatal(err)
	}
	if err := declareErr(h, "SKU-2", eachInput()); !errors.Is(err, product.ErrGtinAlreadyAssigned) {
		t.Fatalf("retired GTIN: err = %v", err)
	}
}

func TestDeclarePackLevel_Rejections(t *testing.T) {
	bigDims := &usecases.DimensionsInput{LengthMm: 0, WidthMm: 1, HeightMm: 1, WeightG: 1}
	tests := []struct {
		name string
		sku  string
		in   usecases.PackLevelInput
		want error
	}{
		{"bad sku", "bad sku", eachInput(), product.ErrInvalidSKU},
		{"unknown product", "NOPE", eachInput(), repository.ErrProductNotFound},
		{"unknown level", "SKU-1", usecases.PackLevelInput{Level: "case", GTIN: gtinEach}, product.ErrInvalidPackLevel},
		{"empty level", "SKU-1", usecases.PackLevelInput{GTIN: gtinEach}, product.ErrInvalidPackLevel},
		{"check digit", "SKU-1", usecases.PackLevelInput{Level: "EACH", GTIN: "4006381333930"}, product.ErrInvalidGTIN},
		{"length", "SKU-1", usecases.PackLevelInput{Level: "EACH", GTIN: "12345"}, product.ErrInvalidGTIN},
		{"no gtin", "SKU-1", usecases.PackLevelInput{Level: "EACH"}, product.ErrInvalidGTIN},
		{"variable measure", "SKU-1", usecases.PackLevelInput{Level: "CASE", GTIN: gtinVar, ChildGTIN: gtinEach, QtyOfChild: 2}, product.ErrVariableMeasureUnsupported},
		{"bad child gtin", "SKU-1", usecases.PackLevelInput{Level: "CASE", GTIN: gtinCase, ChildGTIN: "123", QtyOfChild: 2}, product.ErrInvalidGTIN},
		{"variable measure child", "SKU-1", usecases.PackLevelInput{Level: "CASE", GTIN: gtinCase, ChildGTIN: gtinVar, QtyOfChild: 2}, product.ErrVariableMeasureUnsupported},
		{"case without a child", "SKU-1", usecases.PackLevelInput{Level: "CASE", GTIN: gtinCase, QtyOfChild: 2}, product.ErrPackLevelOrder},
		{"case with one per case", "SKU-1", caseInput(gtinCase, 1), product.ErrInvalidPackQuantity},
		{"bad dimensions", "SKU-1", usecases.PackLevelInput{Level: "EACH", GTIN: gtinEach, Dims: bigDims}, product.ErrInvalidDimension},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness()
			h.registered("SKU-1")
			if err := declareErr(h, tt.sku, tt.in); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if len(h.repo.saves) != 0 || len(h.outbox.msgs) != 0 {
				t.Fatal("a rejected declaration saved something")
			}
		})
	}
}

func TestDeclarePackLevel_InfrastructureErrorsPropagate(t *testing.T) {
	h := newHarness()
	h.registered("SKU-1")
	h.repo.getErr = errBoom
	if err := declareErr(h, "SKU-1", eachInput()); !errors.Is(err, errBoom) {
		t.Fatalf("get: %v", err)
	}
	h = newHarness()
	h.registered("SKU-1")
	h.repo.saveErr = errBoom
	if err := declareErr(h, "SKU-1", eachInput()); !errors.Is(err, errBoom) {
		t.Fatalf("save: %v", err)
	}
	h = newHarness()
	h.registered("SKU-1")
	h.enc.err = errBoom
	if err := declareErr(h, "SKU-1", eachInput()); !errors.Is(err, errBoom) {
		t.Fatalf("encode: %v", err)
	}
	h = newHarness()
	h.registered("SKU-1")
	h.outbox.err = errBoom
	if err := declareErr(h, "SKU-1", eachInput()); !errors.Is(err, errBoom) {
		t.Fatalf("outbox: %v", err)
	}
}

func TestRetirePackLevel(t *testing.T) {
	h := newHarness()
	h.registered("SKU-1")
	declare(t, h, "SKU-1", eachInput())
	declare(t, h, "SKU-1", caseInput(gtinCase, 12))
	uc := &usecases.RetirePackLevel{Writer: h.writer}
	ctx := context.Background()

	p, err := uc.Handle(ctx, usecases.RetirePackLevelCommand{SKU: "SKU-1", Level: "CASE", Reason: "discontinued"})
	if err != nil || p.Version() != 4 {
		t.Fatalf("retire: %v %v", p, err)
	}
	if got := h.outbox.types()[2]; got != "ProductPackLevelRetired" {
		t.Fatalf("outbox = %v", h.outbox.types())
	}
	if ev, ok := h.enc.events[2].(product.ProductPackLevelRetired); !ok || ev.Reason != "discontinued" || !ev.EffectiveTo.Equal(t0) {
		t.Fatalf("event = %+v", h.enc.events[2])
	}

	t.Run("again changes nothing", func(t *testing.T) {
		saves := len(h.repo.saves)
		p, err := uc.Handle(ctx, usecases.RetirePackLevelCommand{SKU: "SKU-1", Level: "CASE", Reason: "again"})
		if err != nil || p.Version() != 4 || len(h.repo.saves) != saves {
			t.Fatalf("again: version=%v err=%v saves %d->%d", p.Version(), err, saves, len(h.repo.saves))
		}
	})
	t.Run("the default reason", func(t *testing.T) {
		if _, err := uc.Handle(ctx, usecases.RetirePackLevelCommand{SKU: "SKU-1", Level: "EACH"}); err != nil {
			t.Fatal(err)
		}
		if ev := h.enc.events[len(h.enc.events)-1].(product.ProductPackLevelRetired); ev.Reason != usecases.DefaultRetireReason || usecases.DefaultRetireReason != "retired" {
			t.Fatalf("reason = %q", ev.Reason)
		}
	})
	t.Run("rejections", func(t *testing.T) {
		for _, tt := range []struct {
			name string
			cmd  usecases.RetirePackLevelCommand
			want error
		}{
			{"bad sku", usecases.RetirePackLevelCommand{SKU: "x y", Level: "CASE"}, product.ErrInvalidSKU},
			{"bad level", usecases.RetirePackLevelCommand{SKU: "SKU-1", Level: "BOX"}, product.ErrInvalidPackLevel},
			{"unknown product", usecases.RetirePackLevelCommand{SKU: "NOPE", Level: "CASE"}, repository.ErrProductNotFound},
			{"never declared", usecases.RetirePackLevelCommand{SKU: "SKU-1", Level: "PALLET"}, product.ErrPackLevelNotFound},
			{"long reason", usecases.RetirePackLevelCommand{SKU: "SKU-1", Level: "PALLET", Reason: string(make([]byte, 201))}, product.ErrInvalidReason},
		} {
			if _, err := uc.Handle(ctx, tt.cmd); !errors.Is(err, tt.want) {
				t.Fatalf("%s: err = %v, want %v", tt.name, err, tt.want)
			}
		}
	})
}

func TestRecordPackLevelMeasurement(t *testing.T) {
	h := newHarness()
	h.registered("SKU-1")
	declare(t, h, "SKU-1", eachInput())
	uc := &usecases.RecordPackLevelMeasurement{Writer: h.writer}
	ctx := context.Background()
	cmd := usecases.RecordPackLevelMeasurementCommand{
		SKU: "SKU-1", Level: "EACH", DimensionsInput: usecases.DimensionsInput{LengthMm: 100, WidthMm: 50, HeightMm: 40, WeightG: 500},
		MeasuredAt: t0.Add(-time.Minute), DeviceID: "cubiscan-1",
	}
	p, err := uc.Handle(ctx, cmd)
	if err != nil || p.Version() != 3 {
		t.Fatalf("measure: %v %v", p, err)
	}
	ev, ok := h.enc.events[len(h.enc.events)-1].(product.ProductPackLevelMeasured)
	if !ok || ev.Level != product.PackLevelEach || ev.Measurement.DeviceID() != "cubiscan-1" {
		t.Fatalf("event = %+v", h.enc.events[len(h.enc.events)-1])
	}
	saves := len(h.repo.saves)
	if p, err := uc.Handle(ctx, cmd); err != nil || p.Version() != 3 || len(h.repo.saves) != saves {
		t.Fatalf("again: %v %v", p, err)
	}

	for _, tt := range []struct {
		name   string
		mutate func(*usecases.RecordPackLevelMeasurementCommand)
		want   error
	}{
		{"bad sku", func(c *usecases.RecordPackLevelMeasurementCommand) { c.SKU = "" }, product.ErrInvalidSKU},
		{"bad level", func(c *usecases.RecordPackLevelMeasurementCommand) { c.Level = "BOX" }, product.ErrInvalidPackLevel},
		{"bad dimensions", func(c *usecases.RecordPackLevelMeasurementCommand) { c.LengthMm = 0 }, product.ErrInvalidDimension},
		{"no time", func(c *usecases.RecordPackLevelMeasurementCommand) { c.MeasuredAt = time.Time{} }, product.ErrMissingMeasuredAt},
		{"future", func(c *usecases.RecordPackLevelMeasurementCommand) { c.MeasuredAt = t0.Add(time.Minute) }, product.ErrMeasuredAtInFuture},
		{"stale", func(c *usecases.RecordPackLevelMeasurementCommand) { c.MeasuredAt = t0.Add(-time.Hour) }, product.ErrStaleMeasurement},
		{"no active level", func(c *usecases.RecordPackLevelMeasurementCommand) { c.Level = "CASE" }, product.ErrPackLevelNotFound},
		{"unknown product", func(c *usecases.RecordPackLevelMeasurementCommand) { c.SKU = "NOPE" }, repository.ErrProductNotFound},
	} {
		c := cmd
		tt.mutate(&c)
		if _, err := uc.Handle(ctx, c); !errors.Is(err, tt.want) {
			t.Fatalf("%s: err = %v, want %v", tt.name, err, tt.want)
		}
	}
}

func TestResolveGTIN(t *testing.T) {
	h := newHarness()
	h.registered("SKU-1")
	declare(t, h, "SKU-1", eachInput())
	declare(t, h, "SKU-1", caseInput(gtinCase, 12))
	declare(t, h, "SKU-1", caseInput(gtinCase2, 24)) // end-dates gtinCase
	uc := &usecases.ResolveGTIN{Products: h.repo}
	ctx := context.Background()

	res, err := uc.Handle(ctx, gtinCase2)
	if err != nil || res.SKU != "SKU-1" || res.Def.Level() != product.PackLevelCase || res.Def.UnitsPerPack() != 24 || !res.Def.Active() {
		t.Fatalf("active: %+v %v", res, err)
	}
	res, err = uc.Handle(ctx, gtinCase)
	if err != nil || res.Def.UnitsPerPack() != 12 || res.Def.Active() {
		t.Fatalf("retired must still resolve, inactive: %+v %v", res, err)
	}
	res, err = uc.Handle(ctx, gtinEach) // a 13-digit spelling resolves to the 14-digit identity
	if err != nil || res.Def.Level() != product.PackLevelEach {
		t.Fatalf("each: %+v %v", res, err)
	}
	for _, tt := range []struct {
		raw  string
		want error
	}{
		{gtinOther, repository.ErrGtinNotFound},
		{"4006381333930", product.ErrInvalidGTIN},
		{"", product.ErrInvalidGTIN},
		{gtinVar, product.ErrVariableMeasureUnsupported},
	} {
		if _, err := uc.Handle(ctx, tt.raw); !errors.Is(err, tt.want) {
			t.Fatalf("%q: err = %v, want %v", tt.raw, err, tt.want)
		}
	}
	h.repo.getErr = errBoom
	if _, err := uc.Handle(ctx, gtinEach); !errors.Is(err, errBoom) {
		t.Fatalf("infra error: %v", err)
	}
}

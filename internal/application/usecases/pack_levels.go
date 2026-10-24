package usecases

import (
	"context"
	"errors"
	"time"

	"github.com/claudioed/product-master/internal/application/ports"
	"github.com/claudioed/product-master/internal/domain/product"
)

// DefaultRetireReason is the reason recorded when a RetirePackLevel command
// names none.
const DefaultRetireReason = "retired"

// PackLevelInput is a raw pack level declaration (ADR 0011). GTIN and
// ChildGTIN are raw strings of 8, 12, 13 or 14 digits; UnitsPerPack 0 means
// "derive it from the child level"; Dims nil means no declared dimensions.
type PackLevelInput struct {
	Level          string
	GTIN           string
	ChildGTIN      string
	QtyOfChild     int
	UnitsPerPack   int
	Dims           *DimensionsInput
	IsOrderable    bool
	IsDespatchUnit bool
}

func (in PackLevelInput) spec() (product.PackLevelSpec, error) {
	level, err := product.ParsePackLevel(in.Level)
	if err != nil {
		return product.PackLevelSpec{}, err
	}
	gtin, err := product.ParseGTIN(in.GTIN)
	if err != nil {
		return product.PackLevelSpec{}, err
	}
	spec := product.PackLevelSpec{
		Level: level, GTIN: gtin, QtyOfChild: in.QtyOfChild, UnitsPerPack: in.UnitsPerPack,
		IsOrderable: in.IsOrderable, IsDespatchUnit: in.IsDespatchUnit,
	}
	if in.ChildGTIN != "" {
		if spec.ChildGTIN, err = product.ParseGTIN(in.ChildGTIN); err != nil {
			return product.PackLevelSpec{}, err
		}
	}
	if in.Dims != nil {
		d, err := in.Dims.build()
		if err != nil {
			return product.PackLevelSpec{}, err
		}
		spec.Dims = &d
	}
	return spec, nil
}

// DeclarePackLevelCommand is the input of DeclarePackLevel.
type DeclarePackLevelCommand struct {
	SKU string
	PackLevelInput
}

// DeclarePackLevelResult is the product after the call, the declared level
// and whether the level is new on the product (201) rather than an existing
// one declared again, changed or superseded (200).
type DeclarePackLevelResult struct {
	Product *product.Product
	Def     product.PackLevelDef
	Created bool
}

// DeclarePackLevel declares a pack level of a registered product. Besides the
// aggregate's invariants it enforces the one that needs other aggregates: a
// GTIN identifies exactly one (sku, level), fleet-wide
// (product.ErrGtinAlreadyAssigned). The repository's unique index on the
// GTIN backs it against a concurrent declaration.
type DeclarePackLevel struct {
	Writer
}

// Handle runs the use case.
func (uc *DeclarePackLevel) Handle(ctx context.Context, cmd DeclarePackLevelCommand) (DeclarePackLevelResult, error) {
	sku, err := parseSKU(cmd.SKU)
	if err != nil {
		return DeclarePackLevelResult{}, err
	}
	spec, err := cmd.spec()
	if err != nil {
		return DeclarePackLevelResult{}, err
	}
	var res DeclarePackLevelResult
	err = uc.UoW.Do(ctx, func(ctx context.Context) error {
		p, loaded, err := uc.load(ctx, sku)
		if err != nil {
			return err
		}
		if err := uc.requireUnassigned(ctx, sku, spec); err != nil {
			return err
		}
		res.Created = !hasLevel(p, spec.Level)
		events, err := p.DeclarePackLevel(spec, uc.now())
		if err != nil {
			return err
		}
		if err := uc.persist(ctx, p, loaded, events); err != nil {
			return err
		}
		res.Product = p
		res.Def, _ = p.PackLevels().ByGTIN(spec.GTIN)
		return nil
	})
	if err != nil {
		return DeclarePackLevelResult{}, err
	}
	return res, nil
}

// requireUnassigned rejects a GTIN another SKU already holds.
func (uc *DeclarePackLevel) requireUnassigned(ctx context.Context, sku product.SKU, spec product.PackLevelSpec) error {
	owner, err := uc.Products.GetByGTIN(ctx, spec.GTIN)
	switch {
	case isGtinNotFound(err):
		return nil
	case err != nil:
		return err
	case owner.SKU() != sku:
		return product.ErrGtinAlreadyAssigned
	}
	return nil
}

func hasLevel(p *product.Product, level product.PackLevel) bool {
	for _, d := range p.PackLevels().Levels() {
		if d.Level() == level {
			return true
		}
	}
	return false
}

// RetirePackLevelCommand is the input of RetirePackLevel. An empty Reason
// is DefaultRetireReason.
type RetirePackLevelCommand struct {
	SKU    string
	Level  string
	Reason string
}

// RetirePackLevel end-dates the active definition of a level. The
// definition stays resolvable by its GTIN. Retiring a level that is already
// retired changes nothing.
type RetirePackLevel struct {
	Writer
}

// Handle runs the use case and returns the product after the call.
func (uc *RetirePackLevel) Handle(ctx context.Context, cmd RetirePackLevelCommand) (*product.Product, error) {
	sku, err := parseSKU(cmd.SKU)
	if err != nil {
		return nil, err
	}
	level, err := product.ParsePackLevel(cmd.Level)
	if err != nil {
		return nil, err
	}
	reason := cmd.Reason
	if reason == "" {
		reason = DefaultRetireReason
	}
	return uc.change(ctx, sku, func(p *product.Product, now time.Time) ([]product.Event, error) {
		return p.RetirePackLevel(level, reason, now)
	})
}

// RecordPackLevelMeasurementCommand is the input of RecordPackLevelMeasurement.
type RecordPackLevelMeasurementCommand struct {
	SKU   string
	Level string
	DimensionsInput
	MeasuredAt time.Time
	DeviceID   string
}

// RecordPackLevelMeasurement records the latest measurement of a product's
// active pack level (ADR 0002 per level, ADR 0011). As for the unit
// measurement the clock's now rejects a reading dated in the future.
type RecordPackLevelMeasurement struct {
	Writer
}

// Handle runs the use case and returns the product after the call.
func (uc *RecordPackLevelMeasurement) Handle(ctx context.Context, cmd RecordPackLevelMeasurementCommand) (*product.Product, error) {
	sku, err := parseSKU(cmd.SKU)
	if err != nil {
		return nil, err
	}
	level, err := product.ParsePackLevel(cmd.Level)
	if err != nil {
		return nil, err
	}
	dims, err := cmd.build()
	if err != nil {
		return nil, err
	}
	m, err := product.NewMeasurement(dims, cmd.MeasuredAt, cmd.DeviceID, uc.now())
	if err != nil {
		return nil, err
	}
	return uc.change(ctx, sku, func(p *product.Product, now time.Time) ([]product.Event, error) {
		return p.RecordPackLevelMeasurement(level, m, now)
	})
}

// GTINResolution is what a GTIN identifies: the SKU and the pack level
// definition (active or retired) that carries it.
type GTINResolution struct {
	SKU product.SKU
	Def product.PackLevelDef
}

// ResolveGTIN answers "which SKU and level does this GTIN identify?" for
// people, tooling and backfills. No service calls it on a hot path (ADR 0011:
// consumers resolve scans from their own event-fed projection).
type ResolveGTIN struct {
	Products ports.ProductRepository
}

// Handle normalises raw to 14 digits (product.ErrInvalidGTIN /
// product.ErrVariableMeasureUnsupported), then looks it up
// (repository.ErrGtinNotFound).
func (uc *ResolveGTIN) Handle(ctx context.Context, raw string) (GTINResolution, error) {
	gtin, err := product.ParseGTIN(raw)
	if err != nil {
		return GTINResolution{}, err
	}
	p, err := uc.Products.GetByGTIN(ctx, gtin)
	if err != nil {
		return GTINResolution{}, err
	}
	def, ok := p.PackLevels().ByGTIN(gtin)
	if !ok {
		// the repository answered with a product that does not hold the GTIN
		return GTINResolution{}, errors.New("repository returned a product without the requested GTIN")
	}
	return GTINResolution{SKU: p.SKU(), Def: def}, nil
}

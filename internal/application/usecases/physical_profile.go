package usecases

import (
	"context"
	"time"

	"github.com/claudioed/product-master/internal/domain/product"
)

// DimensionsInput is a raw unit size and weight (mm and g).
type DimensionsInput struct {
	LengthMm int64
	WidthMm  int64
	HeightMm int64
	WeightG  int64
}

func (d DimensionsInput) build() (product.UnitDimensions, error) {
	return product.NewUnitDimensions(d.LengthMm, d.WidthMm, d.HeightMm, d.WeightG)
}

// DeclareDimensionsCommand is the input of DeclareDimensions.
type DeclareDimensionsCommand struct {
	SKU string
	DimensionsInput
}

// DeclareDimensions sets or replaces a registered product's declared unit
// dimensions (ADR 0002). Identical values are no change.
type DeclareDimensions struct {
	Writer
}

// Handle runs the use case and returns the product after the call.
func (uc *DeclareDimensions) Handle(ctx context.Context, cmd DeclareDimensionsCommand) (*product.Product, error) {
	sku, err := parseSKU(cmd.SKU)
	if err != nil {
		return nil, err
	}
	dims, err := cmd.build()
	if err != nil {
		return nil, err
	}
	return uc.change(ctx, sku, func(p *product.Product, now time.Time) ([]product.Event, error) {
		return p.DeclareDimensions(dims, now), nil
	})
}

// RecordMeasurementCommand is the input of RecordMeasurement.
type RecordMeasurementCommand struct {
	SKU string
	DimensionsInput
	MeasuredAt time.Time
	DeviceID   string
}

// RecordMeasurement records the latest measurement of a registered product
// (ADR 0002). The clock's now rejects a measurement dated in the future; an
// older measurement than the current one is product.ErrStaleMeasurement.
type RecordMeasurement struct {
	Writer
}

// Handle runs the use case and returns the product after the call.
func (uc *RecordMeasurement) Handle(ctx context.Context, cmd RecordMeasurementCommand) (*product.Product, error) {
	sku, err := parseSKU(cmd.SKU)
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
		return p.RecordMeasurement(m, now)
	})
}

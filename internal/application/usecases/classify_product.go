package usecases

import (
	"context"
	"time"

	"github.com/claudioed/product-master/internal/domain/product"
)

// ClassifyProductCommand is the input of ClassifyProduct. DOTHazardClass 0
// means "not recorded".
type ClassifyProductCommand struct {
	SKU              string
	HandlingTags     []string
	TemperatureClass string
	DOTHazardClass   int
}

// ClassifyProductResult is the product after the call and whether it had
// no classification before (201) rather than one being replaced (200).
type ClassifyProductResult struct {
	Product *product.Product
	Created bool
}

// ClassifyProduct sets or replaces a registered product's classification
// (source native).
type ClassifyProduct struct {
	Writer
}

// Handle runs the use case. The classification is validated before the
// product is loaded, so an invalid body is a 400 even for an unknown SKU.
func (uc *ClassifyProduct) Handle(ctx context.Context, cmd ClassifyProductCommand) (ClassifyProductResult, error) {
	sku, err := parseSKU(cmd.SKU)
	if err != nil {
		return ClassifyProductResult{}, err
	}
	c, err := newClassification(cmd.HandlingTags, cmd.TemperatureClass, cmd.DOTHazardClass)
	if err != nil {
		return ClassifyProductResult{}, err
	}
	created := false
	p, err := uc.change(ctx, sku, func(p *product.Product, now time.Time) ([]product.Event, error) {
		created = !p.IsClassified()
		return p.Classify(c, now)
	})
	if err != nil {
		return ClassifyProductResult{}, err
	}
	return ClassifyProductResult{Product: p, Created: created}, nil
}

// newClassification builds a validated classification from raw values;
// every rule (closed sets included) is the domain's.
func newClassification(tags []string, temperatureClass string, dotHazardClass int) (product.Classification, error) {
	handling := make([]product.HandlingTag, len(tags))
	for i, t := range tags {
		handling[i] = product.HandlingTag(t)
	}
	return product.NewClassification(handling, product.TemperatureClass(temperatureClass), product.DOTHazardClass(dotHazardClass))
}

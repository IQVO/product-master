package usecases

import (
	"context"

	"github.com/claudioed/product-master/internal/application/ports"
	"github.com/claudioed/product-master/internal/domain/product"
)

// RegisterProductCommand is the input of RegisterProduct.
type RegisterProductCommand struct {
	SKU         string
	Description string
}

// RegisterProductResult is the product after the call and whether it was
// created (201) rather than already registered (200).
type RegisterProductResult struct {
	Product *product.Product
	Created bool
}

// RegisterProduct registers a SKU as a product or, when it already exists,
// replaces its description. The same description is no change.
type RegisterProduct struct {
	Writer

	// Metrics is the Tier 2 business-metric port (ADR-0013). Optional: a
	// nil Metrics records nothing, so composition roots that don't wire
	// it keep building RegisterProduct exactly as before.
	Metrics ports.ProductMetrics
}

// Handle runs the use case.
func (uc *RegisterProduct) Handle(ctx context.Context, cmd RegisterProductCommand) (RegisterProductResult, error) {
	sku, err := parseSKU(cmd.SKU)
	if err != nil {
		return RegisterProductResult{}, err
	}
	var res RegisterProductResult
	outcome := ports.ProductRegistrationOutcomeUnchanged
	err = uc.UoW.Do(ctx, func(ctx context.Context) error {
		p, loaded, err := uc.load(ctx, sku)
		var events []product.Event
		switch {
		case isNotFound(err):
			p, events, err = product.Register(sku, cmd.Description, uc.now())
			if err != nil {
				return err
			}
			res.Created = true
			outcome = ports.ProductRegistrationOutcomeCreated
		case err != nil:
			return err
		default:
			if events, err = p.ChangeDescription(cmd.Description, uc.now()); err != nil {
				return err
			}
			if len(events) > 0 {
				outcome = ports.ProductRegistrationOutcomeDescriptionChanged
			}
		}
		if err := uc.persist(ctx, p, loaded, events); err != nil {
			return err
		}
		res.Product = p
		return nil
	})
	if err != nil {
		return RegisterProductResult{}, err
	}
	// Recorded here, after the unit of work commits, so the metric tracks
	// an actually-persisted outcome rather than a request that may still
	// roll back (ADR-0013).
	if uc.Metrics != nil {
		uc.Metrics.ProductRegistration(ctx, outcome)
	}
	return res, nil
}

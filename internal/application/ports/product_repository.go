// Package ports declares the application's OUT ports: interfaces only
// (enforced by internal/architecture). Their non-interface vocabulary
// (errors, filters, messages) lives in internal/application/repository and
// internal/application/outbox.
package ports

import (
	"context"

	"github.com/claudioed/product-master/internal/application/repository"
	"github.com/claudioed/product-master/internal/domain/product"
	"github.com/claudioed/product-master/internal/domain/shared/gs1"
)

// ProductRepository persists Product aggregates by SKU. Inside a
// UnitOfWork it joins the transaction carried in ctx.
type ProductRepository interface {
	// Get returns the product, or repository.ErrProductNotFound.
	Get(ctx context.Context, sku product.SKU) (*product.Product, error)
	// GetByGTIN returns the product that holds gtin at any of its pack
	// levels, active or retired, or repository.ErrGtinNotFound. A GTIN
	// identifies exactly one (sku, level), fleet-wide (ADR 0011).
	GetByGTIN(ctx context.Context, gtin gs1.GTIN) (*product.Product, error)
	// Save persists p guarded by the version the caller loaded:
	// loadedVersion 0 inserts (an existing SKU is
	// repository.ErrConcurrentModification); any other value updates only
	// when the stored version still equals loadedVersion (else
	// repository.ErrConcurrentModification). The product's pack levels are
	// saved with it; a GTIN already held by another SKU is
	// product.ErrGtinAlreadyAssigned.
	Save(ctx context.Context, p *product.Product, loadedVersion int64) error
	// List returns up to limit products with SKU > afterSKU (byte order),
	// ascending by SKU, narrowed by filter.
	List(ctx context.Context, filter repository.ListFilter, afterSKU product.SKU, limit int) ([]*product.Product, error)
}

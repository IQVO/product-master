// Package ports declares the application's OUT ports: interfaces only
// (enforced by internal/architecture). Their non-interface vocabulary
// (errors, filters, messages) lives in internal/application/repository and
// internal/application/outbox.
package ports

import (
	"context"

	"github.com/claudioed/product-master/internal/application/repository"
	"github.com/claudioed/product-master/internal/domain/product"
)

// ProductRepository persists Product aggregates by SKU. Inside a
// UnitOfWork it joins the transaction carried in ctx.
type ProductRepository interface {
	// Get returns the product, or repository.ErrProductNotFound.
	Get(ctx context.Context, sku product.SKU) (*product.Product, error)
	// Save persists p guarded by the version the caller loaded:
	// loadedVersion 0 inserts (an existing SKU is
	// repository.ErrConcurrentModification); any other value updates only
	// when the stored version still equals loadedVersion (else
	// repository.ErrConcurrentModification).
	Save(ctx context.Context, p *product.Product, loadedVersion int64) error
	// List returns up to limit products with SKU > afterSKU (byte order),
	// ascending by SKU, narrowed by filter.
	List(ctx context.Context, filter repository.ListFilter, afterSKU product.SKU, limit int) ([]*product.Product, error)
}

// Package memory holds in-memory implementations of the outbound ports. They
// back the service when DATABASE_URL is unset (local development) and the
// BDD suite; they mirror the Postgres adapter's semantics (version guard,
// ascending SKU paging, outbox drain order) so scenarios exercise the same
// behaviour.
package memory

import (
	"context"
	"sort"
	"sync"

	"github.com/claudioed/product-master/internal/application/ports"
	"github.com/claudioed/product-master/internal/application/repository"
	"github.com/claudioed/product-master/internal/domain/product"
)

// ProductRepo is an in-memory, mutex-guarded ports.ProductRepository.
type ProductRepo struct {
	mu       sync.Mutex
	products map[product.SKU]*product.Product
}

// NewProductRepo constructs an empty ProductRepo.
func NewProductRepo() *ProductRepo {
	return &ProductRepo{products: map[product.SKU]*product.Product{}}
}

var _ ports.ProductRepository = (*ProductRepo)(nil)

// copyOf rebuilds an independent Product with the same state, so a caller
// mutating what Get returned never changes what is stored.
func copyOf(p *product.Product) *product.Product {
	var classification *product.Classification
	var source product.ClassificationSource
	if c, s, err := p.Classification(); err == nil {
		cc := product.RehydrateClassification(c.Tags(), c.TemperatureClass(), c.DOTHazardClass())
		classification, source = &cc, s
	}
	return product.Rehydrate(p.SKU(), p.Description(), classification, source, p.PhysicalProfile(), p.Version())
}

// Get implements ports.ProductRepository.
func (r *ProductRepo) Get(_ context.Context, sku product.SKU) (*product.Product, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.products[sku]
	if !ok {
		return nil, repository.ErrProductNotFound
	}
	return copyOf(p), nil
}

// Save implements ports.ProductRepository with the same version guard as the
// Postgres adapter.
func (r *ProductRepo) Save(_ context.Context, p *product.Product, loadedVersion int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, exists := r.products[p.SKU()]
	if loadedVersion == 0 && exists {
		return repository.ErrConcurrentModification
	}
	if loadedVersion != 0 && (!exists || current.Version() != loadedVersion) {
		return repository.ErrConcurrentModification
	}
	r.products[p.SKU()] = copyOf(p)
	return nil
}

// List implements ports.ProductRepository (byte-order SKU, like the
// Postgres adapter's COLLATE "C" column).
func (r *ProductRepo) List(_ context.Context, filter repository.ListFilter, afterSKU product.SKU, limit int) ([]*product.Product, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*product.Product, 0, len(r.products))
	for sku, p := range r.products {
		if sku > afterSKU && matches(p, filter) {
			out = append(out, copyOf(p))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SKU() < out[j].SKU() })
	if limit >= 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func matches(p *product.Product, f repository.ListFilter) bool {
	if f.Classified != nil && p.IsClassified() != *f.Classified {
		return false
	}
	if f.HandlingTag == "" {
		return true
	}
	c, _, err := p.Classification()
	return err == nil && c.HasTag(f.HandlingTag)
}

// Snapshot implements Snapshotter.
func (r *ProductRepo) Snapshot() func() {
	r.mu.Lock()
	defer r.mu.Unlock()
	saved := make(map[product.SKU]*product.Product, len(r.products))
	for k, v := range r.products {
		saved[k] = v
	}
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.products = saved
	}
}

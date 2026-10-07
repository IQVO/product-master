// Package repository holds the non-interface vocabulary of the
// ports.ProductRepository port: its typed errors and its list filter. It is
// separate from package ports because ports may contain interfaces only
// (internal/architecture's "ports package only contains interfaces" rule).
package repository

import (
	"errors"

	"github.com/claudioed/product-master/internal/domain/product"
)

var (
	// ErrProductNotFound is returned by Get when the SKU is not registered.
	ErrProductNotFound = errors.New("product not found")
	// ErrConcurrentModification is returned by Save when the stored version
	// is not the version the caller loaded (or, for an insert, when the SKU
	// already exists): another writer won the race.
	ErrConcurrentModification = errors.New("concurrent modification")
)

// ListFilter narrows ProductRepository.List. Zero values mean "no filter".
type ListFilter struct {
	// HandlingTag keeps only products whose classification carries it.
	HandlingTag product.HandlingTag
	// Classified keeps only classified (true) or unclassified (false)
	// products when non-nil.
	Classified *bool
}

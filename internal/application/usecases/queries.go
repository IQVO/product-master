package usecases

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/claudioed/product-master/internal/application/ports"
	"github.com/claudioed/product-master/internal/application/repository"
	"github.com/claudioed/product-master/internal/domain/product"
)

// List page-size bounds (apis/openapi.yaml).
const (
	DefaultListLimit = 100
	MaxListLimit     = 500
)

// ErrInvalidListQuery marks a malformed list query (limit, cursor or
// filter); the HTTP adapter maps it to 400 malformed-request.
var ErrInvalidListQuery = errors.New("invalid list query")

// GetProduct returns one product.
type GetProduct struct {
	Products ports.ProductRepository
}

// Handle validates the SKU and loads the product
// (repository.ErrProductNotFound when unknown).
func (uc *GetProduct) Handle(ctx context.Context, rawSKU string) (*product.Product, error) {
	sku, err := parseSKU(rawSKU)
	if err != nil {
		return nil, err
	}
	return uc.Products.Get(ctx, sku)
}

// ListProductsQuery is the input of ListProducts. Limit 0 means the
// default; Cursor is the opaque nextCursor of the previous page;
// HandlingTag "" and Classified nil mean "no filter".
type ListProductsQuery struct {
	Limit       int
	Cursor      string
	HandlingTag string
	Classified  *bool
}

// ProductPage is one page of products in ascending SKU order. NextCursor is
// empty on the last page.
type ProductPage struct {
	Items      []*product.Product
	NextCursor string
}

// ListProducts lists products a page at a time (cursor = base64url of the
// last SKU of the previous page).
type ListProducts struct {
	Products ports.ProductRepository
}

// Handle runs the use case.
func (uc *ListProducts) Handle(ctx context.Context, q ListProductsQuery) (ProductPage, error) {
	limit, err := listLimit(q.Limit)
	if err != nil {
		return ProductPage{}, err
	}
	after, err := DecodeCursor(q.Cursor)
	if err != nil {
		return ProductPage{}, err
	}
	filter := repository.ListFilter{Classified: q.Classified}
	if q.HandlingTag != "" {
		tag, err := product.ParseHandlingTag(q.HandlingTag)
		if err != nil {
			return ProductPage{}, fmt.Errorf("%w: handlingTag: %w", ErrInvalidListQuery, err)
		}
		filter.HandlingTag = tag
	}
	items, err := uc.Products.List(ctx, filter, after, limit+1)
	if err != nil {
		return ProductPage{}, err
	}
	page := ProductPage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		page.NextCursor = EncodeCursor(page.Items[limit-1].SKU())
	}
	return page, nil
}

func listLimit(limit int) (int, error) {
	if limit == 0 {
		return DefaultListLimit, nil
	}
	if limit < 1 || limit > MaxListLimit {
		return 0, fmt.Errorf("%w: limit must be 1..%d", ErrInvalidListQuery, MaxListLimit)
	}
	return limit, nil
}

// EncodeCursor makes the opaque cursor for "after this SKU".
func EncodeCursor(sku product.SKU) string {
	return base64.RawURLEncoding.EncodeToString([]byte(sku))
}

// DecodeCursor reverses EncodeCursor; "" is the first page.
func DecodeCursor(cursor string) (product.SKU, error) {
	if cursor == "" {
		return "", nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", fmt.Errorf("%w: cursor is not a valid cursor", ErrInvalidListQuery)
	}
	sku, err := product.NewSKU(string(raw))
	if err != nil {
		return "", fmt.Errorf("%w: cursor is not a valid cursor", ErrInvalidListQuery)
	}
	return sku, nil
}

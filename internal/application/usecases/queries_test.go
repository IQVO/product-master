package usecases_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/claudioed/product-master/internal/application/repository"
	"github.com/claudioed/product-master/internal/application/usecases"
	"github.com/claudioed/product-master/internal/domain/product"
)

const time1h = time.Hour

func TestGetProduct(t *testing.T) {
	h := newHarness()
	h.registered("SKU-1")
	uc := &usecases.GetProduct{Products: h.repo}
	ctx := context.Background()
	if p, err := uc.Handle(ctx, "SKU-1"); err != nil || p.SKU() != "SKU-1" {
		t.Fatalf("got %v, %v", p, err)
	}
	if _, err := uc.Handle(ctx, "SKU-2"); !errors.Is(err, repository.ErrProductNotFound) {
		t.Fatalf("err = %v", err)
	}
	if _, err := uc.Handle(ctx, "bad sku"); !errors.Is(err, product.ErrInvalidSKU) {
		t.Fatalf("err = %v", err)
	}
}

func TestListProducts_PagesWithACursor(t *testing.T) {
	h := newHarness()
	for _, s := range []string{"A", "B", "C"} {
		h.registered(s)
	}
	uc := &usecases.ListProducts{Products: h.repo}
	ctx := context.Background()

	page, err := uc.Handle(ctx, usecases.ListProductsQuery{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Items[0].SKU() != "A" || page.Items[1].SKU() != "B" || page.NextCursor != usecases.EncodeCursor("B") {
		t.Fatalf("page 1 = %v next=%q", skus(page.Items), page.NextCursor)
	}
	if h.repo.listLimit != 3 || h.repo.listAfter != "" {
		t.Fatalf("repo asked for limit=%d after=%q, want limit+1 from the start", h.repo.listLimit, h.repo.listAfter)
	}
	page, err = uc.Handle(ctx, usecases.ListProductsQuery{Limit: 2, Cursor: page.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].SKU() != "C" || page.NextCursor != "" {
		t.Fatalf("page 2 = %v next=%q", skus(page.Items), page.NextCursor)
	}
	if h.repo.listAfter != "B" {
		t.Fatalf("after = %q, want B", h.repo.listAfter)
	}
}

func TestListProducts_ExactlyLimitLeftHasNoNextCursor(t *testing.T) {
	h := newHarness()
	for _, s := range []string{"A", "B", "C"} {
		h.registered(s)
	}
	page, err := (&usecases.ListProducts{Products: h.repo}).Handle(context.Background(), usecases.ListProductsQuery{Limit: 3})
	if err != nil || page.NextCursor != "" || len(page.Items) != 3 {
		t.Fatalf("exact page: %v next=%q err=%v", skus(page.Items), page.NextCursor, err)
	}
}

func TestListProducts_LimitsAndFilters(t *testing.T) {
	h := newHarness()
	uc := &usecases.ListProducts{Products: h.repo}
	ctx := context.Background()
	if _, err := uc.Handle(ctx, usecases.ListProductsQuery{}); err != nil || h.repo.listLimit != usecases.DefaultListLimit+1 {
		t.Fatalf("default: err=%v limit=%d", err, h.repo.listLimit)
	}
	if _, err := uc.Handle(ctx, usecases.ListProductsQuery{Limit: usecases.MaxListLimit}); err != nil || h.repo.listLimit != usecases.MaxListLimit+1 {
		t.Fatalf("max: err=%v limit=%d", err, h.repo.listLimit)
	}
	if _, err := uc.Handle(ctx, usecases.ListProductsQuery{Limit: 1}); err != nil || h.repo.listLimit != 2 {
		t.Fatalf("min: err=%v limit=%d", err, h.repo.listLimit)
	}
	yes := true
	if _, err := uc.Handle(ctx, usecases.ListProductsQuery{HandlingTag: "Hazmat", Classified: &yes}); err != nil {
		t.Fatal(err)
	}
	if h.repo.listFilter.HandlingTag != product.Hazmat || h.repo.listFilter.Classified != &yes {
		t.Fatalf("filter = %+v", h.repo.listFilter)
	}
	for name, q := range map[string]usecases.ListProductsQuery{
		"negative limit":  {Limit: -1},
		"limit too big":   {Limit: usecases.MaxListLimit + 1},
		"cursor not b64":  {Cursor: "!!!"},
		"cursor bad sku":  {Cursor: usecases.EncodeCursor("a b")},
		"unknown tag":     {HandlingTag: "Sharp"},
		"empty b64 value": {Cursor: "=="},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := uc.Handle(ctx, q); !errors.Is(err, usecases.ErrInvalidListQuery) {
				t.Fatalf("err = %v, want ErrInvalidListQuery", err)
			}
		})
	}
	h.repo.listErr = errBoom
	if _, err := uc.Handle(ctx, usecases.ListProductsQuery{}); !errors.Is(err, errBoom) {
		t.Fatalf("err = %v", err)
	}
}

func TestCursorRoundTrip(t *testing.T) {
	if got := usecases.EncodeCursor("SKU-1"); got != "U0tVLTE" {
		t.Fatalf("cursor = %q, want the spec example U0tVLTE", got)
	}
	if sku, err := usecases.DecodeCursor("U0tVLTE"); err != nil || sku != "SKU-1" {
		t.Fatalf("decode = %q, %v", sku, err)
	}
	if sku, err := usecases.DecodeCursor(""); err != nil || sku != "" {
		t.Fatalf("empty = %q, %v", sku, err)
	}
}

func skus(ps []*product.Product) []product.SKU {
	out := make([]product.SKU, len(ps))
	for i, p := range ps {
		out[i] = p.SKU()
	}
	return out
}

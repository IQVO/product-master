package usecases_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/product-master/internal/application/repository"
	"github.com/claudioed/product-master/internal/application/usecases"
	"github.com/claudioed/product-master/internal/domain/product"
)

const time1h = time.Hour

func importer(h *harness) *usecases.ImportLegacyClassification {
	return &usecases.ImportLegacyClassification{Writer: h.writer, ProcessedEvents: h.process}
}

func TestImportLegacy_UnknownSKUIsRegisteredAndClassified(t *testing.T) {
	h := newHarness()
	out, err := importer(h).Handle(context.Background(), "ev-1", "SKU-9", []string{"Hazmat"}, "", 9)
	if err != nil || out != usecases.ImportApplied {
		t.Fatalf("got %q, %v", out, err)
	}
	if want := []string{"ProductRegistered", "ProductClassified"}; !reflect.DeepEqual(h.outbox.types(), want) {
		t.Fatalf("outbox = %v, want %v", h.outbox.types(), want)
	}
	if len(h.repo.saves) != 1 || h.repo.saves[0].loadedVersion != 0 || h.repo.saves[0].version != 2 {
		t.Fatalf("saves = %+v, want ONE insert at version 2", h.repo.saves)
	}
	p, _ := h.repo.Get(context.Background(), "SKU-9")
	c, src, err := p.Classification()
	if err != nil || src != product.SourceLegacyImport || !c.HasTag(product.Hazmat) || c.DOTHazardClass() != 9 || p.Description() != "" {
		t.Fatalf("stored = %v %v %v desc=%q", c.Tags(), src, err, p.Description())
	}
}

func TestImportLegacy_NeverOverwritesNative(t *testing.T) {
	h := newHarness()
	p := h.registered("SKU-1")
	if _, err := p.Classify(mustClassification([]product.HandlingTag{product.Fragile}, "", 0), t0); err != nil {
		t.Fatal(err)
	}
	h.repo.put(p)
	out, err := importer(h).Handle(context.Background(), "ev-1", "SKU-1", []string{"Hazmat"}, "", 0)
	if err != nil || out != usecases.ImportUnchanged {
		t.Fatalf("got %q, %v", out, err)
	}
	if len(h.repo.saves) != 0 || len(h.outbox.msgs) != 0 {
		t.Fatalf("saves=%d outbox=%d, want nothing", len(h.repo.saves), len(h.outbox.msgs))
	}
	if !h.process.seen[usecases.LegacyImportConsumer+"/ev-1"] {
		t.Fatal("the id must still be claimed for a skipped import")
	}
}

func TestImportLegacy_ReplacesALegacyClassificationUnlessIdentical(t *testing.T) {
	h := newHarness()
	p := h.registered("SKU-1")
	p.ImportLegacyClassification(mustClassification([]product.HandlingTag{product.Fragile}, "", 0), t0)
	h.repo.put(p)
	ctx := context.Background()

	if out, err := importer(h).Handle(ctx, "ev-1", "SKU-1", []string{"Fragile"}, "", 0); err != nil || out != usecases.ImportUnchanged {
		t.Fatalf("identical: %q, %v", out, err)
	}
	out, err := importer(h).Handle(ctx, "ev-2", "SKU-1", []string{"TemperatureSensitive"}, "Chilled", 0)
	if err != nil || out != usecases.ImportApplied {
		t.Fatalf("replace: %q, %v", out, err)
	}
	if len(h.repo.saves) != 1 || h.repo.saves[0].loadedVersion != 2 || h.repo.saves[0].version != 3 {
		t.Fatalf("saves = %+v", h.repo.saves)
	}
}

func TestImportLegacy_DuplicateIDIsSkipped(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	if _, err := importer(h).Handle(ctx, "ev-1", "SKU-1", []string{"Fragile"}, "", 0); err != nil {
		t.Fatal(err)
	}
	out, err := importer(h).Handle(ctx, "ev-1", "SKU-1", []string{"Hazmat"}, "", 0)
	if err != nil || out != usecases.ImportDuplicate {
		t.Fatalf("got %q, %v", out, err)
	}
	if len(h.repo.saves) != 1 {
		t.Fatalf("saves = %d, want only the first", len(h.repo.saves))
	}
}

func TestImportLegacy_InvalidPayloadsAreDeterministic(t *testing.T) {
	cases := map[string]struct {
		sku  string
		tags []string
		tc   string
		dot  int
		want error
	}{
		"bad sku":          {"bad sku", []string{"Fragile"}, "", 0, product.ErrInvalidSKU},
		"unknown tag":      {"SKU-1", []string{"Sharp"}, "", 0, product.ErrUnknownHandlingTag},
		"no tags":          {"SKU-1", nil, "", 0, product.ErrNoHandlingTags},
		"broken invariant": {"SKU-1", []string{"Fragile"}, "", 3, product.ErrDOTHazardClassNotApplicable},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness()
			out, err := importer(h).Handle(context.Background(), "ev-1", tc.sku, tc.tags, tc.tc, tc.dot)
			if !errors.Is(err, usecases.ErrInvalidLegacyImport) || !errors.Is(err, tc.want) || out != "" {
				t.Fatalf("got %q, %v; want ErrInvalidLegacyImport wrapping %v", out, err, tc.want)
			}
			if h.uow.runs != 0 {
				t.Fatal("an invalid payload must be rejected before any unit of work (nothing claimed)")
			}
		})
	}
}

func TestImportLegacy_TransientErrorsRollBack(t *testing.T) {
	for name, setup := range map[string]func(h *harness){
		"claim fails": func(h *harness) { h.process.err = errBoom },
		"get fails":   func(h *harness) { h.repo.getErr = errBoom },
		"save fails":  func(h *harness) { h.repo.saveErr = errBoom },
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness()
			setup(h)
			out, err := importer(h).Handle(context.Background(), "ev-1", "SKU-1", []string{"Fragile"}, "", 0)
			if !errors.Is(err, errBoom) || errors.Is(err, usecases.ErrInvalidLegacyImport) || out != "" {
				t.Fatalf("got %q, %v", out, err)
			}
			if h.uow.failures != 1 {
				t.Fatalf("unit of work failures = %d, want 1 (so it rolled back)", h.uow.failures)
			}
		})
	}
}

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

package memory_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/claudioed/product-master/internal/adapters/outbound/memory"
	"github.com/claudioed/product-master/internal/application/outbox"
	"github.com/claudioed/product-master/internal/application/repository"
	"github.com/claudioed/product-master/internal/domain/product"
)

var t0 = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

func registered(t *testing.T, sku string) *product.Product {
	t.Helper()
	p, _, err := product.Register(product.SKU(sku), "d", t0)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func classify(t *testing.T, p *product.Product, tags ...product.HandlingTag) {
	t.Helper()
	c, err := product.NewClassification(tags, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Classify(c, t0); err != nil {
		t.Fatal(err)
	}
}

func TestProductRepo_VersionGuard(t *testing.T) {
	ctx := context.Background()
	r := memory.NewProductRepo()
	p := registered(t, "SKU-1")
	if err := r.Save(ctx, p, 0); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(ctx, p, 0); !errors.Is(err, repository.ErrConcurrentModification) {
		t.Fatalf("duplicate insert: %v", err)
	}
	got, err := r.Get(ctx, "SKU-1")
	if err != nil || got.Version() != 1 {
		t.Fatalf("get: %v %v", got, err)
	}
	classify(t, got, product.Hazmat)
	if err := r.Save(ctx, got, 1); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := r.Save(ctx, got, 1); !errors.Is(err, repository.ErrConcurrentModification) {
		t.Fatalf("stale update: %v", err)
	}
	if err := r.Save(ctx, registered(t, "SKU-2"), 1); !errors.Is(err, repository.ErrConcurrentModification) {
		t.Fatalf("update of a missing row: %v", err)
	}
	if _, err := r.Get(ctx, "SKU-2"); !errors.Is(err, repository.ErrProductNotFound) {
		t.Fatalf("missing: %v", err)
	}
	// Get returns an independent copy.
	again, _ := r.Get(ctx, "SKU-1")
	classify(t, again, product.Fragile)
	stored, _ := r.Get(ctx, "SKU-1")
	if c, _, _ := stored.Classification(); c.HasTag(product.Fragile) || stored.Version() != 2 {
		t.Fatal("mutating a loaded product changed the stored one")
	}
}

func TestProductRepo_ListPagesAndFilters(t *testing.T) {
	ctx := context.Background()
	r := memory.NewProductRepo()
	a, b, c := registered(t, "A"), registered(t, "B"), registered(t, "C")
	classify(t, a, product.Hazmat)
	classify(t, c, product.Fragile)
	for _, p := range []*product.Product{c, a, b} {
		if err := r.Save(ctx, p, 0); err != nil {
			t.Fatal(err)
		}
	}
	yes, no := true, false
	cases := []struct {
		name   string
		filter repository.ListFilter
		after  product.SKU
		limit  int
		want   string
	}{
		{"all", repository.ListFilter{}, "", 10, "ABC"},
		{"after", repository.ListFilter{}, "A", 10, "BC"},
		{"limit", repository.ListFilter{}, "", 2, "AB"},
		{"classified", repository.ListFilter{Classified: &yes}, "", 10, "AC"},
		{"unclassified", repository.ListFilter{Classified: &no}, "", 10, "B"},
		{"tag", repository.ListFilter{HandlingTag: product.Hazmat}, "", 10, "A"},
		{"tag and unclassified", repository.ListFilter{HandlingTag: product.Hazmat, Classified: &no}, "", 10, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := r.List(ctx, tc.filter, tc.after, tc.limit)
			if err != nil {
				t.Fatal(err)
			}
			s := ""
			for _, p := range got {
				s += string(p.SKU())
			}
			if s != tc.want {
				t.Fatalf("got %q, want %q", s, tc.want)
			}
		})
	}
}

func TestUnitOfWork_RollsBackEveryParticipant(t *testing.T) {
	ctx := context.Background()
	products, ob, processed := memory.NewProductRepo(), memory.NewOutboxRepo(), memory.NewProcessedEventRepo()
	uow := memory.NewUnitOfWork(products, ob, processed)
	boom := errors.New("boom")
	err := uow.Do(ctx, func(ctx context.Context) error {
		_ = products.Save(ctx, registered(t, "SKU-1"), 0)
		_ = ob.Insert(ctx, outbox.Message{EventType: "x"})
		_, _ = processed.Claim(ctx, "c", "id")
		// A nested Do joins the outer unit of work.
		return uow.Do(ctx, func(context.Context) error { return boom })
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if _, err := products.Get(ctx, "SKU-1"); !errors.Is(err, repository.ErrProductNotFound) {
		t.Fatal("product survived the rollback")
	}
	if len(ob.Messages()) != 0 || processed.Has("c", "id") {
		t.Fatal("outbox or claim survived the rollback")
	}

	if err := uow.Do(ctx, func(ctx context.Context) error { return products.Save(ctx, registered(t, "SKU-1"), 0) }); err != nil {
		t.Fatal(err)
	}
	if _, err := products.Get(ctx, "SKU-1"); err != nil {
		t.Fatal("committed product missing")
	}
}

func TestOutboxRepo_DrainStopsAtTheFirstFailure(t *testing.T) {
	ctx := context.Background()
	ob := memory.NewOutboxRepo()
	_ = ob.Insert(ctx, outbox.Message{EventType: "a"}, outbox.Message{EventType: "b"}, outbox.Message{EventType: "c"})
	boom := errors.New("boom")
	var sent []string
	n, err := ob.Drain(ctx, 10, func(_ context.Context, m outbox.Message) error {
		if m.EventType == "b" {
			return boom
		}
		sent = append(sent, m.EventType)
		return nil
	})
	if n != 1 || !errors.Is(err, boom) || len(sent) != 1 || ob.Unpublished() != 2 || ob.LastError(1) == "" {
		t.Fatalf("n=%d err=%v sent=%v unpublished=%d", n, err, sent, ob.Unpublished())
	}
	n, err = ob.Drain(ctx, 1, func(context.Context, outbox.Message) error { return nil })
	if n != 1 || err != nil || ob.Unpublished() != 1 || ob.LastError(1) != "" {
		t.Fatalf("limited drain: n=%d err=%v unpublished=%d", n, err, ob.Unpublished())
	}
}

func TestProcessedEventRepo_ClaimsOnce(t *testing.T) {
	r := memory.NewProcessedEventRepo()
	ctx := context.Background()
	if ok, _ := r.Claim(ctx, "c", "1"); !ok {
		t.Fatal("first claim must succeed")
	}
	if ok, _ := r.Claim(ctx, "c", "1"); ok {
		t.Fatal("second claim must fail")
	}
	if ok, _ := r.Claim(ctx, "other", "1"); !ok {
		t.Fatal("claims are per consumer")
	}
}

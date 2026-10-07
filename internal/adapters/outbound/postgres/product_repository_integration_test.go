//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/product-master/internal/adapters/outbound/clock"
	outboundkafka "github.com/claudioed/product-master/internal/adapters/outbound/kafka"
	"github.com/claudioed/product-master/internal/adapters/outbound/postgres"
	"github.com/claudioed/product-master/internal/application/outbox"
	"github.com/claudioed/product-master/internal/application/repository"
	"github.com/claudioed/product-master/internal/application/usecases"
	"github.com/claudioed/product-master/internal/domain/product"
)

var t0 = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

func newProduct(t *testing.T, sku string) *product.Product {
	t.Helper()
	p, _, err := product.Register(product.SKU(sku), "desc "+sku, t0)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func classify(t *testing.T, p *product.Product, tags []product.HandlingTag, tc product.TemperatureClass, dot product.DOTHazardClass) {
	t.Helper()
	c, err := product.NewClassification(tags, tc, dot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Classify(c, t0); err != nil {
		t.Fatal(err)
	}
}

// TestProductRepo_VersionGuard proves the two-branch Save against real
// Postgres: an insert never overwrites, an update applies only on the loaded
// version, and the loser of a race gets ErrConcurrentModification.
func TestProductRepo_VersionGuard(t *testing.T) {
	ctx := context.Background()
	repo := postgres.NewProductRepo(startPostgresPool(t))

	p := newProduct(t, "SKU-1")
	if err := repo.Save(ctx, p, 0); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := repo.Save(ctx, newProduct(t, "SKU-1"), 0); !errors.Is(err, repository.ErrConcurrentModification) {
		t.Fatalf("duplicate insert: %v, want ErrConcurrentModification", err)
	}

	// Two writers load version 1; the first update wins, the second loses.
	a, _ := repo.Get(ctx, "SKU-1")
	b, _ := repo.Get(ctx, "SKU-1")
	classify(t, a, []product.HandlingTag{product.Hazmat}, "", 9)
	if _, err := b.ChangeDescription("other", t0); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, a, 1); err != nil {
		t.Fatalf("first update: %v", err)
	}
	if err := repo.Save(ctx, b, 1); !errors.Is(err, repository.ErrConcurrentModification) {
		t.Fatalf("stale update: %v, want ErrConcurrentModification", err)
	}
	if err := repo.Save(ctx, newProduct(t, "SKU-404"), 1); !errors.Is(err, repository.ErrConcurrentModification) {
		t.Fatalf("update of a missing row: %v", err)
	}

	got, err := repo.Get(ctx, "SKU-1")
	if err != nil {
		t.Fatal(err)
	}
	c, src, _ := got.Classification()
	if got.Version() != 2 || got.Description() != "desc SKU-1" || src != product.SourceNative || !c.HasTag(product.Hazmat) || c.DOTHazardClass() != 9 {
		t.Fatalf("stored = v%d %q %v %v", got.Version(), got.Description(), src, c.Tags())
	}
	if _, err := repo.Get(ctx, "SKU-404"); !errors.Is(err, repository.ErrProductNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

// TestProductRepo_RoundTripsTheWholeAggregate checks every column maps back.
func TestProductRepo_RoundTripsTheWholeAggregate(t *testing.T) {
	ctx := context.Background()
	repo := postgres.NewProductRepo(startPostgresPool(t))
	p := newProduct(t, "SKU-1")
	classify(t, p, []product.HandlingTag{product.TemperatureSensitive, product.Hazmat}, product.Frozen, 3)
	d, _ := product.NewUnitDimensions(200, 120, 80, 1500)
	p.DeclareDimensions(d, t0)
	md, _ := product.NewUnitDimensions(205, 121, 82, 1720)
	m, err := product.NewMeasurement(md, t0.Add(-time.Hour).Add(123456789), "CUBISCAN-03", t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.RecordMeasurement(m, t0); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, p, 0); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, "SKU-1")
	if err != nil {
		t.Fatal(err)
	}
	assertSameState(t, got, p)
	// Repeating the persisted measurement is no change (microsecond precision survives).
	if events, err := got.RecordMeasurement(m, t0); err != nil || len(events) != 0 {
		t.Fatalf("re-recording the stored measurement: %v %v", events, err)
	}
}

// assertSameState compares every persisted part of two products.
func assertSameState(t *testing.T, got, want *product.Product) {
	t.Helper()
	gc, gs, gerr := got.Classification()
	wc, ws, werr := want.Classification()
	if got.Version() != want.Version() || got.Description() != want.Description() || gs != ws || (gerr == nil) != (werr == nil) || !gc.Equal(wc) {
		t.Fatalf("classification/header differ: got v%d %v %v, want v%d %v %v", got.Version(), gs, gc.Tags(), want.Version(), ws, wc.Tags())
	}
	gp, wp := got.PhysicalProfile(), want.PhysicalProfile()
	gd, _ := gp.Declared()
	wd, _ := wp.Declared()
	gm, _ := gp.Measured()
	wm, _ := wp.Measured()
	if gd != wd || gm.Dimensions() != wm.Dimensions() || !gm.MeasuredAt().Equal(wm.MeasuredAt()) || gm.DeviceID() != wm.DeviceID() || gp.Discrepancy() != wp.Discrepancy() {
		t.Fatalf("physical profile differs: got %+v %+v, want %+v %+v", gd, gm, wd, wm)
	}
}

// TestProductRepo_ListPagesInByteOrderWithFilters pages through real
// Postgres via the ListProducts use case.
func TestProductRepo_ListPagesInByteOrderWithFilters(t *testing.T) {
	ctx := context.Background()
	repo := postgres.NewProductRepo(startPostgresPool(t))
	// "B" < "C" < "_x" < "a" in byte order (a linguistic collation would differ).
	seed := map[string][]product.HandlingTag{"a": {product.Fragile}, "_x": {product.Hazmat}, "B": nil, "C": {product.Fragile}}
	for sku, tags := range seed {
		p := newProduct(t, sku)
		if tags != nil {
			classify(t, p, tags, "", 0)
		}
		if err := repo.Save(ctx, p, 0); err != nil {
			t.Fatal(err)
		}
	}
	list := &usecases.ListProducts{Products: repo}
	if seen, want := pageAll(t, list), []product.SKU{"B", "C", "_x", "a"}; !equalSKUs(seen, want) {
		t.Fatalf("paged = %v, want %v", seen, want)
	}

	yes, no := true, false
	for name, tc := range map[string]struct {
		q    usecases.ListProductsQuery
		want []product.SKU
	}{
		"classified":   {usecases.ListProductsQuery{Classified: &yes}, []product.SKU{"C", "_x", "a"}},
		"unclassified": {usecases.ListProductsQuery{Classified: &no}, []product.SKU{"B"}},
		"tag":          {usecases.ListProductsQuery{HandlingTag: "Fragile"}, []product.SKU{"C", "a"}},
		"tag+limit":    {usecases.ListProductsQuery{HandlingTag: "Fragile", Limit: 1}, []product.SKU{"C"}},
	} {
		t.Run(name, func(t *testing.T) {
			page, err := list.Handle(ctx, tc.q)
			if err != nil {
				t.Fatal(err)
			}
			if got := skusOf(page.Items); !equalSKUs(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// pageAll walks every page of size 1 through the opaque cursor.
func pageAll(t *testing.T, list *usecases.ListProducts) []product.SKU {
	t.Helper()
	var seen []product.SKU
	cursor := ""
	for {
		page, err := list.Handle(context.Background(), usecases.ListProductsQuery{Limit: 1, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		seen = append(seen, skusOf(page.Items)...)
		if page.NextCursor == "" {
			return seen
		}
		cursor = page.NextCursor
	}
}

func skusOf(ps []*product.Product) []product.SKU {
	out := make([]product.SKU, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.SKU())
	}
	return out
}

func equalSKUs(a, b []product.SKU) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func outboxTypes(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT event_type FROM outbox_events ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

// TestUseCasesCommitProductAndOutboxAtomically drives the real use cases
// over Postgres: the product row and its outbox rows (FULL type) commit
// together, and a failing unit of work leaves neither behind.
func TestUseCasesCommitProductAndOutboxAtomically(t *testing.T) {
	ctx := context.Background()
	pool := startPostgresPool(t)
	repo, ob, uow := postgres.NewProductRepo(pool), postgres.NewOutboxRepo(pool), postgres.NewUnitOfWork(pool)
	w := usecases.Writer{Products: repo, Outbox: ob, Encoder: outboundkafka.NewEncoder(), UoW: uow, Clock: clock.System{}}

	if _, err := (&usecases.RegisterProduct{Writer: w}).Handle(ctx, usecases.RegisterProductCommand{SKU: "SKU-1", Description: "d"}); err != nil {
		t.Fatal(err)
	}
	if _, err := (&usecases.ClassifyProduct{Writer: w}).Handle(ctx, usecases.ClassifyProductCommand{SKU: "SKU-1", HandlingTags: []string{"Hazmat"}}); err != nil {
		t.Fatal(err)
	}
	prefix := "com.warehouse.wms.product-master.product."
	if got := outboxTypes(t, pool); len(got) != 2 || got[0] != prefix+"ProductRegistered" || got[1] != prefix+"ProductClassified" {
		t.Fatalf("outbox = %v", got)
	}

	boom := errors.New("boom")
	err := uow.Do(ctx, func(ctx context.Context) error {
		if err := repo.Save(ctx, newProduct(t, "SKU-2"), 0); err != nil {
			return err
		}
		if err := ob.Insert(ctx, outbox.Message{EventID: "x", Topic: "t", EventType: "x", Subject: "SKU-2", DataSchema: "x", Value: []byte("{}")}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if _, err := repo.Get(ctx, "SKU-2"); !errors.Is(err, repository.ErrProductNotFound) {
		t.Fatal("the product of a rolled-back unit of work survived")
	}
	if got := outboxTypes(t, pool); len(got) != 2 {
		t.Fatalf("the outbox row of a rolled-back unit of work survived: %v", got)
	}
}

// TestProcessedEventRepo_ClaimsOnceAndRollsBack: claims are once per
// (consumer, id) and a rolled-back claim does not count.
func TestProcessedEventRepo_ClaimsOnceAndRollsBack(t *testing.T) {
	ctx := context.Background()
	pool := startPostgresPool(t)
	uow, processed := postgres.NewUnitOfWork(pool), postgres.NewProcessedEventRepo(pool)
	if ok, err := processed.Claim(ctx, "c", "id-1"); err != nil || !ok {
		t.Fatalf("first claim: %v %v", ok, err)
	}
	if ok, err := processed.Claim(ctx, "c", "id-1"); err != nil || ok {
		t.Fatalf("second claim: %v %v", ok, err)
	}
	_ = uow.Do(ctx, func(ctx context.Context) error {
		_, _ = processed.Claim(ctx, "c", "id-2")
		return errors.New("boom")
	})
	if ok, _ := processed.Claim(ctx, "c", "id-2"); !ok {
		t.Fatal("a rolled-back claim must not count")
	}
}

//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	outboundkafka "github.com/claudioed/product-master/internal/adapters/outbound/kafka"
	"github.com/claudioed/product-master/internal/adapters/outbound/postgres"
	"github.com/claudioed/product-master/internal/application/repository"
	"github.com/claudioed/product-master/internal/application/usecases"
	"github.com/claudioed/product-master/internal/domain/product"
	"github.com/claudioed/product-master/internal/domain/shared/gs1"
)

// Real GTINs (mod-10 checked): an EAN-13 each, two GTIN-14 cases and a pallet.
const (
	gtinEach  = "4006381333931"
	gtinCase  = "24006381333935"
	gtinCase2 = "34006381333932"
	gtinPlt   = "54006381333936"
)

// savedProduct registers sku and persists it at version 1.
func savedProduct(t *testing.T, repo *postgres.ProductRepo, sku string) *product.Product {
	t.Helper()
	p := newProduct(t, sku)
	if err := repo.Save(context.Background(), p, 0); err != nil {
		t.Fatalf("insert %s: %v", sku, err)
	}
	return p
}

// declareAndSave applies spec to the stored product and saves it guarded by the loaded version.
func declareAndSave(t *testing.T, repo *postgres.ProductRepo, sku string, spec product.PackLevelSpec, now time.Time) *product.Product {
	t.Helper()
	ctx := context.Background()
	p, err := repo.Get(ctx, product.SKU(sku))
	if err != nil {
		t.Fatal(err)
	}
	loaded := p.Version()
	if _, err := p.DeclarePackLevel(spec, now); err != nil {
		t.Fatalf("declare %s: %v", spec.Level, err)
	}
	if err := repo.Save(ctx, p, loaded); err != nil {
		t.Fatalf("save: %v", err)
	}
	return p
}

func eachSpec() product.PackLevelSpec {
	return product.PackLevelSpec{Level: product.PackLevelEach, GTIN: mustGTIN(gtinEach), IsOrderable: true}
}

func caseSpec(gtin string, qty int) product.PackLevelSpec {
	return product.PackLevelSpec{Level: product.PackLevelCase, GTIN: mustGTIN(gtin), ChildGTIN: mustGTIN(gtinEach), QtyOfChild: qty, IsOrderable: true, IsDespatchUnit: true}
}

func mustGTIN(raw string) gs1.GTIN {
	g, err := product.ParseGTIN(raw)
	if err != nil {
		panic(err)
	}
	return g
}

func levelCount(t *testing.T, pool *pgxpool.Pool, sku string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM product_pack_levels WHERE sku = $1`, sku).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The whole hierarchy survives a round trip through real Postgres: levels,
// chain, declared and measured dimensions, the end-dated history.
func TestPackLevels_Postgres_RoundTrip(t *testing.T) {
	ctx := context.Background()
	pool := startPostgresPool(t)
	repo := postgres.NewProductRepo(pool)
	savedProduct(t, repo, "SKU-1")

	each := eachSpec()
	d := product.RehydrateUnitDimensions(100, 50, 40, 500)
	each.Dims = &d
	declareAndSave(t, repo, "SKU-1", each, t0)
	declareAndSave(t, repo, "SKU-1", caseSpec(gtinCase, 12), t0.Add(time.Hour))

	p, _ := repo.Get(ctx, "SKU-1")
	loaded := p.Version()
	m, err := product.NewMeasurement(product.RehydrateUnitDimensions(410, 310, 210, 6900), t0.Add(2*time.Hour), "CUBISCAN-03", t0.Add(3*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.RecordPackLevelMeasurement(product.PackLevelCase, m, t0.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, p, loaded); err != nil {
		t.Fatal(err)
	}
	// a new GTIN at the case level end-dates the old one in the same save
	declareAndSave(t, repo, "SKU-1", caseSpec(gtinCase2, 24), t0.Add(4*time.Hour))

	got, err := repo.Get(ctx, "SKU-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Version() != 6 {
		t.Fatalf("version = %d, want 6 (register, each, case, measure, then supersede = retire + declare)", got.Version())
	}
	levels := got.PackLevels().Levels()
	if len(levels) != 3 {
		t.Fatalf("levels = %d, want each + the retired case + the new case", len(levels))
	}
	if levels[0].Level() != product.PackLevelEach || levels[0].UnitsPerPack() != 1 || !levels[0].ChildGTIN().IsZero() {
		t.Fatalf("each = %+v", levels[0])
	}
	if dd, ok := levels[0].DeclaredDimensions(); !ok || dd != d {
		t.Fatalf("each dims = %+v %v", dd, ok)
	}
	old, newer := levels[1], levels[2]
	if old.GTIN().String() != gtinCase || old.Active() || old.QtyOfChild() != 12 || old.UnitsPerPack() != 12 {
		t.Fatalf("old case = %+v", old)
	}
	if to, ended := old.EffectiveTo(); !ended || !to.Equal(t0.Add(4*time.Hour)) {
		t.Fatalf("old effectiveTo = %v %v", to, ended)
	}
	if gm, ok := old.Measurement(); !ok || gm != m {
		t.Fatalf("old case measurement = %+v %v, want %+v", gm, ok, m)
	}
	if newer.GTIN().String() != gtinCase2 || !newer.Active() || newer.UnitsPerPack() != 24 || newer.ChildGTIN().String() != "04006381333931" || !newer.IsDespatchUnit() {
		t.Fatalf("new case = %+v", newer)
	}
	if !newer.EffectiveFrom().Equal(t0.Add(4 * time.Hour)) {
		t.Fatalf("effectiveFrom = %v", newer.EffectiveFrom())
	}

	t.Run("GetByGTIN resolves active, retired and any spelling", func(t *testing.T) {
		for _, raw := range []string{gtinCase2, gtinCase, "04006381333931"} {
			p, err := repo.GetByGTIN(ctx, mustGTIN(raw))
			if err != nil || p.SKU() != "SKU-1" || len(p.PackLevels().Levels()) != 3 {
				t.Fatalf("%s: %v %v", raw, p, err)
			}
		}
		if _, err := repo.GetByGTIN(ctx, mustGTIN(gtinPlt)); !errors.Is(err, repository.ErrGtinNotFound) {
			t.Fatalf("unknown gtin: %v", err)
		}
	})
	t.Run("List carries the pack levels", func(t *testing.T) {
		savedProduct(t, repo, "SKU-2")
		items, err := repo.List(ctx, repository.ListFilter{}, "", 10)
		if err != nil || len(items) != 2 {
			t.Fatalf("list: %v %v", items, err)
		}
		if n := len(items[0].PackLevels().Levels()); n != 3 {
			t.Fatalf("SKU-1 listed with %d levels", n)
		}
		if n := len(items[1].PackLevels().Levels()); n != 0 {
			t.Fatalf("SKU-2 listed with %d levels", n)
		}
	})
	t.Run("saving an unrelated change rewrites no pack level row", func(t *testing.T) {
		var before time.Time
		if err := pool.QueryRow(ctx, `SELECT max(updated_at) FROM product_pack_levels`).Scan(&before); err != nil {
			t.Fatal(err)
		}
		p, _ := repo.Get(ctx, "SKU-1")
		loaded := p.Version()
		if _, err := p.ChangeDescription("renamed", t0); err != nil {
			t.Fatal(err)
		}
		if err := repo.Save(ctx, p, loaded); err != nil {
			t.Fatal(err)
		}
		var after time.Time
		if err := pool.QueryRow(ctx, `SELECT max(updated_at) FROM product_pack_levels`).Scan(&after); err != nil {
			t.Fatal(err)
		}
		if !after.Equal(before) {
			t.Fatalf("pack level rows were rewritten: %v -> %v", before, after)
		}
	})
}

// The version guard covers the pack levels: the loser of a race leaves no
// pack level row and no product change behind.
func TestPackLevels_Postgres_VersionGuardCoversThePackLevels(t *testing.T) {
	ctx := context.Background()
	pool := startPostgresPool(t)
	repo := postgres.NewProductRepo(pool)
	savedProduct(t, repo, "SKU-1")

	a, _ := repo.Get(ctx, "SKU-1")
	b, _ := repo.Get(ctx, "SKU-1")
	if _, err := a.DeclarePackLevel(eachSpec(), t0); err != nil {
		t.Fatal(err)
	}
	if _, err := b.DeclarePackLevel(product.PackLevelSpec{Level: product.PackLevelEach, GTIN: mustGTIN(gtinCase)}, t0); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, a, 1); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, b, 1); !errors.Is(err, repository.ErrConcurrentModification) {
		t.Fatalf("stale save = %v, want ErrConcurrentModification", err)
	}
	if n := levelCount(t, pool, "SKU-1"); n != 1 {
		t.Fatalf("%d pack level rows, want only the winner's", n)
	}
	got, _ := repo.Get(ctx, "SKU-1")
	if lv, _ := got.PackLevels().Active(product.PackLevelEach); lv.GTIN().String() != "04006381333931" {
		t.Fatalf("active each = %s", lv.GTIN())
	}
}

// ErrGtinAlreadyAssigned is fleet-wide: the unique index on gtin refuses a
// second SKU even when the use case's pre-check is bypassed, and the refused
// save rolls the product row back with it.
func TestPackLevels_Postgres_AGTINIsUniqueFleetWide(t *testing.T) {
	ctx := context.Background()
	pool := startPostgresPool(t)
	repo := postgres.NewProductRepo(pool)
	savedProduct(t, repo, "SKU-1")
	savedProduct(t, repo, "SKU-2")
	declareAndSave(t, repo, "SKU-1", eachSpec(), t0)

	p, _ := repo.Get(ctx, "SKU-2")
	if _, err := p.DeclarePackLevel(eachSpec(), t0); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, p, 1); !errors.Is(err, product.ErrGtinAlreadyAssigned) {
		t.Fatalf("save = %v, want ErrGtinAlreadyAssigned", err)
	}
	if n := levelCount(t, pool, "SKU-2"); n != 0 {
		t.Fatalf("SKU-2 has %d pack level rows", n)
	}
	if again, _ := repo.Get(ctx, "SKU-2"); again.Version() != 1 {
		t.Fatalf("SKU-2 version = %d: the product row must roll back with the refused level", again.Version())
	}

	// and through the unit of work with the real outbox: nothing is enqueued either
	w := fanoutWriter(pool)
	_, err := (&usecases.DeclarePackLevel{Writer: w}).Handle(ctx, usecases.DeclarePackLevelCommand{SKU: "SKU-2", PackLevelInput: usecases.PackLevelInput{Level: "EACH", GTIN: gtinEach}})
	if !errors.Is(err, product.ErrGtinAlreadyAssigned) {
		t.Fatalf("use case = %v", err)
	}
	if n := len(analyticsOutboxRows(t, pool)); n != 0 {
		t.Fatalf("outbox rows = %d", n)
	}
}

// The table's own CHECKs and indexes say no even to hand-written SQL.
func TestPackLevels_Postgres_SchemaRefusesWhatTheDomainRefuses(t *testing.T) {
	ctx := context.Background()
	pool := startPostgresPool(t)
	repo := postgres.NewProductRepo(pool)
	savedProduct(t, repo, "SKU-1")
	savedProduct(t, repo, "SKU-2")
	const ins = `INSERT INTO product_pack_levels (sku, level, gtin, child_gtin, qty_of_child, units_per_pack, is_orderable, is_despatch_unit, effective_from, effective_to)
		VALUES ($1, $2, $3, $4, $5, $6, false, false, now(), $7)`
	child := func(s string) *string { return &s }
	qty := func(n int32) *int32 { return &n }
	tests := []struct {
		name       string
		sku, level string
		gtin       string
		child      *string
		qty        *int32
		units      int32
		to         *time.Time
		wantCode   string
		wantConstr string
	}{
		{"13 digit gtin", "SKU-1", "EACH", gtinEach, nil, nil, 1, nil, "23514", ""},
		{"variable measure", "SKU-1", "CASE", "94006381333934", child("04006381333931"), qty(2), 2, nil, "23514", ""},
		{"all zero gtin", "SKU-1", "EACH", "00000000000000", nil, nil, 1, nil, "23514", ""},
		{"unknown level", "SKU-1", "BOX", "04006381333931", nil, nil, 1, nil, "23514", ""},
		{"each with a child", "SKU-1", "EACH", "04006381333931", child("24006381333935"), qty(2), 2, nil, "23514", ""},
		{"case without a child", "SKU-1", "CASE", "24006381333935", nil, nil, 2, nil, "23514", ""},
		{"case of one", "SKU-1", "CASE", "24006381333935", child("04006381333931"), qty(1), 1, nil, "23514", ""},
		{"zero units", "SKU-1", "CASE", "24006381333935", child("04006381333931"), qty(2), 0, nil, "23514", ""},
		{"ends before it starts", "SKU-1", "EACH", "04006381333931", nil, nil, 1, ptrTime(time.Now().Add(-24 * time.Hour)), "23514", ""},
		{"unknown sku", "SKU-404", "EACH", "04006381333931", nil, nil, 1, nil, "23503", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := pool.Exec(ctx, ins, tt.sku, tt.level, tt.gtin, tt.child, tt.qty, tt.units, tt.to)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != tt.wantCode {
				t.Fatalf("err = %v, want SQLSTATE %s", err, tt.wantCode)
			}
		})
	}
	// valid rows: one active each for SKU-1
	if _, err := pool.Exec(ctx, ins, "SKU-1", "EACH", "04006381333931", nil, nil, 1, nil); err != nil {
		t.Fatalf("valid row: %v", err)
	}
	for _, tt := range []struct {
		name, sku, gtin string
	}{
		{"same gtin, other sku", "SKU-2", "04006381333931"},
		{"second active each for the sku", "SKU-1", "05901234123457"},
	} {
		_, err := pool.Exec(ctx, ins, tt.sku, "EACH", tt.gtin, nil, nil, 1, nil)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
			t.Fatalf("%s: err = %v, want a unique violation", tt.name, err)
		}
	}
	// an end-dated row does not block a new active level
	end := time.Now().Add(time.Hour)
	if _, err := pool.Exec(ctx, ins, "SKU-2", "EACH", "05901234123457", nil, nil, 1, &end); err != nil {
		t.Fatalf("end-dated row: %v", err)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

// The use cases over real Postgres and the real outbox: the three events land
// as outbox rows with the FULL CloudEvents type on the integration topic and
// nowhere else, in the same transaction as the pack level rows.
func TestPackLevels_Postgres_UseCasesWriteOutboxRowsWithTheFullType(t *testing.T) {
	ctx := context.Background()
	pool := startPostgresPool(t)
	w := fanoutWriter(pool)
	if _, err := (&usecases.RegisterProduct{Writer: w}).Handle(ctx, usecases.RegisterProductCommand{SKU: "SKU-1", Description: "d"}); err != nil {
		t.Fatal(err)
	}
	declare := func(in usecases.PackLevelInput) {
		t.Helper()
		if _, err := (&usecases.DeclarePackLevel{Writer: w}).Handle(ctx, usecases.DeclarePackLevelCommand{SKU: "SKU-1", PackLevelInput: in}); err != nil {
			t.Fatalf("declare %s: %v", in.Level, err)
		}
	}
	declare(usecases.PackLevelInput{Level: "EACH", GTIN: gtinEach})
	declare(usecases.PackLevelInput{Level: "CASE", GTIN: gtinCase, ChildGTIN: gtinEach, QtyOfChild: 12})
	if _, err := (&usecases.RecordPackLevelMeasurement{Writer: w}).Handle(ctx, usecases.RecordPackLevelMeasurementCommand{
		SKU: "SKU-1", Level: "CASE", DimensionsInput: usecases.DimensionsInput{LengthMm: 400, WidthMm: 300, HeightMm: 200, WeightG: 6500},
		MeasuredAt: time.Now().UTC().Add(-time.Minute), DeviceID: "CUBISCAN-03",
	}); err != nil {
		t.Fatal(err)
	}
	declare(usecases.PackLevelInput{Level: "CASE", GTIN: gtinCase2, ChildGTIN: gtinEach, QtyOfChild: 24}) // supersedes
	if _, err := (&usecases.RetirePackLevel{Writer: w}).Handle(ctx, usecases.RetirePackLevelCommand{SKU: "SKU-1", Level: "CASE", Reason: "discontinued"}); err != nil {
		t.Fatal(err)
	}

	rows := analyticsOutboxRows(t, pool)
	// register -> 2 rows (integration + analytics); every pack level event -> 1
	want := []struct{ topic, eventType string }{
		{outboundkafka.Topic, "com.warehouse.wms.product-master.product.ProductRegistered"},
		{outboundkafka.AnalyticsTopic, "com.warehouse.wms.product-master.product.ProductRegistered"},
		{outboundkafka.Topic, "com.warehouse.wms.product-master.product.ProductPackLevelDeclared"},
		{outboundkafka.Topic, "com.warehouse.wms.product-master.product.ProductPackLevelDeclared"},
		{outboundkafka.Topic, "com.warehouse.wms.product-master.product.ProductPackLevelMeasured"},
		{outboundkafka.Topic, "com.warehouse.wms.product-master.product.ProductPackLevelRetired"},
		{outboundkafka.Topic, "com.warehouse.wms.product-master.product.ProductPackLevelDeclared"},
		{outboundkafka.Topic, "com.warehouse.wms.product-master.product.ProductPackLevelRetired"},
	}
	if len(rows) != len(want) {
		t.Fatalf("outbox rows = %d, want %d: %+v", len(rows), len(want), rows)
	}
	for i, wr := range want {
		if rows[i].topic != wr.topic || rows[i].eventType != wr.eventType {
			t.Errorf("row %d = %s %s, want %s %s", i, rows[i].topic, rows[i].eventType, wr.topic, wr.eventType)
		}
	}
	var reasons int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE convert_from(value,'UTF8')::jsonb -> 'data' ->> 'reason' = 'superseded'`).Scan(&reasons); err != nil || reasons != 1 {
		t.Fatalf("superseded events = %d, %v", reasons, err)
	}
	if rows[5].dataschema != "urn:warehouse:product-master:events:ProductPackLevelRetired:v1" {
		t.Fatalf("dataschema = %q", rows[5].dataschema)
	}

	// the stored aggregate agrees: the second case is retired too, nothing is active at the case level
	got, _ := postgres.NewProductRepo(pool).Get(ctx, "SKU-1")
	if _, ok := got.PackLevels().Active(product.PackLevelCase); ok || got.Version() != 7 {
		t.Fatalf("case active? version %d", got.Version())
	}
}

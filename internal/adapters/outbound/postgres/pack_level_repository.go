package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/claudioed/product-master/internal/application/repository"
	"github.com/claudioed/product-master/internal/domain/product"
	"github.com/claudioed/product-master/internal/domain/shared/gs1"
)

// The pack levels of a Product (ADR 0011) live in product_pack_levels and
// are written by ProductRepo.Save in the same transaction as the products
// row, so the aggregate's version guard covers them.

const (
	packLevelColumns = `sku, level, gtin, child_gtin, qty_of_child, units_per_pack,
	declared_length_mm, declared_width_mm, declared_height_mm, declared_weight_g,
	measured_length_mm, measured_width_mm, measured_height_mm, measured_weight_g, measured_at, measured_device_id,
	is_orderable, is_despatch_unit, effective_from, effective_to`

	insertPackLevel = `INSERT INTO product_pack_levels (` + packLevelColumns + `)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)`

	updatePackLevel = `UPDATE product_pack_levels SET
	child_gtin = $3, qty_of_child = $4, units_per_pack = $5,
	declared_length_mm = $6, declared_width_mm = $7, declared_height_mm = $8, declared_weight_g = $9,
	measured_length_mm = $10, measured_width_mm = $11, measured_height_mm = $12, measured_weight_g = $13,
	measured_at = $14, measured_device_id = $15, is_orderable = $16, is_despatch_unit = $17,
	effective_from = $18, effective_to = $19, updated_at = now()
	WHERE sku = $1 AND gtin = $2`

	// Postgres unique_violation.
	sqlStateUniqueViolation = "23505"
	// The unique index on gtin (migration 0002).
	gtinUniqueIndex = "ux_product_pack_levels_gtin"
)

// packLevelRow is the persisted shape of one pack level; nullable columns
// are pointers.
type packLevelRow struct {
	sku              string
	level            string
	gtin             string
	childGTIN        *string
	qtyOfChild       *int32
	unitsPerPack     int32
	declared         [4]*int64
	measured         [4]*int64
	measuredAt       *time.Time
	measuredDeviceID *string
	isOrderable      bool
	isDespatchUnit   bool
	effectiveFrom    time.Time
	effectiveTo      *time.Time
}

func (r *packLevelRow) targets() []any {
	return []any{
		&r.sku, &r.level, &r.gtin, &r.childGTIN, &r.qtyOfChild, &r.unitsPerPack,
		&r.declared[0], &r.declared[1], &r.declared[2], &r.declared[3],
		&r.measured[0], &r.measured[1], &r.measured[2], &r.measured[3], &r.measuredAt, &r.measuredDeviceID,
		&r.isOrderable, &r.isDespatchUnit, &r.effectiveFrom, &r.effectiveTo,
	}
}

func (r *packLevelRow) values() []any {
	return []any{
		r.sku, r.level, r.gtin, r.childGTIN, r.qtyOfChild, r.unitsPerPack,
		r.declared[0], r.declared[1], r.declared[2], r.declared[3],
		r.measured[0], r.measured[1], r.measured[2], r.measured[3], r.measuredAt, r.measuredDeviceID,
		r.isOrderable, r.isDespatchUnit, r.effectiveFrom, r.effectiveTo,
	}
}

// updateArgs are the arguments of updatePackLevel: (sku, gtin) then the
// mutable columns.
func (r *packLevelRow) updateArgs() []any {
	v := r.values()
	return append([]any{v[0], v[2]}, v[3:]...)
}

// rowOfPackLevel flattens one definition into its persisted shape.
func rowOfPackLevel(sku product.SKU, d product.PackLevelDef) packLevelRow {
	row := packLevelRow{
		sku: string(sku), level: string(d.Level()), gtin: d.GTIN().String(),
		unitsPerPack: int32(d.UnitsPerPack()), // #nosec G115 -- bounded by product.MaxPackQuantity
		isOrderable:  d.IsOrderable(), isDespatchUnit: d.IsDespatchUnit(), effectiveFrom: d.EffectiveFrom(),
	}
	if !d.ChildGTIN().IsZero() {
		child := d.ChildGTIN().String()
		qty := int32(d.QtyOfChild()) // #nosec G115 -- bounded by product.MaxPackQuantity
		row.childGTIN, row.qtyOfChild = &child, &qty
	}
	if dims, ok := d.DeclaredDimensions(); ok {
		row.declared = dimValues(dims)
	}
	if m, ok := d.Measurement(); ok {
		row.measured = dimValues(m.Dimensions())
		at := m.MeasuredAt()
		row.measuredAt = &at
		row.measuredDeviceID = optString(m.DeviceID())
	}
	if to, ended := d.EffectiveTo(); ended {
		row.effectiveTo = &to
	}
	return row
}

// toDef rehydrates the definition (no invariant re-check: the row was
// written from a valid aggregate and the table's CHECKs).
func (r *packLevelRow) toDef() product.PackLevelDef {
	spec := product.PackLevelSpec{
		Level: product.PackLevel(r.level), GTIN: mustStoredGTIN(r.gtin), UnitsPerPack: int(r.unitsPerPack),
		Dims: dims(r.declared), IsOrderable: r.isOrderable, IsDespatchUnit: r.isDespatchUnit,
	}
	if r.childGTIN != nil {
		spec.ChildGTIN = mustStoredGTIN(*r.childGTIN)
	}
	if r.qtyOfChild != nil {
		spec.QtyOfChild = int(*r.qtyOfChild)
	}
	var measured *product.Measurement
	if d := dims(r.measured); d != nil && r.measuredAt != nil {
		m := product.RehydrateMeasurement(*d, *r.measuredAt, deref(r.measuredDeviceID))
		measured = &m
	}
	var to *time.Time
	if r.effectiveTo != nil {
		t := r.effectiveTo.UTC()
		to = &t
	}
	return product.RehydratePackLevelDef(spec, measured, r.effectiveFrom.UTC(), to)
}

// mustStoredGTIN parses a GTIN read from the database. The column's CHECK and
// the domain wrote it, so a failure is corruption, not input.
func mustStoredGTIN(s string) gs1.GTIN {
	g, err := gs1.ParseGTIN(s)
	if err != nil {
		panic(fmt.Sprintf("product_pack_levels holds an invalid GTIN %q: %v", s, err))
	}
	return g
}

func samePtr[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

// same reports whether two rows of one (sku, gtin) hold the same values.
func (r *packLevelRow) same(o *packLevelRow) bool {
	for i := range r.declared {
		if !samePtr(r.declared[i], o.declared[i]) || !samePtr(r.measured[i], o.measured[i]) {
			return false
		}
	}
	return r.level == o.level && samePtr(r.childGTIN, o.childGTIN) && samePtr(r.qtyOfChild, o.qtyOfChild) &&
		r.unitsPerPack == o.unitsPerPack && sameTime(r.measuredAt, o.measuredAt) && samePtr(r.measuredDeviceID, o.measuredDeviceID) &&
		r.isOrderable == o.isOrderable && r.isDespatchUnit == o.isDespatchUnit &&
		r.effectiveFrom.Equal(o.effectiveFrom) && sameTime(r.effectiveTo, o.effectiveTo)
}

// loadPackLevels returns the pack level rows of skus, grouped by SKU, oldest
// declaration first within a level.
func loadPackLevels(ctx context.Context, q querier, skus []string) (map[string][]packLevelRow, error) {
	out := map[string][]packLevelRow{}
	if len(skus) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `SELECT `+packLevelColumns+` FROM product_pack_levels WHERE sku = ANY($1) ORDER BY sku, effective_from, gtin`, skus)
	if err != nil {
		return nil, fmt.Errorf("load pack levels: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var row packLevelRow
		if err := rows.Scan(row.targets()...); err != nil {
			return nil, fmt.Errorf("scan pack level: %w", err)
		}
		out[row.sku] = append(out[row.sku], row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load pack levels: %w", err)
	}
	return out, nil
}

func hierarchyOf(rows []packLevelRow) product.PackagingHierarchy {
	defs := make([]product.PackLevelDef, len(rows))
	for i := range rows {
		defs[i] = rows[i].toDef()
	}
	return product.RehydratePackagingHierarchy(defs)
}

// savePackLevels writes the changed levels of p inside the surrounding
// transaction: end-dated and edited rows first (so the one-active-level-per-
// level index never sees two), then new rows. A GTIN another SKU holds is
// product.ErrGtinAlreadyAssigned; the loser of any other unique race is
// repository.ErrConcurrentModification.
func savePackLevels(ctx context.Context, q querier, p *product.Product) error {
	defs := p.PackLevels().Levels()
	if len(defs) == 0 {
		return nil
	}
	sku := string(p.SKU())
	stored, err := loadPackLevels(ctx, q, []string{sku})
	if err != nil {
		return err
	}
	byGTIN := make(map[string]*packLevelRow, len(stored[sku]))
	for i := range stored[sku] {
		byGTIN[stored[sku][i].gtin] = &stored[sku][i]
	}
	var inserts []packLevelRow
	for _, d := range defs {
		row := rowOfPackLevel(p.SKU(), d)
		cur, exists := byGTIN[row.gtin]
		switch {
		case !exists:
			inserts = append(inserts, row)
		case !cur.same(&row):
			if _, err := q.Exec(ctx, updatePackLevel, row.updateArgs()...); err != nil {
				return packLevelWriteError(sku, row.gtin, err)
			}
		}
	}
	for i := range inserts {
		if _, err := q.Exec(ctx, insertPackLevel, inserts[i].values()...); err != nil {
			return packLevelWriteError(sku, inserts[i].gtin, err)
		}
	}
	return nil
}

func packLevelWriteError(sku, gtin string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == sqlStateUniqueViolation {
		if pgErr.ConstraintName == gtinUniqueIndex {
			return fmt.Errorf("%w: %s", product.ErrGtinAlreadyAssigned, gtin)
		}
		return repository.ErrConcurrentModification
	}
	return fmt.Errorf("save pack level %s of %s: %w", gtin, sku, err)
}

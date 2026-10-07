package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/product-master/internal/application/ports"
	"github.com/claudioed/product-master/internal/application/repository"
	"github.com/claudioed/product-master/internal/domain/product"
)

// ProductRepo is the pgx-backed ports.ProductRepository over the products
// table. Save is version-guarded: an insert never overwrites an existing SKU
// and an update only applies when the stored version is the loaded one.
type ProductRepo struct {
	pool *pgxpool.Pool
}

// NewProductRepo constructs a ProductRepo over pool.
func NewProductRepo(pool *pgxpool.Pool) *ProductRepo { return &ProductRepo{pool: pool} }

var _ ports.ProductRepository = (*ProductRepo)(nil)

const productColumns = `sku, description, version, handling_tags, temperature_class, dot_hazard_class,
	classification_source, declared_length_mm, declared_width_mm, declared_height_mm, declared_weight_g,
	measured_length_mm, measured_width_mm, measured_height_mm, measured_weight_g, measured_at, measured_device_id`

// productRow is the persisted shape of one product; nullable columns are
// pointers.
type productRow struct {
	sku                  string
	description          string
	version              int64
	handlingTags         []string
	temperatureClass     *string
	dotHazardClass       *int16
	classificationSource *string
	declared             [4]*int64
	measured             [4]*int64
	measuredAt           *time.Time
	measuredDeviceID     *string
}

func (r *productRow) targets() []any {
	return []any{
		&r.sku, &r.description, &r.version, &r.handlingTags, &r.temperatureClass, &r.dotHazardClass,
		&r.classificationSource, &r.declared[0], &r.declared[1], &r.declared[2], &r.declared[3],
		&r.measured[0], &r.measured[1], &r.measured[2], &r.measured[3], &r.measuredAt, &r.measuredDeviceID,
	}
}

func (r *productRow) values() []any {
	return []any{
		r.sku, r.description, r.version, r.handlingTags, r.temperatureClass, r.dotHazardClass,
		r.classificationSource, r.declared[0], r.declared[1], r.declared[2], r.declared[3],
		r.measured[0], r.measured[1], r.measured[2], r.measured[3], r.measuredAt, r.measuredDeviceID,
	}
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func dims(v [4]*int64) *product.UnitDimensions {
	if v[0] == nil || v[1] == nil || v[2] == nil || v[3] == nil {
		return nil
	}
	d := product.RehydrateUnitDimensions(*v[0], *v[1], *v[2], *v[3])
	return &d
}

// toProduct rehydrates the aggregate (no invariant re-check: the row was
// written from a valid aggregate).
func (r *productRow) toProduct() *product.Product {
	var classification *product.Classification
	var source product.ClassificationSource
	if r.handlingTags != nil {
		tags := make([]product.HandlingTag, len(r.handlingTags))
		for i, t := range r.handlingTags {
			tags[i] = product.HandlingTag(t)
		}
		var dot product.DOTHazardClass
		if r.dotHazardClass != nil {
			dot = product.DOTHazardClass(*r.dotHazardClass)
		}
		c := product.RehydrateClassification(tags, product.TemperatureClass(deref(r.temperatureClass)), dot)
		classification, source = &c, product.ClassificationSource(deref(r.classificationSource))
	}
	var measurement *product.Measurement
	if d := dims(r.measured); d != nil && r.measuredAt != nil {
		m := product.RehydrateMeasurement(*d, *r.measuredAt, deref(r.measuredDeviceID))
		measurement = &m
	}
	profile := product.RehydratePhysicalProfile(dims(r.declared), measurement)
	return product.Rehydrate(product.SKU(r.sku), r.description, classification, source, profile, r.version)
}

func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func dimValues(d product.UnitDimensions) [4]*int64 {
	l, w, h, g := d.LengthMm(), d.WidthMm(), d.HeightMm(), d.WeightG()
	return [4]*int64{&l, &w, &h, &g}
}

// rowOf flattens the aggregate into its persisted shape.
func rowOf(p *product.Product) productRow {
	row := productRow{sku: string(p.SKU()), description: p.Description(), version: p.Version()}
	if c, source, err := p.Classification(); err == nil {
		tags := c.Tags()
		row.handlingTags = make([]string, len(tags))
		for i, t := range tags {
			row.handlingTags[i] = string(t)
		}
		row.temperatureClass = optString(string(c.TemperatureClass()))
		if dot := c.DOTHazardClass(); dot != product.NoDOTHazardClass {
			v := int16(dot) // #nosec G115 -- a DOT hazard class is 1..9
			row.dotHazardClass = &v
		}
		row.classificationSource = optString(string(source))
	}
	profile := p.PhysicalProfile()
	if d, ok := profile.Declared(); ok {
		row.declared = dimValues(d)
	}
	if m, ok := profile.Measured(); ok {
		row.measured = dimValues(m.Dimensions())
		at := m.MeasuredAt()
		row.measuredAt = &at
		row.measuredDeviceID = optString(m.DeviceID())
	}
	return row
}

// Get implements ports.ProductRepository.
func (r *ProductRepo) Get(ctx context.Context, sku product.SKU) (*product.Product, error) {
	var row productRow
	err := queryFor(ctx, r.pool).QueryRow(ctx, `SELECT `+productColumns+` FROM products WHERE sku = $1`, string(sku)).Scan(row.targets()...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, repository.ErrProductNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get product %s: %w", sku, err)
	}
	return row.toProduct(), nil
}

const insertProduct = `INSERT INTO products (` + productColumns + `)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
	ON CONFLICT (sku) DO NOTHING`

const updateProduct = `UPDATE products SET
	description = $2, version = $3, handling_tags = $4, temperature_class = $5, dot_hazard_class = $6,
	classification_source = $7, declared_length_mm = $8, declared_width_mm = $9, declared_height_mm = $10,
	declared_weight_g = $11, measured_length_mm = $12, measured_width_mm = $13, measured_height_mm = $14,
	measured_weight_g = $15, measured_at = $16, measured_device_id = $17, updated_at = now()
	WHERE sku = $1 AND version = $18`

// Save implements ports.ProductRepository: loadedVersion 0 inserts (an
// existing SKU affects no row), anything else updates only the row whose
// stored version is still loadedVersion. No affected row means another
// writer won: repository.ErrConcurrentModification.
func (r *ProductRepo) Save(ctx context.Context, p *product.Product, loadedVersion int64) error {
	row := rowOf(p)
	sql, args := insertProduct, row.values()
	if loadedVersion != 0 {
		sql, args = updateProduct, append(args, loadedVersion)
	}
	tag, err := queryFor(ctx, r.pool).Exec(ctx, sql, args...)
	if err != nil {
		return fmt.Errorf("save product %s: %w", p.SKU(), err)
	}
	if tag.RowsAffected() != 1 {
		return repository.ErrConcurrentModification
	}
	return nil
}

// List implements ports.ProductRepository: ascending SKU (byte order, the
// column is COLLATE "C"), strictly after afterSKU.
func (r *ProductRepo) List(ctx context.Context, filter repository.ListFilter, afterSKU product.SKU, limit int) ([]*product.Product, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, `SELECT `+productColumns+` FROM products
		WHERE sku > $1
		  AND ($2 = '' OR $2 = ANY(handling_tags))
		  AND ($3::boolean IS NULL OR (handling_tags IS NOT NULL) = $3::boolean)
		ORDER BY sku
		LIMIT $4`, string(afterSKU), string(filter.HandlingTag), filter.Classified, limit)
	if err != nil {
		return nil, fmt.Errorf("list products: %w", err)
	}
	defer rows.Close()
	out := make([]*product.Product, 0, limit)
	for rows.Next() {
		var row productRow
		if err := rows.Scan(row.targets()...); err != nil {
			return nil, fmt.Errorf("scan product: %w", err)
		}
		out = append(out, row.toProduct())
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list products: %w", err)
	}
	return out, nil
}

package analyticsstore

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/product-master/internal/analytics/report"
)

// Reader is the Postgres READER (report.Reader). Days are UTC calendar days;
// every window is half-open [lo, hi). SQL only counts; ratios and range rules
// live in internal/analytics/report.
type Reader struct {
	pool *pgxpool.Pool
}

// NewReader constructs a Reader over the (read-only) reports pool.
func NewReader(pool *pgxpool.Pool) *Reader { return &Reader{pool: pool} }

var _ report.Reader = (*Reader)(nil)

// qualityDaysSQL builds the same windows as report.DayWindows: one per UTC
// day intersecting [$1, $2), clipped to the range. The day series runs over
// UTC-naive timestamps so a session time zone with DST cannot shift a day.
// open_discrepancies is the latest profile state (highest version) of each
// SKU among the states that occurred before the window's end.
const qualityDaysSQL = `
	WITH days AS (
		SELECT d::date AS day,
		       GREATEST(d AT TIME ZONE 'UTC', $1::timestamptz)                      AS lo,
		       LEAST((d + interval '1 day') AT TIME ZONE 'UTC', $2::timestamptz)   AS hi
		FROM generate_series(
		         date_trunc('day', $1::timestamptz AT TIME ZONE 'UTC'),
		         ($2::timestamptz - interval '1 microsecond') AT TIME ZONE 'UTC',
		         interval '1 day') AS d
		WHERE $1::timestamptz < $2::timestamptz
	)
	SELECT days.day,
	       (SELECT count(*) FROM product_facts f WHERE f.registered_at       >= days.lo AND f.registered_at       < days.hi),
	       (SELECT count(*) FROM product_facts f WHERE f.first_classified_at >= days.lo AND f.first_classified_at < days.hi),
	       (SELECT count(*) FROM product_facts f WHERE f.first_declared_at   >= days.lo AND f.first_declared_at   < days.hi),
	       (SELECT count(*) FROM product_facts f WHERE f.first_measured_at   >= days.lo AND f.first_measured_at   < days.hi),
	       (SELECT count(*) FROM (
	            SELECT DISTINCT ON (s.sku) s.discrepancy
	            FROM profile_states s
	            WHERE s.occurred_at < days.hi
	            ORDER BY s.sku, s.version DESC
	        ) latest WHERE latest.discrepancy)
	FROM days
	ORDER BY days.day`

// QualityDays implements report.Reader.
func (r *Reader) QualityDays(ctx context.Context, rg report.Range) ([]report.QualityDay, error) {
	rows, err := r.pool.Query(ctx, qualityDaysSQL, rg.From.UTC(), rg.To.UTC())
	if err != nil {
		return nil, fmt.Errorf("analyticsstore: quality days: %w", err)
	}
	defer rows.Close()
	out := []report.QualityDay{}
	for rows.Next() {
		var d report.QualityDay
		if err := rows.Scan(&d.Day, &d.Registered, &d.Classified, &d.DimensionsDeclared, &d.Measured, &d.OpenDiscrepancies); err != nil {
			return nil, fmt.Errorf("analyticsstore: scan quality day: %w", err)
		}
		y, m, dd := d.Day.Date()
		d.Day = time.Date(y, m, dd, 0, 0, 0, 0, time.UTC)
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("analyticsstore: quality days rows: %w", err)
	}
	return out, nil
}

const coverageSQL = `
	SELECT count(*),
	       count(first_classified_at),
	       count(*) FILTER (WHERE has_declared),
	       count(*) FILTER (WHERE has_measured),
	       count(*) FILTER (WHERE discrepancy)
	FROM product_facts`

// Coverage implements report.Reader.
func (r *Reader) Coverage(ctx context.Context) (report.CoverageCounts, error) {
	var c report.CoverageCounts
	if err := r.pool.QueryRow(ctx, coverageSQL).Scan(&c.Products, &c.Classified, &c.DimensionsDeclared, &c.Measured, &c.OpenDiscrepancies); err != nil {
		return report.CoverageCounts{}, fmt.Errorf("analyticsstore: coverage: %w", err)
	}
	return c, nil
}

// LastEventAt implements report.Reader: the newest CloudEvents time among the
// applied events, nil while none was applied.
func (r *Reader) LastEventAt(ctx context.Context) (*time.Time, error) {
	var at *time.Time
	if err := r.pool.QueryRow(ctx, `SELECT max(occurred_at) FROM analytics_processed_events`).Scan(&at); err != nil {
		return nil, fmt.Errorf("analyticsstore: last event: %w", err)
	}
	if at != nil {
		utc := at.UTC()
		at = &utc
	}
	return at, nil
}

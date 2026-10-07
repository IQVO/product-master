package analyticsstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/product-master/internal/analytics/report"
)

// Projection is the Postgres WRITER (report.Projection). Apply claims the
// event id and folds the event into product_facts / profile_states in ONE
// transaction: the id is recorded if and only if its effect is, so a failure
// anywhere leaves nothing behind and the redelivered event is applied afresh,
// while a replay of an applied id is a no-op.
type Projection struct {
	pool *pgxpool.Pool
}

// NewProjection constructs a Projection over the writer pool.
func NewProjection(pool *pgxpool.Pool) *Projection { return &Projection{pool: pool} }

var _ report.Projection = (*Projection)(nil)

const claimSQL = `
	INSERT INTO analytics_processed_events (event_id, event_type, occurred_at)
	VALUES ($1, $2, $3)
	ON CONFLICT (event_id) DO NOTHING`

// upsertFactsSQL makes the SKU known and keeps the EARLIEST candidate of each
// milestone: LEAST ignores NULLs, so a NULL parameter (the event does not mark
// that milestone) keeps the stored value, and the row converges whatever
// order the events arrive in.
const upsertFactsSQL = `
	INSERT INTO product_facts (sku, registered_at, first_classified_at, first_declared_at, first_measured_at)
	VALUES ($1, $2::timestamptz, $3::timestamptz, $4::timestamptz, $5::timestamptz)
	ON CONFLICT (sku) DO UPDATE SET
		registered_at       = LEAST(product_facts.registered_at, EXCLUDED.registered_at),
		first_classified_at = LEAST(product_facts.first_classified_at, EXCLUDED.first_classified_at),
		first_declared_at   = LEAST(product_facts.first_declared_at, EXCLUDED.first_declared_at),
		first_measured_at   = LEAST(product_facts.first_measured_at, EXCLUDED.first_measured_at)`

// insertProfileStateSQL records the profile history row of one version.
const insertProfileStateSQL = `
	INSERT INTO profile_states (sku, version, occurred_at, has_declared, has_measured, discrepancy)
	VALUES ($1, $2, $3, $4, $5, $6)
	ON CONFLICT (sku, version) DO NOTHING`

// updateCurrentProfileSQL overwrites the current profile only with a greater
// aggregate version (the ADR 0004 consumer rule), so a late, older profile
// event never rolls the current state back.
const updateCurrentProfileSQL = `
	UPDATE product_facts
	SET profile_version = $2, has_declared = $3, has_measured = $4, discrepancy = $5
	WHERE sku = $1 AND profile_version < $2`

// Apply implements report.Projection.
func (p *Projection) Apply(ctx context.Context, e report.ProductEvent) (bool, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("analyticsstore: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once committed

	tag, err := tx.Exec(ctx, claimSQL, e.EventID, string(e.Kind), e.At.UTC())
	if err != nil {
		return false, classify(fmt.Errorf("analyticsstore: claim event %s: %w", e.EventID, err))
	}
	if tag.RowsAffected() == 0 {
		return false, nil // already applied: the deferred rollback ends the empty tx
	}
	f := report.FirstsOf(e)
	if _, err := tx.Exec(ctx, upsertFactsSQL, e.SKU, f.Registered, f.Classified, f.DimensionsDeclared, f.Measured); err != nil {
		return false, classify(fmt.Errorf("analyticsstore: project %s %s: %w", e.Kind, e.SKU, err))
	}
	if s := e.Profile; s != nil {
		if _, err := tx.Exec(ctx, insertProfileStateSQL, e.SKU, e.Version, e.At.UTC(), s.HasDeclared, s.HasMeasured, s.Discrepancy); err != nil {
			return false, classify(fmt.Errorf("analyticsstore: profile state %s v%d: %w", e.SKU, e.Version, err))
		}
		if _, err := tx.Exec(ctx, updateCurrentProfileSQL, e.SKU, e.Version, s.HasDeclared, s.HasMeasured, s.Discrepancy); err != nil {
			return false, classify(fmt.Errorf("analyticsstore: current profile %s v%d: %w", e.SKU, e.Version, err))
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("analyticsstore: commit: %w", err)
	}
	return true, nil
}

// classify wraps a Postgres data-exception (SQLSTATE class 22) or
// integrity-violation (class 23) error in report.ErrRejected: the same event
// can never succeed, so the consumer dead-letters it instead of retrying
// forever. Everything else (connection loss, timeouts, locks, failovers)
// stays transient.
func classify(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && len(pgErr.Code) >= 2 && (pgErr.Code[:2] == "22" || pgErr.Code[:2] == "23") {
		return fmt.Errorf("%w: %w", report.ErrRejected, err)
	}
	return err
}

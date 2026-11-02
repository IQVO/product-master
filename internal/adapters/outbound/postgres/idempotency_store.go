package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/product-master/internal/adapters/outbound/postgres/pgtx"
	"github.com/claudioed/product-master/internal/application/idempotency"
)

// idempotencyRetention is how long a key row is kept; fresh-key requests
// prune older rows (the guideline asks for at least 24 hours).
const idempotencyRetention = "7 days"

// IdempotencyStore is the Postgres ports.IdempotencyStore. The key row, the
// aggregate change and the outbox rows commit in ONE transaction: Execute
// begins it, hands run a ctx carrying it (pgtx) and commits only after the
// stored answer is written. Concurrent requests with one key race on
// INSERT ... ON CONFLICT DO NOTHING; the loser blocks on the unique index
// until the winner resolves, then replays the winner's committed answer (or,
// when the winner rolled back, runs itself).
type IdempotencyStore struct {
	pool *pgxpool.Pool
}

// NewIdempotencyStore constructs the store over pool.
func NewIdempotencyStore(pool *pgxpool.Pool) *IdempotencyStore {
	return &IdempotencyStore{pool: pool}
}

// Execute implements ports.IdempotencyStore.
func (s *IdempotencyStore) Execute(ctx context.Context, key, fingerprint string,
	run func(ctx context.Context) (idempotency.StoredResponse, error),
) (idempotency.StoredResponse, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return idempotency.StoredResponse{}, false, fmt.Errorf("begin idempotent request: %w", err)
	}
	// A rollback after Commit is a no-op; it also covers a panic in run.
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if _, err := tx.Exec(ctx, `DELETE FROM idempotency_keys WHERE created_at < now() - interval '`+idempotencyRetention+`'`); err != nil {
		return idempotency.StoredResponse{}, false, fmt.Errorf("prune idempotency keys: %w", err)
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO idempotency_keys (key, fingerprint, status_code, response_body, response_headers)
		VALUES ($1, $2, 0, ''::bytea, '{}'::jsonb)
		ON CONFLICT (key) DO NOTHING`, key, fingerprint)
	if err != nil {
		return idempotency.StoredResponse{}, false, fmt.Errorf("insert idempotency key: %w", err)
	}
	if tag.RowsAffected() == 0 {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return s.replay(ctx, key, fingerprint)
	}

	resp, err := run(pgtx.With(ctx, tx))
	if err != nil {
		return idempotency.StoredResponse{}, false, err
	}
	headers, err := json.Marshal(resp.Header)
	if err != nil {
		return idempotency.StoredResponse{}, false, fmt.Errorf("encode stored headers: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE idempotency_keys SET status_code = $1, response_body = $2, response_headers = $3
		WHERE key = $4`, resp.Status, resp.Body, headers, key); err != nil {
		return idempotency.StoredResponse{}, false, fmt.Errorf("store idempotent response: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return idempotency.StoredResponse{}, false, fmt.Errorf("commit idempotent request: %w", err)
	}
	return resp, false, nil
}

// replay reads the committed row of an already-used key.
func (s *IdempotencyStore) replay(ctx context.Context, key, fingerprint string) (idempotency.StoredResponse, bool, error) {
	var (
		stored  string
		resp    idempotency.StoredResponse
		headers []byte
	)
	err := s.pool.QueryRow(ctx, `
		SELECT fingerprint, status_code, response_body, response_headers
		FROM idempotency_keys WHERE key = $1`, key).Scan(&stored, &resp.Status, &resp.Body, &headers)
	if errors.Is(err, pgx.ErrNoRows) {
		// Pruned between the conflict and this read (older than the
		// retention): the caller's retry is then a fresh key.
		return idempotency.StoredResponse{}, false, fmt.Errorf("idempotency key %q expired during replay", key)
	}
	if err != nil {
		return idempotency.StoredResponse{}, false, fmt.Errorf("read idempotency key: %w", err)
	}
	if stored != fingerprint {
		return idempotency.StoredResponse{}, false, idempotency.ErrKeyReused
	}
	if err := json.Unmarshal(headers, &resp.Header); err != nil {
		return idempotency.StoredResponse{}, false, fmt.Errorf("decode stored headers: %w", err)
	}
	return resp, true, nil
}

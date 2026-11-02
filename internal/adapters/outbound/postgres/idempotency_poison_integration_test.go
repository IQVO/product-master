//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/claudioed/product-master/internal/adapters/outbound/postgres"
	"github.com/claudioed/product-master/internal/adapters/outbound/postgres/pgtx"
	"github.com/claudioed/product-master/internal/application/idempotency"
)

// poisonTx simulates what a mapped domain conflict does to the store's
// transaction: a statement fails on the server (a unique violation, like a
// lost race on a unique index), the repository maps it to a typed error and
// the handler answers 4xx, but the transaction is ABORTED from then on.
func poisonTx(t *testing.T, ctx context.Context) {
	t.Helper()
	tx, ok := pgtx.From(ctx)
	if !ok {
		t.Fatal("the store did not bind a transaction into the run context")
	}
	for i := 0; i < 2; i++ {
		if _, err := tx.Exec(ctx, `INSERT INTO idempotency_keys (key, fingerprint, status_code, response_body, response_headers) VALUES ('dup', 'f', 1, ''::bytea, '{}'::jsonb)`); err != nil {
			return // the second insert aborts the transaction (23505)
		}
	}
	t.Fatal("the duplicate insert did not abort the transaction")
}

// A mapped 4xx over an aborted transaction is still recorded and returned,
// and its replay never re-runs the handler.
func TestIdempotency_A409FromAnAbortedTransactionCommitsTheOutcome(t *testing.T) {
	ctx := context.Background()
	pool := startPostgresPool(t)
	store := postgres.NewIdempotencyStore(pool)
	var calls int
	run := func(ctx context.Context) (idempotency.StoredResponse, error) {
		calls++
		poisonTx(t, ctx)
		return idempotency.StoredResponse{
			Status: http.StatusConflict,
			Header: map[string][]string{"Content-Type": {"application/problem+json"}},
			Body:   []byte(`{"status":409}`),
		}, nil
	}

	first, replayed, err := store.Execute(ctx, "k-poison", "fp", run)
	if err != nil || replayed || first.Status != http.StatusConflict {
		t.Fatalf("first = %+v replayed=%v err=%v, want the truthful 409", first, replayed, err)
	}
	var status int
	if err := pool.QueryRow(ctx, `SELECT status_code FROM idempotency_keys WHERE key = 'k-poison'`).Scan(&status); err != nil || status != http.StatusConflict {
		t.Fatalf("the 409 outcome was not committed: status=%d err=%v", status, err)
	}
	var dup int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM idempotency_keys WHERE key = 'dup'`).Scan(&dup); err != nil || dup != 0 {
		t.Fatalf("the handler's partial writes survived: %d rows, err=%v", dup, err)
	}

	again, replayed, err := store.Execute(ctx, "k-poison", "fp", run)
	if err != nil || !replayed || again.Status != http.StatusConflict || string(again.Body) != string(first.Body) {
		t.Fatalf("replay = %+v replayed=%v err=%v, want the cached 409", again, replayed, err)
	}
	if calls != 1 {
		t.Fatalf("handler ran %d times, want 1 (replay must not re-run it)", calls)
	}
}

// A 2xx over an aborted transaction is a lie: never returned, never cached,
// and the same key retries the real work.
func TestIdempotency_A2xxFromAnAbortedTransactionIsNeverCached(t *testing.T) {
	ctx := context.Background()
	pool := startPostgresPool(t)
	store := postgres.NewIdempotencyStore(pool)
	var calls int
	run := func(ctx context.Context) (idempotency.StoredResponse, error) {
		calls++
		if calls == 1 {
			poisonTx(t, ctx) // only the first attempt dies inside the transaction
		}
		return idempotency.StoredResponse{Status: http.StatusCreated, Body: []byte(`{}`), Header: map[string][]string{}}, nil
	}

	if _, _, err := store.Execute(ctx, "k-lie", "fp", run); err == nil || errors.Is(err, idempotency.ErrNotStored) {
		t.Fatalf("first err = %v, want a plain failure (the writes never happened)", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM idempotency_keys WHERE key = 'k-lie'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("a failed success left %d idempotency rows (err=%v): the key could not retry", n, err)
	}
	second, replayed, err := store.Execute(ctx, "k-lie", "fp", run)
	if err != nil || replayed || second.Status != http.StatusCreated {
		t.Fatalf("retry = %+v replayed=%v err=%v, want the real work to succeed", second, replayed, err)
	}
	if calls != 2 {
		t.Fatalf("handler ran %d times, want 2", calls)
	}
}

//go:build integration

// Rendered from warehouse-harness-template
// templates/idempotency/replay_classes_wiring_test.go.tmpl: the repo-specific
// half of the idempotency replay-class test, and the ONLY file of the pair
// that names a driver. The store is the real Postgres one behind
// ports.IdempotencyStore (postgres.IdempotencyStore): the key row, the claim,
// the handler's savepoint and the stored answer all live in ONE transaction
// (ADR 0015), so Releasing a RETRYABLE key is the whole-transaction rollback
// of the 5xx branch, and KeyRows/Committed are real proofs. The middleware
// and the error writer come from the production package through export_test.go.
package http_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	inboundhttp "github.com/claudioed/product-master/internal/adapters/inbound/http"
	"github.com/claudioed/product-master/internal/adapters/outbound/postgres"
	"github.com/claudioed/product-master/internal/adapters/outbound/postgres/pgtx"
)

// newReplayOps returns the six operations over ONE clean store: a private
// clone of the migrated template database (the shared container of this
// package's TestMain), its idempotency_keys empty and the replay_probe
// scratch table truncated.
func newReplayOps(t *testing.T) replayOps {
	t.Helper()
	pool, err := postgres.NewPool(context.Background(), restMigratedDB(t))
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(context.Background(), `CREATE TABLE IF NOT EXISTS replay_probe (tag text NOT NULL)`); err != nil {
		t.Fatalf("create replay_probe: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `TRUNCATE replay_probe`); err != nil {
		t.Fatalf("truncate replay_probe: %v", err)
	}
	count := func(t *testing.T, query string, args ...any) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	errs := map[string]error{}
	for _, row := range inboundhttp.Catalogue() {
		if _, seen := errs[row.Slug]; !seen {
			errs[row.Slug] = row.Err
		}
	}
	return replayOps{
		Catalogue: func() []problemEntry {
			out := make([]problemEntry, 0, len(inboundhttp.Catalogue()))
			for _, row := range inboundhttp.Catalogue() {
				out = append(out, problemEntry{Slug: row.Slug, Status: row.Status, Class: row.Class})
			}
			return out
		},
		Middleware: func(_ *testing.T, next http.Handler) http.Handler {
			return inboundhttp.RequireIdempotencyKey(postgres.NewIdempotencyStore(pool))(next)
		},
		Refuse: func(w http.ResponseWriter, r *http.Request, e problemEntry) {
			err, ok := errs[e.Slug]
			if !ok {
				panic(fmt.Sprintf("no error maps to problem %q", e.Slug))
			}
			inboundhttp.WriteError(w, r, fmt.Errorf("%w: replay-class test", err))
		},
		Write: func(ctx context.Context, tag string) error {
			tx, ok := pgtx.From(ctx)
			if !ok {
				return errors.New("the middleware did not bind a transaction")
			}
			_, err := tx.Exec(ctx, `INSERT INTO replay_probe (tag) VALUES ($1)`, tag)
			return err
		},
		Committed: func(t *testing.T, tag string) int {
			return count(t, `SELECT count(*) FROM replay_probe WHERE tag = $1`, tag)
		},
		KeyRows: func(t *testing.T, key string) int {
			return count(t, `SELECT count(*) FROM idempotency_keys WHERE key = $1`, key)
		},
	}
}

//go:build integration

// Real router + real Postgres: the RETRYABLE refusals of the one keyed route
// (POST /products/{sku}/pack-levels/{level}/retirements) are not stored, the
// key is released, and a retry under the SAME key executes again (ADR 0015).
// The optimistic-lock loss is provoked deterministically: a repository
// decorator commits a competing version bump on its own connection before
// the request's first Save, so the request's guarded update affects no row
// (repository.ErrConcurrentModification).
package http_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	inboundhttp "github.com/claudioed/product-master/internal/adapters/inbound/http"
	outboundkafka "github.com/claudioed/product-master/internal/adapters/outbound/kafka"
	"github.com/claudioed/product-master/internal/adapters/outbound/postgres"
	"github.com/claudioed/product-master/internal/application/ports"
	"github.com/claudioed/product-master/internal/application/usecases"
	"github.com/claudioed/product-master/internal/domain/product"
)

// competingWriter wraps the product repository: the FIRST Save of a test
// first commits a version bump of bumpSKU on the pool (a connection outside
// the request's transaction), then delegates. The request's version-guarded
// update then matches no row and Save answers ErrConcurrentModification.
type competingWriter struct {
	ports.ProductRepository
	pool    *pgxpool.Pool
	bumpSKU string
	mu      sync.Mutex
	armed   bool
}

func (c *competingWriter) Save(ctx context.Context, p *product.Product, loadedVersion int64) error {
	c.mu.Lock()
	fire := c.armed
	c.armed = false
	c.mu.Unlock()
	if fire {
		if _, err := c.pool.Exec(context.WithoutCancel(ctx),
			`UPDATE products SET version = version + 1, updated_at = now() WHERE sku = $1`, c.bumpSKU); err != nil {
			return err
		}
	}
	return c.ProductRepository.Save(ctx, p, loadedVersion)
}

// newRetireServerPool boots the real router over a private migrated clone,
// with the retire use case's product repository wrapped by competingWriter.
func newRetireServerPool(t *testing.T, bumpSKU string) (*httptest.Server, *pgxpool.Pool, *competingWriter) {
	t.Helper()
	pool, err := postgres.NewPool(context.Background(), restMigratedDB(t))
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	compete := &competingWriter{ProductRepository: postgres.NewProductRepo(pool), pool: pool, bumpSKU: bumpSKU, armed: true}
	w := usecases.Writer{
		Products: compete, Outbox: postgres.NewOutboxRepo(pool),
		Encoder: outboundkafka.NewEncoder(), UoW: postgres.NewUnitOfWork(pool), Clock: restClock{restAt},
	}
	s := &inboundhttp.Server{
		RegisterProduct:   &usecases.RegisterProduct{Writer: w},
		ClassifyProduct:   &usecases.ClassifyProduct{Writer: w},
		DeclareDimensions: &usecases.DeclareDimensions{Writer: w},
		RecordMeasurement: &usecases.RecordMeasurement{Writer: w},
		GetProduct:        &usecases.GetProduct{Products: postgres.NewProductRepo(pool)},
		ListProducts:      &usecases.ListProducts{Products: postgres.NewProductRepo(pool)},
		DeclarePackLevel:  &usecases.DeclarePackLevel{Writer: w},
		RetirePackLevel:   &usecases.RetirePackLevel{Writer: w},
		Idempotency:       postgres.NewIdempotencyStore(pool),
		Readiness:         &inboundhttp.Readiness{},
	}
	srv := httptest.NewServer(inboundhttp.NewRouter(s))
	t.Cleanup(srv.Close)
	return srv, pool, compete
}

func keyRows(t *testing.T, pool *pgxpool.Pool, key string) int {
	t.Helper()
	return count(t, pool, `SELECT count(*) FROM idempotency_keys WHERE key = $1`, key)
}

// The loser of an optimistic-lock race: the 409 is RETRYABLE, so its key row
// is absent and its retry under the same key EXECUTES (and is then stored and
// replayed).
func TestREST_ConcurrentModificationRefusalIsNotStoredAndRetriesExecute(t *testing.T) {
	srv, pool, _ := newRetireServerPool(t, "SKU-1")
	seedRetirable(t, srv)
	const key = "k-concurrent"
	path := "/products/SKU-1/pack-levels/EACH/retirements"
	body := `{"reason":"discontinued"}`

	first, firstBody, _ := keyedPost(t, srv, path, body, key)
	if first != http.StatusConflict || !strings.Contains(firstBody, "concurrent-modification") {
		t.Fatalf("first answer = %d %q, want 409 concurrent-modification", first, firstBody)
	}
	if got := keyRows(t, pool, key); got != 0 {
		t.Fatalf("key rows after the lost race = %d, want 0: a RETRYABLE refusal is released", got)
	}

	retry, retryBody, retryHeader := keyedPost(t, srv, path, body, key)
	if retry != http.StatusOK {
		t.Fatalf("retry under the same key = %d %q, want 200: the key was released, it executes", retry, retryBody)
	}
	if replayed := retryHeader.Get("Idempotency-Replayed"); replayed != "" {
		t.Fatalf("the retry carries Idempotency-Replayed %q, want none: it executed", replayed)
	}
	if got := keyRows(t, pool, key); got != 1 {
		t.Fatalf("key rows after the successful retry = %d, want 1: a 2xx is stored", got)
	}

	third, thirdBody, thirdHeader := keyedPost(t, srv, path, body, key)
	if third != http.StatusOK || thirdBody != retryBody || thirdHeader.Get("Idempotency-Replayed") != "true" {
		t.Fatalf("third request = %d %q replayed=%q, want the stored 200 replayed byte for byte",
			third, thirdBody, thirdHeader.Get("Idempotency-Replayed"))
	}
}

// The 404 of a product another request may register later is RETRYABLE too:
// released, retried under the same key once the product exists, then stored.
func TestREST_ProductNotFoundRefusalIsNotStoredAndRetriesExecute(t *testing.T) {
	srv, pool, _ := newRetireServerPool(t, "SKU-404")
	const key = "k-not-found"
	path := "/products/SKU-404/pack-levels/EACH/retirements"
	body := `{"reason":"discontinued"}`

	first, firstBody, _ := keyedPost(t, srv, path, body, key)
	if first != http.StatusNotFound || !strings.Contains(firstBody, "product-not-found") {
		t.Fatalf("first answer = %d %q, want 404 product-not-found", first, firstBody)
	}
	if got := keyRows(t, pool, key); got != 0 {
		t.Fatalf("key rows after the 404 = %d, want 0: a RETRYABLE refusal is released", got)
	}

	seedRetirable(t, srv) // registers SKU-1, not SKU-404: register it by hand
	if st, _, _ := restDo(t, srv, http.MethodPut, "/products/SKU-404", `{}`); st != http.StatusCreated {
		t.Fatalf("register SKU-404 = %d, want 201", st)
	}
	if st, _, _ := restDo(t, srv, http.MethodPut, "/products/SKU-404/pack-levels/EACH", `{"gtin":"14006381333932","isOrderable":true}`); st != http.StatusCreated {
		t.Fatalf("declare EACH of SKU-404 = %d, want 201", st)
	}

	retry, retryBody, retryHeader := keyedPost(t, srv, path, body, key)
	if retry != http.StatusOK {
		t.Fatalf("retry under the same key = %d %q, want 200: the product exists now, the key was free", retry, retryBody)
	}
	if replayed := retryHeader.Get("Idempotency-Replayed"); replayed != "" {
		t.Fatalf("the retry carries Idempotency-Replayed %q, want none: it executed", replayed)
	}
	if got := keyRows(t, pool, key); got != 1 {
		t.Fatalf("key rows after the successful retry = %d, want 1", got)
	}
	if third, thirdBody, thirdHeader := keyedPost(t, srv, path, body, key); third != http.StatusOK ||
		thirdBody != retryBody || thirdHeader.Get("Idempotency-Replayed") != "true" {
		t.Fatalf("third request = %d replayed=%q, want the stored 200 replayed", third, thirdHeader.Get("Idempotency-Replayed"))
	}
}

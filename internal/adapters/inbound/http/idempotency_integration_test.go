//go:build integration

package http_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Real router + real Postgres: the Idempotency-Key row commits in the same
// transaction as the aggregate change and its outbox rows.

func keyedPost(t *testing.T, srv *httptest.Server, path, body, key string) (int, string, http.Header) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw), resp.Header
}

func count(t *testing.T, pool *pgxpool.Pool, sql string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), sql).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func seedRetirable(t *testing.T, srv *httptest.Server) {
	t.Helper()
	if st, _, _ := restDo(t, srv, http.MethodPut, "/products/SKU-1", `{}`); st != http.StatusCreated {
		t.Fatalf("register = %d", st)
	}
	if st, _, _ := restDo(t, srv, http.MethodPut, "/products/SKU-1/pack-levels/EACH", `{"gtin":"04006381333931","isOrderable":true}`); st != http.StatusCreated {
		t.Fatalf("declare = %d", st)
	}
}

func TestREST_RetireIsIdempotentInPostgres(t *testing.T) {
	srv, pool := newRESTServerPool(t)
	seedRetirable(t, srv)
	const path = "/products/SKU-1/pack-levels/EACH/retirements"

	if st, _, _ := keyedPost(t, srv, path, "", ""); st != http.StatusBadRequest {
		t.Fatalf("missing key = %d", st)
	}
	st, first, h := keyedPost(t, srv, path, `{"reason":"gone"}`, "pg-1")
	if st != http.StatusOK || h.Get("Idempotency-Replayed") != "" {
		t.Fatalf("first = %d %q", st, h.Get("Idempotency-Replayed"))
	}
	events := count(t, pool, `SELECT count(*) FROM outbox_events`)
	st, again, h := keyedPost(t, srv, path, `{"reason":"gone"}`, "pg-1")
	if st != http.StatusOK || again != first || h.Get("Idempotency-Replayed") != "true" {
		t.Fatalf("replay = %d %q %s", st, h.Get("Idempotency-Replayed"), again)
	}
	if got := count(t, pool, `SELECT count(*) FROM outbox_events`); got != events {
		t.Fatalf("replay wrote outbox rows: %d -> %d", events, got)
	}
	if st, body, _ := keyedPost(t, srv, path, `{"reason":"other"}`, "pg-1"); st != http.StatusConflict || !strings.Contains(body, "idempotency-key-reused") {
		t.Fatalf("reuse = %d %s", st, body)
	}
	if n := count(t, pool, `SELECT count(*) FROM idempotency_keys`); n != 1 {
		t.Fatalf("idempotency_keys = %d, want 1", n)
	}
}

func TestREST_RetireCachesA4xxAndConcurrentSameKeyRunsOnce(t *testing.T) {
	srv, pool := newRESTServerPool(t)
	seedRetirable(t, srv)

	_, a, _ := keyedPost(t, srv, "/products/SKU-1/pack-levels/PALLET/retirements", "", "pg-404")
	st, b, h := keyedPost(t, srv, "/products/SKU-1/pack-levels/PALLET/retirements", "", "pg-404")
	if st != http.StatusNotFound || a != b || h.Get("Idempotency-Replayed") != "true" {
		t.Fatalf("4xx replay = %d %q", st, h.Get("Idempotency-Replayed"))
	}

	var wg sync.WaitGroup
	fresh := make(chan int, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st, _, h := keyedPost(t, srv, "/products/SKU-1/pack-levels/EACH/retirements", `{"reason":"x"}`, "pg-conc")
			if st != http.StatusOK {
				t.Errorf("status = %d", st)
			}
			if h.Get("Idempotency-Replayed") == "" {
				fresh <- 1
			}
		}()
	}
	wg.Wait()
	close(fresh)
	if len(fresh) != 1 {
		t.Fatalf("%d executions, want 1", len(fresh))
	}
	if n := count(t, pool, `SELECT count(*) FROM idempotency_keys WHERE key = 'pg-conc'`); n != 1 {
		t.Fatalf("rows = %d", n)
	}
}

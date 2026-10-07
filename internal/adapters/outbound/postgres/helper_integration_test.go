//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/claudioed/product-master/internal/adapters/outbound/postgres"
)

// startPostgresPool boots a real Postgres via testcontainers (never a
// skip-gated external database), applies the embedded migrations and
// returns an open pool. Each test gets its own container: the tests assert
// on whole-table contents.
func startPostgresPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("product_master_test"),
		tcpostgres.WithUsername("product_master_test"),
		tcpostgres.WithPassword("product_master_test"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	url, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	if err := postgres.RunMigrations(url); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	// Running them again is a no-op (boot runs them every start).
	if err := postgres.RunMigrations(url); err != nil {
		t.Fatalf("second migration run: %v", err)
	}
	pool, err := postgres.NewPool(ctx, url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

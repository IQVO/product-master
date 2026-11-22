// Package pgtest boots ONE real Postgres through testcontainers per test
// binary and hands every test its own EMPTY database (no migrations applied:
// cmd/product-projector runs the embedded analytical migrations itself, and a
// seeded reports database is migrated through analyticsstore.RunMigrations).
// It never reads DATABASE_URL or ANALYTICS_DATABASE_URL and never skips: a
// missing Docker is a test failure (fleet rule,
// TestPostgresIntegrationTestsUseTestcontainers).
//
// Minimal port of customer-ordering's internal/testing/pgtest (the
// EmptyURL/Shutdown subset), test-support code only.
package pgtest

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	user     = "product_master_itest"
	password = "product_master_itest"
	adminDB  = "postgres"
)

var (
	once      sync.Once
	container *tcpostgres.PostgresContainer
	errStart  error
	seq       atomic.Int64
)

func start() {
	ctx := context.Background()
	container, errStart = tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase(adminDB),
		tcpostgres.WithUsername(user),
		tcpostgres.WithPassword(password),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(90*time.Second)),
	)
}

// databaseURL rewrites the database segment of the container's DSN, keeping
// the credentials and the mapped port intact.
func databaseURL(ctx context.Context, db string) (string, error) {
	base, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return "", err
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	u.Path = "/" + db
	return u.String(), nil
}

func adminExec(ctx context.Context, sql string) error {
	dsn, err := databaseURL(ctx, adminDB)
	if err != nil {
		return err
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(ctx) }()
	_, err = conn.Exec(ctx, sql)
	return err
}

// EmptyURL returns the DSN of a fresh database WITHOUT migrations, for tests
// that boot a binary migrating the database itself or apply their own
// migrations before seeding.
func EmptyURL(t *testing.T) string {
	t.Helper()
	once.Do(start)
	if errStart != nil {
		t.Fatalf("start postgres container: %v", errStart)
	}
	ctx := context.Background()
	name := fmt.Sprintf("e_%d", seq.Add(1))
	if err := adminExec(ctx, `CREATE DATABASE `+name); err != nil {
		t.Fatalf("create database %s: %v", name, err)
	}
	dsn, err := databaseURL(ctx, name)
	if err != nil {
		t.Fatalf("database url: %v", err)
	}
	return dsn
}

// Shutdown terminates the shared container; call it from TestMain.
func Shutdown() {
	if container != nil {
		_ = testcontainers.TerminateContainer(container)
	}
}

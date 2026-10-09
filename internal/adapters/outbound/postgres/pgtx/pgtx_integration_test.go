//go:build integration

// Integration tests for the pgtx transaction-context mechanism against a
// REAL Postgres (testcontainers): pgtx.With/From are what let the Postgres
// repositories transparently participate in a ports.UnitOfWork, and these
// tests prove the two properties that make the transactional outbox safe
// against real infrastructure:
//
//   - a repo write (save + outbox enqueue) inside postgres.UnitOfWork
//     commits ATOMICALLY — both land or neither does; and
//   - an error inside the scope rolls EVERYTHING back — no product row, no
//     outbox row, no trace.
//
// One container per package run (TestMain), migrated once into a template
// database, one private clone per test. Never an external DATABASE_URL,
// never t.Skip.
package pgtx_test

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	outboundkafka "github.com/claudioed/product-master/internal/adapters/outbound/kafka"
	"github.com/claudioed/product-master/internal/adapters/outbound/postgres"
	"github.com/claudioed/product-master/internal/adapters/outbound/postgres/pgtx"
	"github.com/claudioed/product-master/internal/application/outbox"
	"github.com/claudioed/product-master/internal/application/repository"
	"github.com/claudioed/product-master/internal/application/usecases"
	"github.com/claudioed/product-master/internal/domain/product"
)

const pgtxTemplateDB = "pgtx_migrated_template"

var (
	pgtxBaseURL string
	pgtxDBSeq   atomic.Uint64
)

func TestMain(m *testing.M) {
	os.Exit(runPgtxTests(m))
}

func runPgtxTests(m *testing.M) int {
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("product_master_pgtx"),
		tcpostgres.WithUsername("pgtx"),
		tcpostgres.WithPassword("pgtx"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(90*time.Second),
		),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "start postgres container: %v\n", err)
		return 1
	}
	defer func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			fmt.Fprintf(os.Stderr, "terminate postgres container: %v\n", err)
		}
	}()

	pgtxBaseURL, err = container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "postgres connection string: %v\n", err)
		return 1
	}
	if err := pgtxCreateDatabase(ctx, pgtxTemplateDB); err != nil {
		fmt.Fprintf(os.Stderr, "create template database: %v\n", err)
		return 1
	}
	u, err := url.Parse(pgtxBaseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse base url: %v\n", err)
		return 1
	}
	u.Path = "/" + pgtxTemplateDB
	if err := postgres.RunMigrations(u.String()); err != nil {
		fmt.Fprintf(os.Stderr, "migrate template: %v\n", err)
		return 1
	}
	return m.Run()
}

// pgtxCreateDatabase creates an empty database inside the shared container.
func pgtxCreateDatabase(ctx context.Context, name string) error {
	conn, err := pgx.Connect(ctx, pgtxBaseURL)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, fmt.Sprintf("CREATE DATABASE %q", name)); err != nil {
		return fmt.Errorf("create database %s: %w", name, err)
	}
	return nil
}

// pgtxMigratedDB hands the test a connection URL to its own private
// database cloned from the migrated template.
func pgtxMigratedDB(t *testing.T) string {
	t.Helper()
	name := fmt.Sprintf("pgtx_%d", pgtxDBSeq.Add(1))
	conn, err := pgx.Connect(context.Background(), pgtxBaseURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(context.Background(), fmt.Sprintf(
		"CREATE DATABASE %q WITH TEMPLATE %q", name, pgtxTemplateDB)); err != nil {
		t.Fatalf("clone database: %v", err)
	}
	u, err := url.Parse(pgtxBaseURL)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	return u.String()
}

// pgtxCounts reads the products and outbox_events row counts straight from
// the private database, outside any pool the code under test holds.
func pgtxCounts(t *testing.T, dbURL string) (products, outbox int) {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(context.Background())
	if err := conn.QueryRow(context.Background(), `SELECT count(*) FROM products`).Scan(&products); err != nil {
		t.Fatalf("count products: %v", err)
	}
	if err := conn.QueryRow(context.Background(), `SELECT count(*) FROM outbox_events`).Scan(&outbox); err != nil {
		t.Fatalf("count outbox_events: %v", err)
	}
	return products, outbox
}

func TestPgtx_CommitsSaveAndOutboxAtomically(t *testing.T) {
	ctx := context.Background()
	dbURL := pgtxMigratedDB(t)
	pool, err := postgres.NewPool(ctx, dbURL)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	repo, ob, uow := postgres.NewProductRepo(pool), postgres.NewOutboxRepo(pool), postgres.NewUnitOfWork(pool)

	// A real write use case (register) inside the real UnitOfWork: the
	// product row and its ProductRegistered outbox row must land together.
	w := usecases.Writer{Products: repo, Outbox: ob, Encoder: outboundkafka.NewEncoder(), UoW: uow, Clock: pgtxClock{}}
	if _, err := (&usecases.RegisterProduct{Writer: w}).Handle(ctx, usecases.RegisterProductCommand{
		SKU: "ITCOV-PGTX-1", Description: "Battery",
	}); err != nil {
		t.Fatalf("register inside the unit of work: %v", err)
	}
	products, outboxRows := pgtxCounts(t, dbURL)
	if products != 1 || outboxRows != 1 {
		t.Fatalf("after commit: products=%d outbox=%d, want 1 and 1 (atomic save+publish)", products, outboxRows)
	}

	// pgtx.From must NOT see a transaction outside a unit of work: the
	// repos fall back to the pool, exactly as the read paths expect.
	if _, ok := pgtx.From(context.Background()); ok {
		t.Fatal("pgtx.From found a transaction on a bare context")
	}
}

func TestPgtx_ErrorInsideTheScopeRollsEverythingBack(t *testing.T) {
	ctx := context.Background()
	dbURL := pgtxMigratedDB(t)
	pool, err := postgres.NewPool(ctx, dbURL)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	repo, ob, uow := postgres.NewProductRepo(pool), postgres.NewOutboxRepo(pool), postgres.NewUnitOfWork(pool)

	// Seed one committed product so the rollback has something to contrast.
	seed, _, err := product.Register(product.SKU("ITCOV-PGTX-SEED"), "seed", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, seed, 0); err != nil {
		t.Fatalf("seed save: %v", err)
	}

	// Inside ONE unit of work: save a second product, enqueue its outbox
	// row, then fail. Neither write may survive.
	boom := errors.New("boom")
	p2, _, err := product.Register(product.SKU("ITCOV-PGTX-2"), "rollback me", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	err = uow.Do(ctx, func(ctx context.Context) error {
		// The ctx handed to fn carries the open tx (pgtx.With): both repos
		// must transparently join it.
		if _, ok := pgtx.From(ctx); !ok {
			t.Fatal("the unit-of-work ctx does not carry the transaction")
		}
		if err := repo.Save(ctx, p2, 0); err != nil {
			return err
		}
		if err := ob.Insert(ctx, mustMessage(t, p2)...); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the scope's error", err)
	}

	// Everything rolled back: the seed row is still there, the scoped
	// product and its outbox row are gone.
	if _, err := repo.Get(ctx, "ITCOV-PGTX-2"); !errors.Is(err, repository.ErrProductNotFound) {
		t.Fatal("the product saved inside the failed scope survived the rollback")
	}
	products, outboxRows := pgtxCounts(t, dbURL)
	if products != 1 || outboxRows != 0 {
		t.Fatalf("after rollback: products=%d outbox=%d, want 1 (the seed) and 0", products, outboxRows)
	}
}

// pgtxClock is the ports.Clock for the seeding write.
type pgtxClock struct{}

func (pgtxClock) Now() time.Time { return time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC) }

// mustMessage encodes p's registration event the way the writer would, for
// a direct outbox Insert inside the scope.
func mustMessage(t *testing.T, p *product.Product) []outbox.Message {
	t.Helper()
	_, events, err := product.Register(p.SKU(), p.Description(), pgtxClock{}.Now())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := outboundkafka.NewEncoder().Encode(events...)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

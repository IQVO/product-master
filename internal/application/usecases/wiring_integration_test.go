//go:build integration

// Package usecases_test proves the product-master write use cases against a
// REAL Postgres (testcontainers): the real ProductRepo, the real OutboxRepo,
// the real postgres.UnitOfWork and the real CloudEvents kafka.Encoder, wired
// exactly like cmd/api's composition root. These are integration tests in
// the fleet's sense: they execute the real cross-component contracts (the
// aggregate lifecycle register -> classify -> measure, optimistic version
// guarding, the atomic save+enqueue bracket) against real infrastructure,
// with no in-memory repo fakes anywhere in the path.
//
// The package boots its own throwaway Postgres via testcontainers in
// TestMain: one container for the whole package, migrated once into a
// template database, one private clone per test. Never an external
// DATABASE_URL, never t.Skip.
package usecases_test

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	outboundkafka "github.com/claudioed/product-master/internal/adapters/outbound/kafka"
	"github.com/claudioed/product-master/internal/adapters/outbound/postgres"
	"github.com/claudioed/product-master/internal/application/repository"
	"github.com/claudioed/product-master/internal/application/usecases"
	"github.com/claudioed/product-master/internal/domain/product"
)

// One Postgres container serves the whole package (containers are slow to
// boot). TestMain starts it, migrates a TEMPLATE database ONCE, and each
// test then gets its own database cloned from that template
// (CREATE DATABASE ... WITH TEMPLATE, a file-level copy: milliseconds).
// Isolation is therefore total, with no dependence on test order.
//
// Never an external DATABASE_URL, never t.Skip.
const wiringTemplateDB = "usecases_migrated_template"

var (
	wiringBaseURL string // connection URL of the container's default database
	wiringDBSeq   atomic.Uint64
)

func TestMain(m *testing.M) {
	os.Exit(runWiringTests(m))
}

func runWiringTests(m *testing.M) int {
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("product_master_usecases"),
		tcpostgres.WithUsername("usecases"),
		tcpostgres.WithPassword("usecases"),
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

	wiringBaseURL, err = container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "postgres connection string: %v\n", err)
		return 1
	}
	if err := wiringCreateDatabase(ctx, wiringTemplateDB); err != nil {
		fmt.Fprintf(os.Stderr, "create template database: %v\n", err)
		return 1
	}
	u, err := url.Parse(wiringBaseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse base url: %v\n", err)
		return 1
	}
	u.Path = "/" + wiringTemplateDB
	if err := postgres.RunMigrations(u.String()); err != nil {
		fmt.Fprintf(os.Stderr, "migrate template: %v\n", err)
		return 1
	}
	return m.Run()
}

// wiringCreateDatabase creates an empty database inside the shared
// container.
func wiringCreateDatabase(ctx context.Context, name string) error {
	conn, err := pgx.Connect(ctx, wiringBaseURL)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, fmt.Sprintf("CREATE DATABASE %q", name)); err != nil {
		return fmt.Errorf("create database %s: %w", name, err)
	}
	return nil
}

// wiringMigratedDB hands the test a connection URL to its own private
// database, cloned from the migrated template.
func wiringMigratedDB(t *testing.T) string {
	t.Helper()
	name := fmt.Sprintf("usecases_%d", wiringDBSeq.Add(1))
	conn, err := pgx.Connect(context.Background(), wiringBaseURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(context.Background(), fmt.Sprintf(
		"CREATE DATABASE %q WITH TEMPLATE %q", name, wiringTemplateDB)); err != nil {
		t.Fatalf("clone database: %v", err)
	}
	u, err := url.Parse(wiringBaseURL)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	return u.String()
}

// wiringClock is the ports.Clock the use cases accept; a deterministic
// timestamp keeps assertions exact.
type wiringClock struct{ now time.Time }

func (c wiringClock) Now() time.Time { return c.now }

var wiringAt = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

// newWiredWriter builds the real adapter stack over a private migrated
// database, wired exactly like cmd/api's composition root: the real
// Postgres repos, the real UnitOfWork and the real CloudEvents encoder. It
// also returns the private database's URL for direct outbox assertions.
func newWiredWriter(t *testing.T) (usecases.Writer, string) {
	t.Helper()
	ctx := context.Background()
	dbURL := wiringMigratedDB(t)
	pool, err := postgres.NewPool(ctx, dbURL)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return usecases.Writer{
		Products: postgres.NewProductRepo(pool), Outbox: postgres.NewOutboxRepo(pool),
		Encoder: outboundkafka.NewEncoder(), UoW: postgres.NewUnitOfWork(pool), Clock: wiringClock{wiringAt},
	}, dbURL
}

// wiringOutboxTypes returns the CloudEvents types of every outbox row in id
// order, read straight out of the private database, so tests assert exactly
// what the use cases published (the buffering-publisher assertions of the
// reference shape, against the real transactional outbox).
func wiringOutboxTypes(t *testing.T, dbURL string) []string {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(context.Background())
	rows, err := conn.Query(context.Background(), `SELECT event_type FROM outbox_events ORDER BY id`)
	if err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var typ string
		if err := rows.Scan(&typ); err != nil {
			t.Fatalf("scan outbox: %v", err)
		}
		out = append(out, typ)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	return out
}

// wiringSeedRows asserts the seed lifecycle published exactly the expected
// CloudEvents types, in order, through the real outbox.
func assertOutbox(t *testing.T, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("outbox = %v, want %v", got, want)
	}
}

func TestWiring_RegisterClassifyMeasureLifecycle(t *testing.T) {
	w, dbURL := newWiredWriter(t)
	ctx := context.Background()

	// Register (the aggregate's birth): v1, one ProductRegistered enqueued.
	reg := &usecases.RegisterProduct{Writer: w}
	res, err := reg.Handle(ctx, usecases.RegisterProductCommand{SKU: "ITCOV-WIRE-1", Description: "Battery"})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if !res.Created || res.Product.Version() != 1 || res.Product.Description() != "Battery" {
		t.Fatalf("register = created %v v%d %q", res.Created, res.Product.Version(), res.Product.Description())
	}

	// Re-register with a new description: a replace, not a create (v2).
	res, err = reg.Handle(ctx, usecases.RegisterProductCommand{SKU: "ITCOV-WIRE-1", Description: "Battery 12V"})
	if err != nil {
		t.Fatalf("re-register: %v", err)
	}
	if res.Created || res.Product.Version() != 2 {
		t.Fatalf("re-register = created %v v%d", res.Created, res.Product.Version())
	}

	// Classify (the state change): native source, v3.
	cls, err := (&usecases.ClassifyProduct{Writer: w}).Handle(ctx, usecases.ClassifyProductCommand{
		SKU: "ITCOV-WIRE-1", HandlingTags: []string{"Hazmat", "TemperatureSensitive"},
		TemperatureClass: "Frozen", DOTHazardClass: 3,
	})
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if !cls.Created || cls.Product.Version() != 3 {
		t.Fatalf("classify = created %v v%d", cls.Created, cls.Product.Version())
	}

	// Measure (the physical profile): v4.
	measured, err := (&usecases.RecordMeasurement{Writer: w}).Handle(ctx, usecases.RecordMeasurementCommand{
		SKU:             "ITCOV-WIRE-1",
		DimensionsInput: usecases.DimensionsInput{LengthMm: 205, WidthMm: 101, HeightMm: 52, WeightG: 510},
		MeasuredAt:      wiringAt.Add(-time.Hour), DeviceID: "CUBISCAN-03",
	})
	if err != nil {
		t.Fatalf("measure: %v", err)
	}
	if measured.Version() != 4 {
		t.Fatalf("measure = v%d", measured.Version())
	}

	// The read model agrees: the load through the REAL repo round-trips the
	// whole aggregate, not just the use case's in-memory copy.
	loaded, err := (&usecases.GetProduct{Products: w.Products}).Handle(ctx, "ITCOV-WIRE-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if loaded.Version() != 4 || loaded.Description() != "Battery 12V" {
		t.Fatalf("loaded = v%d %q", loaded.Version(), loaded.Description())
	}
	c, source, err := loaded.Classification()
	if err != nil || source != product.SourceNative || !c.HasTag(product.Hazmat) || c.DOTHazardClass() != 3 {
		t.Fatalf("loaded classification = %v %v %v", c, source, err)
	}
	if _, ok := loaded.PhysicalProfile().Measured(); !ok {
		t.Fatal("loaded profile lost the measurement")
	}

	// Every step published exactly one event, atomically with its save, in
	// order, through the real transactional outbox.
	prefix := "com.warehouse.wms.product-master.product."
	assertOutbox(t, wiringOutboxTypes(t, dbURL), []string{
		prefix + "ProductRegistered",
		prefix + "ProductDescriptionChanged",
		prefix + "ProductClassified",
		prefix + "ProductMeasured",
	})
}

func TestWiring_NoChangeEventPublishesNothing(t *testing.T) {
	w, dbURL := newWiredWriter(t)
	ctx := context.Background()

	if _, err := (&usecases.RegisterProduct{Writer: w}).Handle(ctx, usecases.RegisterProductCommand{
		SKU: "ITCOV-WIRE-2", Description: "same",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	// The identical description is no change: still v1, no new outbox row.
	res, err := (&usecases.RegisterProduct{Writer: w}).Handle(ctx, usecases.RegisterProductCommand{
		SKU: "ITCOV-WIRE-2", Description: "same",
	})
	if err != nil {
		t.Fatalf("re-register identical: %v", err)
	}
	if res.Created || res.Product.Version() != 1 {
		t.Fatalf("identical re-register = created %v v%d", res.Created, res.Product.Version())
	}
	prefix := "com.warehouse.wms.product-master.product."
	assertOutbox(t, wiringOutboxTypes(t, dbURL), []string{prefix + "ProductRegistered"})
}

func TestWiring_UnknownSKUWriteIsRejected(t *testing.T) {
	w, dbURL := newWiredWriter(t)
	ctx := context.Background()

	// Classifying a product that was never registered must fail and leave
	// no row and no outbox event behind.
	_, err := (&usecases.ClassifyProduct{Writer: w}).Handle(ctx, usecases.ClassifyProductCommand{
		SKU: "ITCOV-WIRE-404", HandlingTags: []string{"Fragile"},
	})
	if !errors.Is(err, repository.ErrProductNotFound) {
		t.Fatalf("err = %v, want the repository's not-found error", err)
	}
	if _, err := (&usecases.GetProduct{Products: w.Products}).Handle(ctx, "ITCOV-WIRE-404"); err == nil {
		t.Fatal("the rejected write must not have registered the product")
	}
	if got := wiringOutboxTypes(t, dbURL); len(got) != 0 {
		t.Fatalf("the rejected write must not have published anything, got %v", got)
	}
}

//go:build integration

package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	// The test starts its Kafka through internal/testing/kafkatest; the blank
	// import keeps testcontainers-go/modules/kafka visible in this file, which
	// TestKafkaIntegrationTestsUseTestcontainers requires of every
	// Kafka-touching integration test.
	_ "github.com/testcontainers/testcontainers-go/modules/kafka"

	"github.com/claudioed/product-master/internal/adapters/outbound/analyticsstore"
	outboundkafka "github.com/claudioed/product-master/internal/adapters/outbound/kafka"
	"github.com/claudioed/product-master/internal/domain/product"
	"github.com/claudioed/product-master/internal/testing/kafkatest"
	"github.com/claudioed/product-master/internal/testing/pgtest"
)

func TestMain(m *testing.M) {
	code := m.Run()
	kafkatest.Shutdown()
	pgtest.Shutdown()
	os.Exit(code)
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().String()
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(90 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

// stopWithSIGTERM delivers SIGTERM to this process until run() returns.
// run() takes no context: it listens for SIGINT/SIGTERM itself
// (signal.NotifyContext). The test's own handler is registered FIRST so a
// signal arriving before run() reaches its NotifyContext can never kill the
// test binary (Go's default SIGTERM disposition), and the signal is retried
// until run() observes one. This is the documented drain contract: flip
// readiness, drain the admin server, let the consumer finish the message in
// flight, exit nil.
func stopWithSIGTERM(t *testing.T, done <-chan error) error {
	t.Helper()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
		select {
		case err := <-done:
			return err
		case <-sigCh:
			// Swallowed by the test's own handler; run() may not have been
			// listening yet. Fall through and send another.
		case <-time.After(300 * time.Millisecond):
		}
	}
	t.Fatal("run() did not return after SIGTERM")
	return nil
}

// The real entry point over an EMPTY analytical database and a real Kafka:
// the binary migrates the database itself (openAnalyticsPool ->
// analyticsstore.RunMigrations, ADR 0006), /readyz answers, the consumer
// projects the repo's OWN AnalyticsEncoder events from
// warehouse.product-master.analytics into product_facts / profile_states
// (including the stale-version drop rule), and SIGTERM drains to a clean
// nil exit.
func TestProjectorBootsMigratesProjectsAndStopsCleanly(t *testing.T) {
	url := pgtest.EmptyURL(t)
	brokers := kafkatest.Brokers(t)
	kafkatest.CreateTopic(t, outboundkafka.AnalyticsTopic)

	// The projector's input fixture, encoded by the repo's own analytics
	// encoder (the same bytes the outbox fan-out would relay): the full
	// profile journey of one SKU (registered -> declared -> measured with a
	// >10 % disagreement, then a LATE stale v2 declared event), and a second
	// SKU whose ProductDescriptionChanged marks no milestone (report.FirstsOf:
	// "existence only").
	const skuA, skuB = "SKU-ITEST-PROJ-1", "SKU-ITEST-PROJ-2"
	t0 := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	t1 := t0.Add(5 * time.Minute)
	t2 := t0.Add(10 * time.Minute)
	t3 := t0.Add(15 * time.Minute)
	declared, err := product.NewUnitDimensions(200, 100, 50, 500)
	if err != nil {
		t.Fatal(err)
	}
	measuredDims, err := product.NewUnitDimensions(250, 100, 50, 600)
	if err != nil {
		t.Fatal(err)
	}
	// 250x100x50 vs 200x100x50: the volume differs by more than 10 % of the
	// declared value, so the profile carries discrepancy (ADR 0002).
	measurement, err := product.NewMeasurement(measuredDims, t2, "", t2.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	declaredOnly := product.RehydratePhysicalProfile(&declared, nil)
	declaredAndMeasured := product.RehydratePhysicalProfile(&declared, &measurement)
	events := []product.Event{
		product.ProductRegistered{Header: product.Header{SKU: skuA, Version: 1, At: t0}, Description: "projector integration fixture"},
		product.ProductDimensionsDeclared{Header: product.Header{SKU: skuA, Version: 2, At: t1}, Profile: declaredOnly},
		product.ProductMeasured{Header: product.Header{SKU: skuA, Version: 3, At: t2}, Profile: declaredAndMeasured},
		// A stale re-declaration (version 2 again, minted AFTER the v3
		// measurement): the current profile is overwritten only by a GREATER
		// version (the ADR 0004 consumer rule, AGENTS.md rule 4).
		product.ProductDimensionsDeclared{Header: product.Header{SKU: skuA, Version: 2, At: t3}, Profile: declaredOnly},
		product.ProductRegistered{Header: product.Header{SKU: skuB, Version: 1, At: t0}, Description: "existence-only fixture"},
		product.ProductDescriptionChanged{Header: product.Header{SKU: skuB, Version: 2, At: t1}, Description: "renamed"},
	}
	msgs, err := (&outboundkafka.AnalyticsEncoder{}).Encode(events...)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		headers := make(map[string]string, len(m.Headers))
		for _, h := range m.Headers {
			headers[h.Key] = h.Value
		}
		kafkatest.Produce(t, m.Topic, kafkatest.Message{Key: string(m.Key), Value: m.Value, Headers: headers})
	}

	group := fmt.Sprintf("product-projector-itest-%d", time.Now().UnixNano())
	t.Setenv("ANALYTICS_DATABASE_URL", url)
	t.Setenv("KAFKA_BROKERS", strings.Join(brokers, ","))
	t.Setenv("ANALYTICS_CONSUMER_GROUP", group)
	addr := freeAddr(t)
	t.Setenv("ADMIN_ADDR", addr)
	t.Setenv("LOG_LEVEL", "error")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "127.0.0.1:1")
	done := make(chan error, 1)
	go func() { done <- run() }()

	waitUntil(t, "/readyz 200 on the admin port", func() bool {
		resp, err := http.Get("http://" + addr + "/readyz")
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	})

	pool, err := analyticsstore.NewPool(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	// The documented projection (analytics/migrations/0001): the four
	// first-milestones with LEAST semantics, the CURRENT profile state at the
	// highest version, and one idempotency mark per applied CloudEvents id.
	waitUntil(t, "the SKU-A journey and the SKU-B existence row projected", func() bool {
		var regA, declA, measA time.Time
		var classifiedA *time.Time
		var version int64
		var hasDeclared, hasMeasured, discrepancy bool
		err := pool.QueryRow(context.Background(), `SELECT registered_at, first_classified_at, first_declared_at, first_measured_at,
			profile_version, has_declared, has_measured, discrepancy FROM product_facts WHERE sku = $1`, skuA).
			Scan(&regA, &classifiedA, &declA, &measA, &version, &hasDeclared, &hasMeasured, &discrepancy)
		if err != nil || !regA.Equal(t0) || classifiedA != nil || !declA.Equal(t1) || !measA.Equal(t2) ||
			version != 3 || !hasDeclared || !hasMeasured || !discrepancy {
			return false
		}
		var regB time.Time
		var classifiedB, declB, measB *time.Time
		var versionB int64
		var hasDeclaredB, hasMeasuredB, discrepancyB bool
		err = pool.QueryRow(context.Background(), `SELECT registered_at, first_classified_at, first_declared_at, first_measured_at,
			profile_version, has_declared, has_measured, discrepancy FROM product_facts WHERE sku = $1`, skuB).
			Scan(&regB, &classifiedB, &declB, &measB, &versionB, &hasDeclaredB, &hasMeasuredB, &discrepancyB)
		if err != nil || !regB.Equal(t0) || classifiedB != nil || declB != nil || measB != nil ||
			versionB != 0 || hasDeclaredB || hasMeasuredB || discrepancyB {
			return false
		}
		var processed int
		return pool.QueryRow(context.Background(), `SELECT count(*) FROM analytics_processed_events`).Scan(&processed) == nil && processed == len(msgs)
	})

	if err := stopWithSIGTERM(t, done); err != nil {
		t.Fatalf("a SIGTERMed projector must drain cleanly, got %v", err)
	}
}

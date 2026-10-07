//go:build integration

package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	kafkago "github.com/segmentio/kafka-go"
	"github.com/testcontainers/testcontainers-go"
	tckafka "github.com/testcontainers/testcontainers-go/modules/kafka"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	inboundhttp "github.com/claudioed/product-master/internal/adapters/inbound/http"
	inboundkafka "github.com/claudioed/product-master/internal/adapters/inbound/kafka"
	"github.com/claudioed/product-master/internal/adapters/outbound/analyticsstore"
	outboundkafka "github.com/claudioed/product-master/internal/adapters/outbound/kafka"
	relay "github.com/claudioed/product-master/internal/adapters/outbound/outbox"
	"github.com/claudioed/product-master/internal/adapters/outbound/postgres"
	"github.com/claudioed/product-master/internal/application/outbox"
	"github.com/claudioed/product-master/internal/application/usecases"
)

// The whole analytics read side (ADR 0006) against real infrastructure, all
// started with testcontainers (never skip-gated, never a hardcoded broker):
// products are registered, classified, declared and measured against the OLTP
// Postgres (both topics' rows in one transaction), the relay drains the outbox
// to a real Kafka (losing one ack, so a row is republished under the SAME id),
// the projector's consumer projects the analytics topic into a SEPARATE
// analytical Postgres, and the reports endpoints answer from it -- while
// legacy, unknown-type, poison and duplicate messages share the topic.

func e2eStartPostgres(t *testing.T, db string) string {
	t.Helper()
	ctx := context.Background()
	c, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase(db), tcpostgres.WithUsername("itest"), tcpostgres.WithPassword("itest"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(60*time.Second)))
	if err != nil {
		t.Fatalf("start postgres %s: %v", db, err)
	}
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })
	url, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	return url
}

func e2eStartKafka(t *testing.T) []string {
	t.Helper()
	ctx := context.Background()
	c, err := tckafka.Run(ctx, "confluentinc/confluent-local:7.6.1", tckafka.WithClusterID("product-master-analytics-e2e"))
	if err != nil {
		t.Fatalf("start kafka: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })
	brokers, err := c.Brokers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return brokers
}

func e2eCreateTopic(t *testing.T, broker, topic string) {
	t.Helper()
	conn, err := kafkago.Dial("tcp", broker)
	if err != nil {
		t.Fatalf("dial broker: %v", err)
	}
	defer conn.Close()
	if err := conn.CreateTopics(kafkago.TopicConfig{Topic: topic, NumPartitions: 1, ReplicationFactor: 1}); err != nil {
		t.Fatalf("create topic: %v", err)
	}
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if parts, err := conn.ReadPartitions(topic); err == nil && len(parts) == 1 && parts[0].Leader.ID != 0 {
			return
		}
	}
	t.Fatalf("topic %s never got a leader", topic)
}

// lostAckOnce delivers to the real sink and THEN reports failure once: the
// "published, but the row was not marked" crash window, so the relay
// republishes the first row with the SAME CloudEvents id.
type lostAckOnce struct {
	inner relay.Sink
	mu    sync.Mutex
	fired bool
}

func (f *lostAckOnce) Send(ctx context.Context, msgs ...outbox.Message) error {
	if err := f.inner.Send(ctx, msgs...); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.fired {
		f.fired = true
		return errors.New("simulated crash after the broker ack, before the row was marked published")
	}
	return nil
}

// settableClock is the use cases' clock, moved by the test.
type settableClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *settableClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *settableClock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

func e2eAt(day, hour int) time.Time { return time.Date(2026, 10, day, hour, 0, 0, 0, time.UTC) }

// e2eWrites drives the real use cases over the OLTP database.
type e2eWrites struct {
	t     *testing.T
	w     usecases.Writer
	clock *settableClock
}

func (e e2eWrites) at(when time.Time, what string, run func(ctx context.Context) error) {
	e.t.Helper()
	e.clock.set(when)
	if err := run(context.Background()); err != nil {
		e.t.Fatalf("%s: %v", what, err)
	}
}

func (e e2eWrites) register(sku string, when time.Time) {
	e.t.Helper()
	e.at(when, "register "+sku, func(ctx context.Context) error {
		_, err := (&usecases.RegisterProduct{Writer: e.w}).Handle(ctx, usecases.RegisterProductCommand{SKU: sku, Description: "e2e " + sku})
		return err
	})
}

func (e e2eWrites) classify(sku string, when time.Time) {
	e.t.Helper()
	e.at(when, "classify "+sku, func(ctx context.Context) error {
		_, err := (&usecases.ClassifyProduct{Writer: e.w}).Handle(ctx, usecases.ClassifyProductCommand{SKU: sku, HandlingTags: []string{"Fragile"}})
		return err
	})
}

func (e e2eWrites) declare(sku string, lengthMm int64, when time.Time) {
	e.t.Helper()
	e.at(when, "declare "+sku, func(ctx context.Context) error {
		_, err := (&usecases.DeclareDimensions{Writer: e.w}).Handle(ctx, usecases.DeclareDimensionsCommand{SKU: sku,
			DimensionsInput: usecases.DimensionsInput{LengthMm: lengthMm, WidthMm: 120, HeightMm: 80, WeightG: 1500}})
		return err
	})
}

func (e e2eWrites) measure(sku string, lengthMm int64, when time.Time) {
	e.t.Helper()
	e.at(when, "measure "+sku, func(ctx context.Context) error {
		_, err := (&usecases.RecordMeasurement{Writer: e.w}).Handle(ctx, usecases.RecordMeasurementCommand{SKU: sku,
			DimensionsInput: usecases.DimensionsInput{LengthMm: lengthMm, WidthMm: 120, HeightMm: 80, WeightG: 1500},
			MeasuredAt:      when.Add(-time.Minute), DeviceID: "CUBISCAN-03"})
		return err
	})
}

type e2eReport struct {
	Days     []map[string]any `json:"days"`
	Coverage map[string]any   `json:"coverage"`
}

func TestAnalyticsReadSide_EndToEnd(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	brokers := e2eStartKafka(t)
	oltpURL := e2eStartPostgres(t, "product_master_test")
	analyticalURL := e2eStartPostgres(t, "product_master_analytics")
	if err := postgres.RunMigrations(oltpURL); err != nil {
		t.Fatal(err)
	}
	if err := analyticsstore.RunMigrations(analyticalURL); err != nil {
		t.Fatal(err)
	}
	oltp, err := postgres.NewPool(ctx, oltpURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(oltp.Close)

	nonce := time.Now().UnixNano()
	integrationTopic := fmt.Sprintf("warehouse.product-master.events.e2e-%d", nonce)
	analyticsTopic := fmt.Sprintf("warehouse.product-master.analytics.e2e-%d", nonce)
	for _, topic := range []string{integrationTopic, analyticsTopic, analyticsTopic + inboundkafka.DLQSuffix} {
		e2eCreateTopic(t, brokers[0], topic)
	}

	// ---- OLTP writes: both topics' rows in ONE transaction per change ----
	ob := postgres.NewOutboxRepo(oltp)
	clock := &settableClock{}
	writes := e2eWrites{t: t, clock: clock, w: usecases.Writer{
		Products: postgres.NewProductRepo(oltp), Outbox: ob, UoW: postgres.NewUnitOfWork(oltp), Clock: clock,
		Encoder: &outboundkafka.FanoutEncoder{
			Integration: &outboundkafka.Encoder{Topic: integrationTopic},
			Analytics:   &outboundkafka.AnalyticsEncoder{Topic: analyticsTopic},
			NewID:       uuid.NewString,
		},
	}}
	// Day 5: A and B registered; A classified, declared, then measured 50% longer (a discrepancy).
	writes.register("SKU-A", e2eAt(5, 8))
	writes.register("SKU-B", e2eAt(5, 9))
	writes.classify("SKU-A", e2eAt(5, 10))
	writes.declare("SKU-A", 200, e2eAt(5, 11))
	writes.measure("SKU-A", 300, e2eAt(5, 12))
	// Day 6: C registered; B declared and measured in agreement; A re-declared to its measurement (resolved).
	writes.register("SKU-C", e2eAt(6, 8))
	writes.declare("SKU-B", 200, e2eAt(6, 9))
	writes.measure("SKU-B", 200, e2eAt(6, 10))
	writes.declare("SKU-A", 300, e2eAt(6, 11))
	if n := countRows(t, oltp, `SELECT count(*) FROM outbox_events WHERE topic = $1`, analyticsTopic); n != 9 {
		t.Fatalf("analytics outbox rows = %d, want 9 (one per event)", n)
	}

	// ---- the topic also carries messages the projector must survive ----
	producer := &kafkago.Writer{Addr: kafkago.TCP(brokers...), Topic: analyticsTopic, BatchTimeout: 10 * time.Millisecond, RequiredAcks: kafkago.RequireAll}
	t.Cleanup(func() { _ = producer.Close() })
	legacy := []byte(`{"event_id":"e-1","event_type":"ProductRegistered","occurred_at":"2026-10-05T08:00:00Z","payload":{"sku":"x"}}`)
	unknown := hand(t, "evt-unknown", "com.warehouse.wms.product-master.product.ProductRetired", "SKU-U", e2eAt(5, 13), map[string]any{"sku": "SKU-U", "version": 9})
	poison := hand(t, "evt-poison", "com.warehouse.wms.product-master.product.ProductMeasured", "SKU-P", e2eAt(5, 13),
		map[string]any{"sku": "SKU-P", "version": 2, "effective_source": "none", "discrepancy": false}) // a measurement event without a measurement
	if err := producer.WriteMessages(ctx,
		kafkago.Message{Key: []byte("x"), Value: legacy}, kafkago.Message{Key: []byte("SKU-U"), Value: unknown},
		kafkago.Message{Key: []byte("SKU-P"), Value: poison}); err != nil {
		t.Fatal(err)
	}

	// ---- relay -> real Kafka (with one lost ack: a republish under the SAME id) ----
	sink := outboundkafka.NewRelaySink(brokers)
	t.Cleanup(func() { _ = sink.Close() })
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	r := relay.NewRelay(ob, &lostAckOnce{inner: sink}, quiet, relay.WithInterval(50*time.Millisecond))
	relayDone := make(chan struct{})
	go func() { defer close(relayDone); _ = r.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-relayDone })

	// ---- the projector: consumer + writer pool over the analytical database ----
	writer, err := analyticsstore.NewPool(ctx, analyticalURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(writer.Close)
	consumer := inboundkafka.NewAnalyticsConsumer(brokers, analyticsTopic, fmt.Sprintf("product-projector-e2e-%d", nonce),
		analyticsstore.NewProjection(writer), quiet)
	consumer.Retry = inboundkafka.RetryPolicy{Initial: 50 * time.Millisecond, Max: 200 * time.Millisecond}
	consumerDone := make(chan struct{})
	go func() { defer close(consumerDone); _ = consumer.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-consumerDone; _ = consumer.Close() })

	// ---- the reports service over a READ-ONLY pool ----
	readOnly, err := analyticsstore.NewReadOnlyPool(ctx, analyticalURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(readOnly.Close)
	srv := httptest.NewServer(inboundhttp.NewReportsRouter(&inboundhttp.ReportsServer{
		Reader: analyticsstore.NewReader(readOnly), Now: func() time.Time { return e2eAt(6, 12) }}))
	t.Cleanup(srv.Close)
	report := func() e2eReport {
		var out e2eReport
		getJSON(t, srv.URL+"/reports/master-data-quality?from=2026-10-05T00:00:00Z&to=2026-10-07T00:00:00Z", &out)
		return out
	}
	waitFor(t, 90*time.Second, "all nine events projected", func() bool {
		var f struct {
			AsOf *time.Time `json:"as_of"`
		}
		getJSON(t, srv.URL+"/reports/freshness", &f)
		return f.AsOf != nil && f.AsOf.Equal(e2eAt(6, 11))
	})

	assertMasterDataQuality(t, report())
	assertOnlyThePoisonWasDeadLettered(t, brokers, analyticsTopic+inboundkafka.DLQSuffix, poison)

	// ---- a redelivered analytics message (same id) changes nothing; a later marker proves it was consumed ----
	var dup []byte
	if err := oltp.QueryRow(ctx, `SELECT value FROM outbox_events WHERE topic = $1 AND event_type LIKE '%ProductRegistered' ORDER BY id LIMIT 1`, analyticsTopic).Scan(&dup); err != nil {
		t.Fatal(err)
	}
	marker := hand(t, "evt-marker", "com.warehouse.wms.product-master.product.ProductDescriptionChanged", "SKU-MARKER", e2eAt(6, 11),
		map[string]any{"sku": "SKU-MARKER", "description": "marker", "version": 2})
	if err := producer.WriteMessages(ctx, kafkago.Message{Key: []byte("SKU-A"), Value: dup}, kafkago.Message{Key: []byte("SKU-MARKER"), Value: marker}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 60*time.Second, "the marker product projected", func() bool {
		return report().Coverage["products"] == 4.0
	})
	if day5 := report().Days[0]; day5["registered"] != 2.0 {
		t.Errorf("after a redelivered ProductRegistered day 5 registered = %v, want still 2 (idempotent on the CloudEvents id)", day5["registered"])
	}

	// ---- every outbox row was published (the lost ack was retried, not lost) ----
	waitFor(t, 30*time.Second, "outbox drained", func() bool {
		return countRows(t, oltp, `SELECT count(*) FROM outbox_events WHERE published_at IS NULL`) == 0
	})
}

func assertMasterDataQuality(t *testing.T, got e2eReport) {
	t.Helper()
	wantDays := []map[string]any{
		{"day": "2026-10-05", "registered": 2.0, "classified": 1.0, "dimensions_declared": 1.0, "measured": 1.0, "open_discrepancies": 1.0},
		{"day": "2026-10-06", "registered": 1.0, "classified": 0.0, "dimensions_declared": 1.0, "measured": 1.0, "open_discrepancies": 0.0},
	}
	if !reflect.DeepEqual(got.Days, wantDays) {
		t.Errorf("days = %v\nwant %v", got.Days, wantDays)
	}
	wantCoverage := map[string]any{
		"products": 3.0, "classified": 1.0, "dimensions_declared": 2.0, "measured": 2.0, "open_discrepancies": 0.0,
		"classified_ratio": 1.0 / 3, "dimensions_declared_ratio": 2.0 / 3, "measured_ratio": 2.0 / 3, "discrepancy_ratio": 0.0,
	}
	if !reflect.DeepEqual(got.Coverage, wantCoverage) {
		t.Errorf("coverage = %v\nwant %v", got.Coverage, wantCoverage)
	}
}

// assertOnlyThePoisonWasDeadLettered reads the DLQ: exactly the poison bytes,
// nothing for the legacy or unknown-type messages.
func assertOnlyThePoisonWasDeadLettered(t *testing.T, brokers []string, dlqTopic string, poison []byte) {
	t.Helper()
	dlq := kafkago.NewReader(kafkago.ReaderConfig{Brokers: brokers, Topic: dlqTopic, Partition: 0, MinBytes: 1, MaxBytes: 10e6})
	defer dlq.Close()
	readCtx, stop := context.WithTimeout(context.Background(), 60*time.Second)
	defer stop()
	m, err := dlq.ReadMessage(readCtx)
	if err != nil {
		t.Fatalf("nothing reached the DLQ: %v", err)
	}
	if !bytes.Equal(m.Value, poison) {
		t.Errorf("DLQ value differs from the poison message:\n%s", m.Value)
	}
	quietCtx, quietStop := context.WithTimeout(context.Background(), 3*time.Second)
	defer quietStop()
	if extra, err := dlq.ReadMessage(quietCtx); err == nil {
		t.Errorf("a second message reached the DLQ (legacy and unknown types are skipped, not dead-lettered): %s", extra.Value)
	}
}

func hand(t *testing.T, id, typ, subject string, at time.Time, data map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"specversion": "1.0", "id": id, "source": "/warehouse/product-master", "type": typ, "subject": subject,
		"datacontenttype": "application/json", "time": at.Format(time.RFC3339), "data": data,
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func getJSON(t *testing.T, url string, into any) {
	t.Helper()
	resp, err := http.Get(url) //nolint:noctx // test helper against a local httptest server
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d %s", url, resp.StatusCode, body)
	}
	if err := json.Unmarshal(body, into); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
}

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(timeout); ; time.Sleep(200 * time.Millisecond) {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for: %s", what)
		}
	}
}

func countRows(t *testing.T, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

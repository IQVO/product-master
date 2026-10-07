//go:build integration

package kafka_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	kafkago "github.com/segmentio/kafka-go"
	"github.com/testcontainers/testcontainers-go"
	_ "github.com/testcontainers/testcontainers-go/modules/kafka" // the broker comes from startKafka (testcontainers), shared per package
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	inboundkafka "github.com/claudioed/product-master/internal/adapters/inbound/kafka"
	"github.com/claudioed/product-master/internal/adapters/outbound/analyticsstore"
	outboundkafka "github.com/claudioed/product-master/internal/adapters/outbound/kafka"
	"github.com/claudioed/product-master/internal/analytics/report"
	"github.com/claudioed/product-master/internal/domain/product"
)

// The projector's consumer against real infrastructure (testcontainers only):
// the real AnalyticsEncoder's bytes on a real topic land in a real analytical
// Postgres; garbage is skipped, poison is dead-lettered byte for byte, a
// redelivered id is applied once, and the offsets are committed (a second
// consumer in the same group resumes after them).

func startAnalyticsPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("product_master_analytics"),
		tcpostgres.WithUsername("analytics"),
		tcpostgres.WithPassword("analytics"),
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
	if err := analyticsstore.RunMigrations(url); err != nil {
		t.Fatalf("analytics migrations: %v", err)
	}
	pool, err := analyticsstore.NewPool(ctx, url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func analyticsMessage(t *testing.T, topic, id string, ev product.Event) kafkago.Message {
	t.Helper()
	msgs, err := (&outboundkafka.AnalyticsEncoder{NewID: func() string { return id }, Topic: topic}).Encode(ev)
	if err != nil {
		t.Fatal(err)
	}
	return kafkago.Message{Key: msgs[0].Key, Value: msgs[0].Value}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestAnalyticsConsumer_RealKafkaAndPostgres(t *testing.T) {
	brokers := startKafka(t)
	pool := startAnalyticsPool(t)
	stamp := time.Now().UnixNano()
	topic := fmt.Sprintf("warehouse.product-master.analytics.itest-%d", stamp)
	group := fmt.Sprintf("product-master-analytics-itest-%d", stamp)
	createTopic(t, brokers[0], topic)
	createTopic(t, brokers[0], topic+inboundkafka.DLQSuffix)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	reader := analyticsstore.NewReader(pool)
	at := time.Now().UTC().Truncate(time.Second)

	// start runs one group member; the returned stop is idempotent.
	start := func() (stop func()) {
		c := inboundkafka.NewAnalyticsConsumer(brokers, topic, group, analyticsstore.NewProjection(pool), logger)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { defer close(done); _ = c.Run(ctx) }()
		var once sync.Once
		stop = func() { once.Do(func() { cancel(); <-done; _ = c.Close() }) }
		t.Cleanup(stop)
		return stop
	}
	stopFirst := start()

	registered := analyticsMessage(t, topic, "evt-reg", product.ProductRegistered{Header: product.Header{SKU: "SKU-1", Version: 1, At: at}})
	poison := kafkago.Message{Key: []byte("SKU-1"), Value: bytes.Replace(registered.Value, []byte(`"subject":"SKU-1"`), []byte(`"subject":"SKU-2"`), 1)}
	poison.Value = bytes.Replace(poison.Value, []byte(`"evt-reg"`), []byte(`"evt-poison"`), 1)
	cls, err := product.NewClassification([]product.HandlingTag{product.Fragile}, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	marker := analyticsMessage(t, topic, "evt-cls", product.ProductClassified{Header: product.Header{SKU: "SKU-1", Version: 2, At: at}, Classification: cls, Source: product.SourceNative})
	publish(t, brokers, topic,
		kafkago.Message{Key: []byte("x"), Value: []byte(`{"event_id":"flat","event_type":"ProductRegistered"}`)}, // not a CloudEvent: skipped
		registered,
		poison,
		registered, // redelivered id: applied once
		marker,     // proves everything before it was consumed
	)

	waitFor(t, "the marker to be projected", func() bool {
		c, err := reader.Coverage(context.Background())
		return err == nil && c.Classified == 1
	})
	c, _ := reader.Coverage(context.Background())
	if c != (report.CoverageCounts{Products: 1, Classified: 1}) {
		t.Fatalf("coverage = %+v", c)
	}
	var applied int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM analytics_processed_events`).Scan(&applied); err != nil || applied != 2 {
		t.Fatalf("applied ids = %d (%v), want 2 (the duplicate and the poison are not applied)", applied, err)
	}

	assertRawDeadLetter(t, readRaw(t, brokers, topic+inboundkafka.DLQSuffix, 1)[0], poison, topic)

	// Offsets were committed: a second member of the same group, started
	// after the first one left, resumes after them. Were they not, it would
	// re-read the poison and dead-letter it a second time.
	stopFirst()
	start()
	measured := analyticsMessage(t, topic, "evt-measured", product.ProductMeasured{
		Header:  product.Header{SKU: "SKU-1", Version: 3, At: at},
		Profile: product.RehydratePhysicalProfile(nil, ptr(product.RehydrateMeasurement(product.RehydrateUnitDimensions(1, 2, 3, 4), at.Add(-time.Minute), ""))),
	})
	publish(t, brokers, topic, measured)
	waitFor(t, "the measurement to be projected", func() bool {
		c, err := reader.Coverage(context.Background())
		return err == nil && c.Measured == 1
	})
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM analytics_processed_events`).Scan(&applied); err != nil || applied != 3 {
		t.Fatalf("applied ids = %d (%v), want 3", applied, err)
	}
	if n := countRaw(t, brokers, topic+inboundkafka.DLQSuffix, 3*time.Second); n != 1 {
		t.Fatalf("dlq messages = %d, want still 1 (the second member re-read committed offsets)", n)
	}
}

// countRaw counts the messages of partition 0 of topic, reading until idle.
func countRaw(t *testing.T, brokers []string, topic string, idle time.Duration) int {
	t.Helper()
	r := kafkago.NewReader(kafkago.ReaderConfig{Brokers: brokers, Topic: topic, Partition: 0, MinBytes: 1, MaxBytes: 1 << 20})
	defer r.Close()
	n := 0
	for {
		ctx, cancel := context.WithTimeout(context.Background(), idle)
		_, err := r.ReadMessage(ctx)
		cancel()
		if err != nil {
			return n
		}
		n++
	}
}

// assertRawDeadLetter checks a DLQ message carries the poison's raw bytes and
// key and the x-dlq-* context of its source offset (2).
func assertRawDeadLetter(t *testing.T, got, poison kafkago.Message, topic string) {
	t.Helper()
	if !bytes.Equal(got.Value, poison.Value) || string(got.Key) != "SKU-1" {
		t.Fatalf("dlq message = %s, want the raw poison bytes", got.Value)
	}
	headers := map[string]string{}
	for _, h := range got.Headers {
		headers[h.Key] = string(h.Value)
	}
	if headers["x-dlq-source-topic"] != topic || headers["x-dlq-error"] == "" || headers["x-dlq-source-offset"] != "2" {
		t.Fatalf("dlq headers = %v", headers)
	}
}

func ptr[T any](v T) *T { return &v }

// readRaw reads n raw messages from partition 0 of topic, from the start.
func readRaw(t *testing.T, brokers []string, topic string, n int) []kafkago.Message {
	t.Helper()
	r := kafkago.NewReader(kafkago.ReaderConfig{Brokers: brokers, Topic: topic, Partition: 0, MinBytes: 1, MaxBytes: 1 << 20})
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out := make([]kafkago.Message, 0, n)
	for len(out) < n {
		m, err := r.ReadMessage(ctx)
		if err != nil {
			t.Fatalf("read %d of %d from %s: %v", len(out), n, topic, err)
		}
		out = append(out, m)
	}
	return out
}

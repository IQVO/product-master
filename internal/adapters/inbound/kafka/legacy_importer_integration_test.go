//go:build integration

package kafka_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"
	"github.com/jackc/pgx/v5/pgxpool"
	kafkago "github.com/segmentio/kafka-go"
	"github.com/testcontainers/testcontainers-go"
	tckafka "github.com/testcontainers/testcontainers-go/modules/kafka"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	inboundkafka "github.com/claudioed/product-master/internal/adapters/inbound/kafka"
	"github.com/claudioed/product-master/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/product-master/internal/adapters/outbound/clock"
	outboundkafka "github.com/claudioed/product-master/internal/adapters/outbound/kafka"
	relay "github.com/claudioed/product-master/internal/adapters/outbound/outbox"
	"github.com/claudioed/product-master/internal/adapters/outbound/postgres"
	"github.com/claudioed/product-master/internal/application/usecases"
	"github.com/claudioed/product-master/internal/domain/product"
)

// End to end against real infrastructure (testcontainers only): an
// inventory-storage-shaped CloudEvent on a real topic is consumed by the
// legacy importer, lands in real Postgres, and product-master's own
// ProductRegistered + ProductClassified leave through the outbox relay.

var (
	kafkaOnce       sync.Once
	sharedBrokers   []string
	sharedContainer testcontainers.Container
	startKafkaErr   error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if sharedContainer != nil {
		if err := testcontainers.TerminateContainer(sharedContainer); err != nil {
			fmt.Fprintf(os.Stderr, "terminate kafka container: %v\n", err)
		}
	}
	os.Exit(code)
}

func startKafka(t *testing.T) []string {
	t.Helper()
	kafkaOnce.Do(func() {
		ctx := context.Background()
		container, err := tckafka.Run(ctx, "confluentinc/confluent-local:7.6.1", tckafka.WithClusterID("product-master-importer-itest"))
		if err != nil {
			startKafkaErr = err
			return
		}
		sharedContainer = container
		sharedBrokers, startKafkaErr = container.Brokers(ctx)
	})
	if startKafkaErr != nil {
		t.Fatalf("start kafka container: %v", startKafkaErr)
	}
	return sharedBrokers
}

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
	pool, err := postgres.NewPool(ctx, url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// createTopic creates a one-partition topic and waits for its leader.
func createTopic(t *testing.T, broker, topic string) {
	t.Helper()
	conn, err := kafkago.Dial("tcp", broker)
	if err != nil {
		t.Fatalf("dial broker: %v", err)
	}
	defer conn.Close()
	if err := conn.CreateTopics(kafkago.TopicConfig{Topic: topic, NumPartitions: 1, ReplicationFactor: 1}); err != nil {
		t.Fatalf("create topic: %v", err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if parts, err := conn.ReadPartitions(topic); err == nil && len(parts) == 1 && parts[0].Leader.ID != 0 {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("topic %s never got a leader", topic)
}

// legacyEvent is the exact structured-mode CloudEvent inventory-storage
// publishes (built with the raw sdk-go event package, as that producer does).
func legacyEvent(t *testing.T, id string, data map[string]any) []byte {
	t.Helper()
	e := ce.New(ce.CloudEventsVersionV1)
	e.SetID(id)
	e.SetSource("/warehouse/inventory-storage")
	e.SetType(inboundkafka.TypeLegacyProductClassified)
	e.SetSubject(fmt.Sprint(data["sku"]))
	e.SetTime(time.Now().UTC())
	e.SetDataSchema("urn:warehouse:inventory-storage:events:ProductClassified:v1")
	if err := e.SetData("application/json", data); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func publish(t *testing.T, brokers []string, topic string, msgs ...kafkago.Message) {
	t.Helper()
	// A fresh Transport per writer: kafka-go's DefaultTransport is shared by
	// every Writer in the process and caches cluster metadata (6s TTL), so a
	// topic created by this test after another test of the package already
	// wrote through it would look unknown ("Unknown Topic Or Partition").
	transport := &kafkago.Transport{}
	defer transport.CloseIdleConnections()
	w := &kafkago.Writer{Addr: kafkago.TCP(brokers...), Topic: topic, Balancer: &kafkago.Hash{}, RequiredAcks: kafkago.RequireAll, BatchTimeout: 10 * time.Millisecond, Transport: transport}
	defer w.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := w.WriteMessages(ctx, msgs...); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

func TestLegacyImporter_EndToEnd(t *testing.T) {
	brokers := startKafka(t)
	pool := startPostgresPool(t)
	stamp := time.Now().UnixNano()
	inTopic, outTopic := fmt.Sprintf("warehouse.inventory.events.itest-%d", stamp), fmt.Sprintf("warehouse.product-master.events.itest-%d", stamp)
	createTopic(t, brokers[0], inTopic)
	createTopic(t, brokers[0], outTopic)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	products := postgres.NewProductRepo(pool)
	uc := &usecases.ImportLegacyClassification{
		Writer: usecases.Writer{
			Products: products, Outbox: postgres.NewOutboxRepo(pool), Encoder: &outboundkafka.Encoder{Topic: outTopic},
			UoW: postgres.NewUnitOfWork(pool), Clock: clock.System{},
		},
		ProcessedEvents: postgres.NewProcessedEventRepo(pool),
	}
	importer := inboundkafka.NewLegacyImporterForTopic(brokers, inTopic, fmt.Sprintf("legacy-import-itest-%d", stamp), uc, logger)
	runInBackground(t, func(ctx context.Context) { _ = importer.Run(ctx) })
	t.Cleanup(func() { _ = importer.Close() })

	sink := outboundkafka.NewRelaySink(brokers)
	t.Cleanup(func() { _ = sink.Close() })
	r := relay.NewRelay(postgres.NewOutboxRepo(pool), sink, logger, relay.WithInterval(100*time.Millisecond))
	runInBackground(t, func(ctx context.Context) { _ = r.Run(ctx) })

	valid := legacyEvent(t, "legacy-1", map[string]any{"sku": "SKU-9", "handling_tags": []string{"Hazmat"}, "dot_hazard_class": 9})
	publish(t, brokers, inTopic,
		kafkago.Message{Key: []byte("x"), Value: []byte(`{"event_id":"flat","event_type":"ProductClassified"}`)},                                     // not a CloudEvent: skipped
		kafkago.Message{Key: []byte("SKU-9"), Value: legacyEvent(t, "legacy-0", map[string]any{"sku": "SKU-9", "handling_tags": []string{"Sharp"}})}, // invalid: skipped
		kafkago.Message{Key: []byte("SKU-9"), Value: valid},
		kafkago.Message{Key: []byte("SKU-9"), Value: valid}, // redelivered id: deduped
	)

	got := readEvents(t, brokers, outTopic, 2)
	prefix := "com.warehouse.wms.product-master.product."
	if got[0].Type() != prefix+"ProductRegistered" || got[1].Type() != prefix+"ProductClassified" {
		t.Fatalf("published = %s, %s", got[0].Type(), got[1].Type())
	}
	var data map[string]any
	if err := got[1].DataAs(&data); err != nil {
		t.Fatal(err)
	}
	if data["classification_source"] != "legacy-import" || data["dot_hazard_class"] != float64(9) || data["version"] != float64(2) {
		t.Fatalf("ProductClassified data = %v", data)
	}

	p, err := products.Get(context.Background(), "SKU-9")
	if err != nil {
		t.Fatal(err)
	}
	c, src, err := p.Classification()
	if err != nil || src != product.SourceLegacyImport || !c.HasTag(product.Hazmat) || p.Version() != 2 {
		t.Fatalf("stored = v%d %v %v %v", p.Version(), src, c.Tags(), err)
	}
	var claims int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM processed_events WHERE event_id = 'legacy-1'`).Scan(&claims); err != nil || claims != 1 {
		t.Fatalf("claims of legacy-1 = %d (%v), want 1", claims, err)
	}
}

func runInBackground(t *testing.T, run func(ctx context.Context)) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
}

// readEvents reads n CloudEvents from topic (partition 0, from the start).
func readEvents(t *testing.T, brokers []string, topic string, n int) []ce.Event {
	t.Helper()
	reader := kafkago.NewReader(kafkago.ReaderConfig{Brokers: brokers, Topic: topic, Partition: 0, MinBytes: 1, MaxBytes: 1 << 20})
	defer reader.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out := make([]ce.Event, 0, n)
	for len(out) < n {
		msg, err := reader.ReadMessage(ctx)
		if err != nil {
			t.Fatalf("read %d of %d: %v", len(out), n, err)
		}
		if string(msg.Key) != "SKU-9" {
			t.Fatalf("key = %q, want SKU-9", msg.Key)
		}
		e, err := cloudevents.Decode(msg.Value)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

//go:build integration

// Integration tests for the outbound Kafka publisher against a REAL broker
// (testcontainers): real domain events raised by the real Product aggregate
// flow through the production publisher — the CloudEvents kafka.Encoder and
// kafka.RelaySink — onto a unique per-test topic, are consumed back with a
// test consumer, and every message must carry the fleet-mandatory
// CloudEvents 1.0 envelope (com.warehouse.wms.product-master.product.<Event>,
// specversion 1.0, id, source) and the pinned payload. Never an external
// KAFKA_BROKERS, never t.Skip, never a hardcoded broker address.
package kafka_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"github.com/testcontainers/testcontainers-go"
	tckafka "github.com/testcontainers/testcontainers-go/modules/kafka"

	"github.com/claudioed/product-master/internal/adapters/kafka/cloudevents"
	outboundkafka "github.com/claudioed/product-master/internal/adapters/outbound/kafka"
	"github.com/claudioed/product-master/internal/domain/product"
)

// One Kafka container serves the whole package (containers are slow to
// boot); every test publishes to its own unique timestamp-suffixed topic so
// tests never share state. The container is terminated in TestMain.
var (
	publisherOnce       sync.Once
	publisherBrokers    []string
	publisherContainer  *tckafka.KafkaContainer
	publisherStartError error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if publisherContainer != nil {
		if err := testcontainers.TerminateContainer(publisherContainer); err != nil {
			fmt.Fprintf(os.Stderr, "terminate kafka container: %v\n", err)
		}
	}
	os.Exit(code)
}

// startPublisherKafka boots the shared broker once per package run.
func startPublisherKafka(t *testing.T) []string {
	t.Helper()
	publisherOnce.Do(func() {
		ctx := context.Background()
		container, err := tckafka.Run(ctx, "confluentinc/confluent-local:7.6.1",
			tckafka.WithClusterID("product-master-publisher-itest"))
		if err != nil {
			publisherStartError = err
			return
		}
		publisherContainer = container
		publisherBrokers, publisherStartError = container.Brokers(ctx)
	})
	if publisherStartError != nil {
		t.Fatalf("start kafka container: %v", publisherStartError)
	}
	return publisherBrokers
}

// createTopicForPublisher creates a one-partition topic and waits for its
// partition leader (CreateTopics returns before the broker finishes).
func createTopicForPublisher(t *testing.T, broker, topic string) {
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

// consume reads every message currently on the topic (a fresh reader at
// offset 0, no group) and hands each to assert; it returns once want
// messages were read or the deadline passes.
func consume(t *testing.T, brokers []string, topic string, want int, assert func(t *testing.T, msg kafkago.Message)) {
	t.Helper()
	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers: brokers, Topic: topic, Partition: 0, MinBytes: 1, MaxBytes: 1 << 20,
	})
	t.Cleanup(func() { _ = reader.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for i := 0; i < want; i++ {
		msg, err := reader.ReadMessage(ctx)
		if err != nil {
			t.Fatalf("read message %d of %d from %s: %v", i+1, want, topic, err)
		}
		assert(t, msg)
	}
}

// envelopeCheck asserts the fleet-mandatory CloudEvents 1.0 envelope of one
// consumed message: specversion 1.0, a non-empty id, the service source,
// the full com.warehouse.<sub>.<ctx>.<entity>.<Event> type, the SKU subject,
// the structured-mode content-type header and the SKU message key.
func envelopeCheck(t *testing.T, msg kafkago.Message, wantType string) kafkago.Message {
	t.Helper()
	if len(msg.Headers) != 1 || msg.Headers[0].Key != "content-type" || string(msg.Headers[0].Value) != cloudevents.MediaType {
		t.Fatalf("headers = %+v, want one content-type %s", msg.Headers, cloudevents.MediaType)
	}
	e, err := cloudevents.Decode(msg.Value)
	if err != nil {
		t.Fatalf("decode CloudEvent: %v (value %s)", err, msg.Value)
	}
	if e.SpecVersion() != cloudevents.SpecVersion {
		t.Fatalf("specversion = %q, want %q", e.SpecVersion(), cloudevents.SpecVersion)
	}
	if e.ID() == "" {
		t.Fatal("CloudEvents id is empty")
	}
	if e.Source() != cloudevents.Source {
		t.Fatalf("source = %q, want %q", e.Source(), cloudevents.Source)
	}
	if e.Type() != wantType {
		t.Fatalf("type = %q, want %q", e.Type(), wantType)
	}
	return msg
}

func TestPublisher_RealKafka_CloudEventsEnvelopeAndPayload(t *testing.T) {
	brokers := startPublisherKafka(t)
	ctx := context.Background()
	topic := fmt.Sprintf("warehouse.product-master.publisher.itest-%d", time.Now().UnixNano())
	createTopicForPublisher(t, brokers[0], topic)

	// Real aggregate, real events, real production encoder and sink: the
	// exact bytes the relay would write, keyed by SKU.
	at := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	p, registered, err := product.Register(product.SKU("SKU-PUB-1"), "Battery", at)
	if err != nil {
		t.Fatal(err)
	}
	classification, err := product.NewClassification(
		[]product.HandlingTag{product.TemperatureSensitive, product.Hazmat}, product.Frozen, 3)
	if err != nil {
		t.Fatal(err)
	}
	classified, err := p.Classify(classification, at)
	if err != nil {
		t.Fatal(err)
	}

	encoder := &outboundkafka.Encoder{Topic: topic}
	msgs, err := encoder.Encode(append(registered, classified...)...)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("encoded %d messages, want 2", len(msgs))
	}
	ids := map[string]bool{}
	for _, m := range msgs {
		if ids[m.EventID] {
			t.Fatalf("duplicate CloudEvents id %s", m.EventID)
		}
		ids[m.EventID] = true
	}

	sink := outboundkafka.NewRelaySink(brokers)
	t.Cleanup(func() { _ = sink.Close() })
	if err := sink.Send(ctx, msgs...); err != nil {
		t.Fatalf("publish through the production relay sink: %v", err)
	}

	prefix := cloudevents.Type(outboundkafka.Entity, "")
	var i int
	consume(t, brokers, topic, 2, func(t *testing.T, msg kafkago.Message) {
		if string(msg.Key) != "SKU-PUB-1" {
			t.Fatalf("key = %q, want the SKU (per-key ordering)", msg.Key)
		}
		switch i {
		case 0:
			envelopeCheck(t, msg, prefix+"ProductRegistered")
			assertRegisteredPayload(t, msg)
		case 1:
			envelopeCheck(t, msg, prefix+"ProductClassified")
			assertClassifiedPayload(t, msg)
		}
		i++
	})
}

// assertRegisteredPayload checks the pinned ProductRegistered data block.
func assertRegisteredPayload(t *testing.T, msg kafkago.Message) {
	t.Helper()
	e, err := cloudevents.Decode(msg.Value)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	var data struct {
		SKU         string `json:"sku"`
		Description string `json:"description"`
		Version     int64  `json:"version"`
	}
	if err := e.DataAs(&data); err != nil {
		t.Fatalf("ProductRegistered data: %v", err)
	}
	if data.SKU != "SKU-PUB-1" || data.Description != "Battery" || data.Version != 1 {
		t.Fatalf("ProductRegistered data = %+v", data)
	}
	if e.Subject() != "SKU-PUB-1" {
		t.Fatalf("subject = %q", e.Subject())
	}
	if ds := e.DataSchema(); ds != "urn:warehouse:product-master:events:ProductRegistered:v1" {
		t.Fatalf("dataschema = %q", ds)
	}
}

// assertClassifiedPayload checks the pinned ProductClassified data block.
func assertClassifiedPayload(t *testing.T, msg kafkago.Message) {
	t.Helper()
	e, err := cloudevents.Decode(msg.Value)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	var data struct {
		SKU                  string   `json:"sku"`
		HandlingTags         []string `json:"handling_tags"`
		TemperatureClass     string   `json:"temperature_class"`
		DOTHazardClass       int      `json:"dot_hazard_class"`
		ClassificationSource string   `json:"classification_source"`
		Version              int64    `json:"version"`
	}
	if err := e.DataAs(&data); err != nil {
		t.Fatalf("ProductClassified data: %v", err)
	}
	if data.SKU != "SKU-PUB-1" || fmt.Sprint(data.HandlingTags) != "[Hazmat TemperatureSensitive]" ||
		data.TemperatureClass != "Frozen" || data.DOTHazardClass != 3 ||
		data.ClassificationSource != "native" || data.Version != 2 {
		t.Fatalf("ProductClassified data = %+v", data)
	}
}

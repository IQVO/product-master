//go:build integration

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
)

// Shared testcontainers Kafka for this package's integration tests (one
// broker per test binary, terminated in TestMain). Kept after the ADR 0003
// legacy importer was retired: the analytics consumer's integration test
// uses these helpers.

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
		container, err := tckafka.Run(ctx, "confluentinc/confluent-local:7.6.1", tckafka.WithClusterID("product-master-itest"))
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

// Package kafkatest boots ONE real Kafka broker through testcontainers per
// test binary (test-support code). It never reads KAFKA_BROKERS, never
// hardcodes a broker address and never skips: a missing Docker is a test
// failure (fleet rule, TestKafkaIntegrationTestsUseTestcontainers).
//
// Minimal port of last-mile-delivery's internal/testing/kafkatest (the
// Brokers/CreateTopic/Produce/Shutdown subset the cmd integration tests
// need), following this repo's own kafka_itest_helpers recipe (unique topic
// per test, explicit CreateTopics + wait for the partition leader before the
// first write).
package kafkatest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"github.com/testcontainers/testcontainers-go"
	tckafka "github.com/testcontainers/testcontainers-go/modules/kafka"
)

var (
	once      sync.Once
	container *tckafka.KafkaContainer
	brokers   []string
	errStart  error
	seq       atomic.Int64
)

// Brokers starts the shared broker on first use and returns its addresses.
func Brokers(t *testing.T) []string {
	t.Helper()
	once.Do(func() {
		ctx := context.Background()
		container, errStart = tckafka.Run(ctx, "confluentinc/confluent-local:7.6.1", tckafka.WithClusterID("product-master-itest"))
		if errStart != nil {
			return
		}
		brokers, errStart = container.Brokers(ctx)
	})
	if errStart != nil {
		t.Fatalf("start kafka container: %v", errStart)
	}
	return brokers
}

// Topic creates a unique one-partition topic named "<prefix>.itest-<n>" and
// waits until it has a leader, then returns its name.
func Topic(t *testing.T, prefix string) string {
	t.Helper()
	topic := fmt.Sprintf("%s.itest-%d-%d", prefix, time.Now().UnixNano(), seq.Add(1))
	CreateTopic(t, topic)
	return topic
}

// CreateTopic creates a one-partition topic and waits for its leader
// (CreateTopics returns before the broker is done).
func CreateTopic(t *testing.T, topic string) {
	t.Helper()
	conn, err := kafkago.Dial("tcp", Brokers(t)[0])
	if err != nil {
		t.Fatalf("dial broker: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.CreateTopics(kafkago.TopicConfig{Topic: topic, NumPartitions: 1, ReplicationFactor: 1}); err != nil {
		t.Fatalf("create topic %s: %v", topic, err)
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

// Message is a produced Kafka message in neutral terms, so test files need
// not touch the Kafka client themselves.
type Message struct {
	Key     string
	Value   []byte
	Headers map[string]string
}

// Produce writes messages to topic (key and headers as given) and waits for
// the broker's acknowledgement.
func Produce(t *testing.T, topic string, msgs ...Message) {
	t.Helper()
	// Its own Transport: kafkago.DefaultTransport caches topic metadata across
	// writers, so a topic created after an earlier Produce would stay unknown.
	w := &kafkago.Writer{Addr: kafkago.TCP(Brokers(t)...), Topic: topic, Balancer: &kafkago.Hash{}, RequiredAcks: kafkago.RequireAll,
		BatchTimeout: 10 * time.Millisecond, Transport: &kafkago.Transport{}}
	defer func() { _ = w.Close() }()
	out := make([]kafkago.Message, len(msgs))
	for i, m := range msgs {
		out[i] = kafkago.Message{Key: []byte(m.Key), Value: m.Value}
		for k, v := range m.Headers {
			out[i].Headers = append(out[i].Headers, kafkago.Header{Key: k, Value: []byte(v)})
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// A just-created topic can still be unknown to the broker that serves the
	// produce request even once its leader is visible, so retry that one error.
	var err error
	for attempt := 0; attempt < 50; attempt++ {
		if err = w.WriteMessages(ctx, out...); err == nil || !errors.Is(err, kafkago.UnknownTopicOrPartition) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("produce to %s: %v", topic, err)
	}
}

// Shutdown terminates the shared broker; call it from TestMain.
func Shutdown() {
	if container != nil {
		_ = testcontainers.TerminateContainer(container)
	}
}

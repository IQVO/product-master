package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	outboxrelay "github.com/claudioed/product-master/internal/adapters/outbound/outbox"
	"github.com/claudioed/product-master/internal/application/outbox"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestParsePublisherMode(t *testing.T) {
	for raw, want := range map[string]string{"": "log", "log": "log", "kafka": "kafka"} {
		got, err := parsePublisherMode(raw)
		if err != nil || got != want {
			t.Errorf("parsePublisherMode(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	if _, err := parsePublisherMode("rabbit"); err == nil {
		t.Error("an unknown EVENT_PUBLISHER must be a config error")
	}
}

func TestParseRelayInterval(t *testing.T) {
	cases := map[string]time.Duration{
		"": time.Second, "250ms": 250 * time.Millisecond, "3s": 3 * time.Second, "2": 2 * time.Second,
		"0.5": 500 * time.Millisecond, "abc": time.Second, "-1s": time.Second, "0": time.Second,
	}
	for raw, want := range cases {
		if got := parseRelayInterval(raw, quietLogger()); got != want {
			t.Errorf("parseRelayInterval(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestShutdownDrainDelay(t *testing.T) {
	cases := map[string]time.Duration{"": DefaultShutdownDrainDelay, "0": 0, "2s": 2 * time.Second, "x": DefaultShutdownDrainDelay, "-1s": DefaultShutdownDrainDelay}
	for raw, want := range cases {
		t.Setenv("SHUTDOWN_DRAIN_DELAY", raw)
		if got := shutdownDrainDelay(quietLogger()); got != want {
			t.Errorf("shutdownDrainDelay(%q) = %v, want %v", raw, got, want)
		}
	}
}

type countingStore struct{ drains chan struct{} }

func (s countingStore) Drain(ctx context.Context, _ int, _ func(context.Context, outbox.Message) error) (int, error) {
	select {
	case s.drains <- struct{}{}:
	default:
	}
	return 0, ctx.Err()
}

var _ outboxrelay.Store = countingStore{}

func TestStartOutboxRelay_DefaultsToTheLogSinkAndStops(t *testing.T) {
	t.Setenv("EVENT_PUBLISHER", "")
	t.Setenv("OUTBOX_RELAY_INTERVAL", "10ms")
	store := countingStore{drains: make(chan struct{}, 1)}
	w, stop, err := startOutboxRelay(store, quietLogger())
	if err != nil {
		t.Fatalf("startOutboxRelay: %v", err)
	}
	select {
	case <-store.drains:
	case <-time.After(5 * time.Second):
		t.Fatal("the relay never drained")
	}
	stop()
	select {
	case <-w.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the relay did not stop")
	}
}

// A broker that is down at boot must not crash or block startup.
func TestStartOutboxRelay_KafkaModeDoesNotDialAtBoot(t *testing.T) {
	t.Setenv("EVENT_PUBLISHER", "kafka")
	t.Setenv("KAFKA_BROKERS", "127.0.0.1:1")
	t.Setenv("OUTBOX_RELAY_INTERVAL", "10ms")
	start := time.Now()
	w, stop, err := startOutboxRelay(countingStore{drains: make(chan struct{}, 1)}, quietLogger())
	if err != nil {
		t.Fatalf("startOutboxRelay with an unreachable broker: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("startup took %v; it must not wait on Kafka", elapsed)
	}
	stop()
	await(quietLogger(), w)
}

func TestStartOutboxRelay_ConfigErrors(t *testing.T) {
	t.Setenv("EVENT_PUBLISHER", "kafka")
	t.Setenv("KAFKA_BROKERS", "")
	if _, _, err := startOutboxRelay(countingStore{}, quietLogger()); err == nil {
		t.Fatal("EVENT_PUBLISHER=kafka needs KAFKA_BROKERS")
	}
	t.Setenv("EVENT_PUBLISHER", "nope")
	if _, _, err := startOutboxRelay(countingStore{}, quietLogger()); err == nil {
		t.Fatal("want an error for an unknown EVENT_PUBLISHER")
	}
}

func TestStartLegacyImporter(t *testing.T) {
	ad, closeFn, err := buildAdapters(context.Background(), "", quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()

	t.Setenv("LEGACY_IMPORT_CONSUMER_GROUP", "")
	w, stop, err := startLegacyImporter(ad, quietLogger())
	if err != nil || w != nil {
		t.Fatalf("unset group: worker=%v err=%v, want not started", w, err)
	}
	stop()

	t.Setenv("LEGACY_IMPORT_CONSUMER_GROUP", "product-master-legacy-import")
	t.Setenv("KAFKA_BROKERS", "")
	if _, _, err := startLegacyImporter(ad, quietLogger()); err == nil {
		t.Fatal("a group without KAFKA_BROKERS must be a config error")
	}

	t.Setenv("KAFKA_BROKERS", "127.0.0.1:1")
	start := time.Now()
	w, stop, err = startLegacyImporter(ad, quietLogger())
	if err != nil || w == nil {
		t.Fatalf("start: %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("starting the importer must not dial Kafka")
	}
	stop()
	select {
	case <-w.done:
	case <-time.After(10 * time.Second):
		t.Fatal("the importer did not stop")
	}
}

func TestBuildAdapters_InMemoryWiresEveryPort(t *testing.T) {
	ad, closeFn, err := buildAdapters(context.Background(), "", quietLogger())
	if err != nil {
		t.Fatalf("buildAdapters: %v", err)
	}
	defer closeFn()
	if ad.products == nil || ad.outbox == nil || ad.outboxStore == nil || ad.processed == nil || ad.uow == nil {
		t.Fatalf("ports not wired: %+v", ad)
	}
	s := buildServer(ad, nil, nil)
	if s.RegisterProduct == nil || s.ListProducts == nil || s.GetProduct == nil || s.RecordMeasurement == nil {
		t.Fatal("use cases not wired")
	}
}

func TestMigrationsDatabaseURL(t *testing.T) {
	const pooled, direct = "postgres://u@pgbouncer:6432/db", "postgres://u@postgres:5432/db"
	t.Setenv("MIGRATIONS_DATABASE_URL", "")
	if got := migrationsDatabaseURL(pooled); got != pooled {
		t.Errorf("unset: got %q", got)
	}
	t.Setenv("MIGRATIONS_DATABASE_URL", direct)
	if got := migrationsDatabaseURL(pooled); got != direct {
		t.Errorf("set: got %q", got)
	}
}

func TestNewLoggerAndGetenv(t *testing.T) {
	for _, lvl := range []string{"debug", "info", "warn", "error", "nonsense"} {
		if newLogger(lvl) == nil {
			t.Fatalf("newLogger(%q) = nil", lvl)
		}
	}
	t.Setenv("PM_TEST_KEY", "")
	if getenv("PM_TEST_KEY", "fallback") != "fallback" {
		t.Fatal("getenv fallback")
	}
	t.Setenv("PM_TEST_KEY", "v")
	if getenv("PM_TEST_KEY", "fallback") != "v" {
		t.Fatal("getenv value")
	}
}

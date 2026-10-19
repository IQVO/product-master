package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/product-master/internal/adapters/outbound/memory"
	outboxrelay "github.com/claudioed/product-master/internal/adapters/outbound/outbox"
	"github.com/claudioed/product-master/internal/application/outbox"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// capturingLogger returns a logger and the buffer it writes to.
func capturingLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, nil)), &buf
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

func (s countingStore) Drain(ctx context.Context, _, _ int, _ func(context.Context, outbox.Message) error) (int, error) {
	select {
	case s.drains <- struct{}{}:
	default:
	}
	return 0, ctx.Err()
}

var _ outboxrelay.Store = countingStore{}

func TestStartOutboxRelay_DrainsAndStops(t *testing.T) {
	t.Setenv("OUTBOX_RELAY_INTERVAL", "10ms")
	store := countingStore{drains: make(chan struct{}, 1)}
	w, stop, err := startOutboxRelay(store, []string{"127.0.0.1:1"}, quietLogger())
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
func TestStartOutboxRelay_DoesNotDialAtBoot(t *testing.T) {
	t.Setenv("OUTBOX_RELAY_INTERVAL", "10ms")
	start := time.Now()
	w, stop, err := startOutboxRelay(countingStore{drains: make(chan struct{}, 1)}, []string{"127.0.0.1:1"}, quietLogger())
	if err != nil {
		t.Fatalf("startOutboxRelay with an unreachable broker: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("startup took %v; it must not wait on Kafka", elapsed)
	}
	stop()
	await(quietLogger(), w)
}

// The relay is not optional: no store or no brokers is an error naming the
// missing variable, never a silent degrade.
func TestStartOutboxRelay_RefusesToBeBuiltWithoutItsDependencies(t *testing.T) {
	cases := []struct {
		name    string
		store   outboxrelay.Store
		brokers []string
		want    string
	}{
		{"no store", nil, []string{"b:9092"}, "DATABASE_URL is required"},
		{"no brokers", countingStore{}, nil, "KAFKA_BROKERS is required"},
		{"empty brokers", countingStore{}, []string{}, "KAFKA_BROKERS is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, stop, err := startOutboxRelay(tc.store, tc.brokers, quietLogger())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("startOutboxRelay = %v, want an error naming %q", err, tc.want)
			}
			if w != nil || stop != nil {
				t.Fatal("a refused relay must not start a worker")
			}
		})
	}
}

// buildServer wires every use case; the ports here are the in-memory TEST
// DOUBLES (the binary never imports them).
func TestBuildServer_WiresEveryUseCase(t *testing.T) {
	products, ob, processed := memory.NewProductRepo(), memory.NewOutboxRepo(), memory.NewProcessedEventRepo()
	ad := adapters{
		products: products, outbox: ob, outboxStore: ob, processed: processed,
		uow: memory.NewUnitOfWork(products, ob, processed),
	}
	s := buildServer(ad, nil, nil, nil)
	if s.RegisterProduct == nil || s.ClassifyProduct == nil || s.DeclareDimensions == nil ||
		s.RecordMeasurement == nil || s.GetProduct == nil || s.ListProducts == nil {
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

package main

import (
	"net"
	"strings"
	"testing"
	"time"
)

// setBootEnv sets a complete, valid mandatory environment; tests then blank
// or corrupt one variable at a time.
func setBootEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://u:x@127.0.0.1:1/db?sslmode=disable")
	t.Setenv("MIGRATIONS_DATABASE_URL", "")
	t.Setenv("KAFKA_BROKERS", "127.0.0.1:1")
	t.Setenv("EVENT_PUBLISHER", "kafka")
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().String()
}

func TestLoadBootConfigAcceptsACompleteEnvironment(t *testing.T) {
	setBootEnv(t)
	cfg, err := loadBootConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.databaseURL == "" || cfg.migrationsURL != cfg.databaseURL || len(cfg.brokers) != 1 {
		t.Fatalf("config = %+v", cfg)
	}
	t.Setenv("MIGRATIONS_DATABASE_URL", "postgres://direct")
	if cfg, _ := loadBootConfig(); cfg.migrationsURL != "postgres://direct" {
		t.Fatalf("the direct migrations DSN must win, got %q", cfg.migrationsURL)
	}
}

// EVENT_PUBLISHER is optional (kafka is the only publisher and the default).
func TestLoadBootConfigAcceptsAnUnsetEventPublisher(t *testing.T) {
	setBootEnv(t)
	t.Setenv("EVENT_PUBLISHER", "")
	if _, err := loadBootConfig(); err != nil {
		t.Fatalf("unset EVENT_PUBLISHER = %v, want the kafka default", err)
	}
}

// Each missing or rejected value is a boot error that names its variable.
func TestLoadBootConfigRefusesEachMissingOrRejectedValue(t *testing.T) {
	cases := []struct {
		name, variable, value, want string
	}{
		{"database unset", "DATABASE_URL", "", "DATABASE_URL is required: this context runs on real Postgres"},
		{"brokers unset", "KAFKA_BROKERS", "", "KAFKA_BROKERS is required: this context runs on real Kafka"},
		{"brokers blank", "KAFKA_BROKERS", " , ", "KAFKA_BROKERS is required"},
		{"log publisher", "EVENT_PUBLISHER", "log", "EVENT_PUBLISHER=log is not supported: this context publishes to real Kafka"},
		{"unknown publisher", "EVENT_PUBLISHER", "rabbit", `unknown EVENT_PUBLISHER "rabbit"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setBootEnv(t)
			t.Setenv(tc.variable, tc.value)
			_, err := loadBootConfig()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("%s=%q: %v, want a boot error containing %q", tc.variable, tc.value, err, tc.want)
			}
			if !strings.Contains(err.Error(), tc.variable) {
				t.Fatalf("the error does not name %s: %v", tc.variable, err)
			}
		})
	}
}

func TestLoadBootConfigReportsEveryProblemAtOnce(t *testing.T) {
	setBootEnv(t)
	t.Setenv("DATABASE_URL", "")
	t.Setenv("KAFKA_BROKERS", "")
	t.Setenv("EVENT_PUBLISHER", "log")
	_, err := loadBootConfig()
	if err == nil {
		t.Fatal("want a boot error")
	}
	for _, name := range []string{"DATABASE_URL", "KAFKA_BROKERS", "EVENT_PUBLISHER"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the error does not name %s: %v", name, err)
		}
	}
}

func TestBuildAdaptersRefusesAnEmptyDatabaseURL(t *testing.T) {
	_, closeAdapters, err := buildAdapters(t.Context(), "", "", quietLogger())
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL is required") {
		t.Fatalf("empty DATABASE_URL = %v; there is no in-memory fallback", err)
	}
	closeAdapters()
}

// run fails fast on a bad configuration: before it opens a connection and
// before it binds the listener.
func TestRunFailsFastBeforeBindingTheListener(t *testing.T) {
	setBootEnv(t)
	t.Setenv("DATABASE_URL", "")
	addr := freeAddr(t)
	t.Setenv("HTTP_ADDR", addr)

	start := time.Now()
	err := run(t.Context())
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL is required") {
		t.Fatalf("run = %v, want a boot error naming DATABASE_URL", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("a configuration error must not wait on any dependency")
	}
	if c, derr := (&net.Dialer{Timeout: 200 * time.Millisecond}).DialContext(t.Context(), "tcp", addr); derr == nil {
		_ = c.Close()
		t.Fatal("the listener was bound although the configuration is invalid")
	}
}

func TestLogTopicsNamesTheProducedTopics(t *testing.T) {
	logger, logs := capturingLogger()
	logTopics(logger, []string{"b:9092"})
	for _, want := range []string{"warehouse.product-master.events", "warehouse.product-master.analytics", "never creates"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("topics log lacks %q: %s", want, logs.String())
		}
	}
}

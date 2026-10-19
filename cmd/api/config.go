package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	outboxrelay "github.com/claudioed/product-master/internal/adapters/outbound/outbox"
)

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// newLogger builds the JSON logger; LOG_LEVEL debug|info|warn|warning|error
// (case-insensitive; "warning" is accepted like "warn" fleet-wide).
func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}

// parseRelayInterval reads OUTBOX_RELAY_INTERVAL (Go duration or plain
// seconds), defaulting when unset, malformed or non-positive.
func parseRelayInterval(raw string, logger *slog.Logger) time.Duration {
	if raw == "" {
		return outboxrelay.DefaultInterval
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		if secs, serr := strconv.ParseFloat(raw, 64); serr == nil {
			d, err = time.Duration(secs*float64(time.Second)), nil
		}
	}
	if err != nil || d <= 0 {
		logger.Warn("invalid OUTBOX_RELAY_INTERVAL; using the default", "value", raw, "default", outboxrelay.DefaultInterval)
		return outboxrelay.DefaultInterval
	}
	return d
}

// outboxMaxAttempts reads OUTBOX_MAX_ATTEMPTS (gap fix D12, ADR 0007): how
// many times an outbox row may fail before the relay excludes it from
// claims. Unset = the fleet default (20); 0 = unlimited; negative or
// unparsable values log a warning and use the default, so a typo never
// silently removes the cap.
func outboxMaxAttempts(raw string, logger *slog.Logger) int {
	if raw == "" {
		return outboxrelay.DefaultMaxAttempts
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		logger.Warn("invalid OUTBOX_MAX_ATTEMPTS; using the default", "value", raw, "default", outboxrelay.DefaultMaxAttempts)
		return outboxrelay.DefaultMaxAttempts
	}
	return n
}

// DefaultShutdownDrainDelay is how long shutdown waits, after flipping
// /readyz to not-ready, before closing the listener (SHUTDOWN_DRAIN_DELAY
// overrides it; "0" disables it).
const DefaultShutdownDrainDelay = 5 * time.Second

// shutdownDrainDelay reads SHUTDOWN_DRAIN_DELAY ("0" disables; unset,
// negative or unparsable = the default).
func shutdownDrainDelay(logger *slog.Logger) time.Duration {
	raw := os.Getenv("SHUTDOWN_DRAIN_DELAY")
	if raw == "" {
		return DefaultShutdownDrainDelay
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		logger.Warn("ignoring invalid SHUTDOWN_DRAIN_DELAY", "value", raw, "default", DefaultShutdownDrainDelay.String())
		return DefaultShutdownDrainDelay
	}
	return d
}

// brokersFromEnv reads KAFKA_BROKERS (comma separated); nil when unset.
func brokersFromEnv() []string {
	var brokers []string
	for _, b := range strings.Split(os.Getenv("KAFKA_BROKERS"), ",") {
		if b = strings.TrimSpace(b); b != "" {
			brokers = append(brokers, b)
		}
	}
	return brokers
}

// Boot errors of the mandatory configuration. Each names its variable.
var (
	errNoDatabase = errors.New("DATABASE_URL is required: this context runs on real Postgres")
	errNoBrokers  = errors.New("KAFKA_BROKERS is required: this context runs on real Kafka")
)

// checkEventPublisher validates EVENT_PUBLISHER. The only publisher is Kafka:
// unset and "kafka" are accepted (the variable is kept because the chart and
// warehouse-infra set it), "log" and anything else is a boot error naming the
// variable.
func checkEventPublisher(raw string) error {
	if raw == "" || raw == "kafka" {
		return nil
	}
	if raw == "log" {
		return errors.New("EVENT_PUBLISHER=log is not supported: this context publishes to real Kafka")
	}
	return fmt.Errorf("unknown EVENT_PUBLISHER %q: this context publishes to real Kafka (want kafka)", raw)
}

// bootConfig is the mandatory configuration of the service: real Postgres and
// real Kafka. There is no default and no degraded mode behind any of it.
type bootConfig struct {
	databaseURL   string
	migrationsURL string
	brokers       []string
}

// loadBootConfig reads and validates the mandatory environment. Every problem
// is reported at once, each naming its variable, so the process fails fast at
// boot, before it opens a connection or binds the listener.
func loadBootConfig() (bootConfig, error) {
	cfg := bootConfig{databaseURL: os.Getenv("DATABASE_URL"), brokers: brokersFromEnv()}
	cfg.migrationsURL = migrationsDatabaseURL(cfg.databaseURL)
	var errs []error
	if cfg.databaseURL == "" {
		errs = append(errs, errNoDatabase)
	}
	if len(cfg.brokers) == 0 {
		errs = append(errs, errNoBrokers)
	}
	if err := checkEventPublisher(os.Getenv("EVENT_PUBLISHER")); err != nil {
		errs = append(errs, err)
	}
	if err := errors.Join(errs...); err != nil {
		return bootConfig{}, fmt.Errorf("invalid configuration: %w", err)
	}
	return cfg, nil
}

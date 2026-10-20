// Command api is product-master's composition root: it wires env config
// into adapters, adapters into use cases, and use cases into the HTTP router
// and the outbox relay.
//
// Postgres and Kafka are the ONLY runtime (ADR 0010): there is no in-memory
// mode, no log sink and no optional relay. A missing mandatory variable is a
// boot error naming it, raised before the listener binds. The service never
// creates topics through an admin API: the cluster broker auto-creates them
// on first write (the relay's writer sets AllowAutoTopicCreation) and
// infrastructure declares none.
//
// Environment:
//
//	DATABASE_URL                  REQUIRED. Postgres DSN
//	MIGRATIONS_DATABASE_URL       direct DSN for the boot migrations (default DATABASE_URL)
//	KAFKA_BROKERS                 REQUIRED. Comma-separated brokers
//	EVENT_PUBLISHER               kafka (default). log is REJECTED at boot
//	HTTP_ADDR                     listen address (default :8080)
//	OUTBOX_RELAY_INTERVAL         relay poll interval (Go duration or seconds, default 1s)
//	OUTBOX_MAX_ATTEMPTS           failures before the relay excludes a row (default 20, 0 = unlimited)
//	OTEL_EXPORTER_OTLP_ENDPOINT   OTLP/gRPC collector (default localhost:4317)
//	SHUTDOWN_DRAIN_DELAY          wait after flipping /readyz before closing (default 5s)
//	LOG_LEVEL, SERVICE_VERSION, ENVIRONMENT, CORS_ALLOWED_ORIGINS
//
// /readyz is the drain gate AND a Postgres ping AND a Kafka metadata check
// (a broker answers a metadata request that names no topic: reachability
// only, never topics), cached for a few seconds. /healthz is liveness only.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	inboundhttp "github.com/claudioed/product-master/internal/adapters/inbound/http"
	outboundkafka "github.com/claudioed/product-master/internal/adapters/outbound/kafka"
	"github.com/claudioed/product-master/internal/adapters/outbound/telemetry"
)

// shutdownTimeout bounds the HTTP server's graceful drain.
const shutdownTimeout = 10 * time.Second

// workerDrainTimeout bounds how long shutdown waits for the relay to return
// after its context is cancelled.
const workerDrainTimeout = 10 * time.Second

func main() {
	if err := run(context.Background()); err != nil {
		slog.Error("service exited with error", "error", err)
		os.Exit(1)
	}
}

// run boots the service and serves until SIGINT/SIGTERM or until ctx is
// cancelled (the in-process tests stop it that way), then shuts down in the
// fleet order. It reads the whole configuration from the environment.
func run(ctx context.Context) error {
	logger := newLogger(getenv("LOG_LEVEL", "info"))
	slog.SetDefault(logger)

	cfg, err := loadBootConfig()
	if err != nil {
		return err
	}
	otelShutdown, err := setupTelemetry(logger)
	if err != nil {
		return err
	}
	defer otelShutdown()

	// closeAdapters (pool.Close) is deferred FIRST so it runs LAST, after
	// the HTTP drain and the relay have stopped.
	ad, closeAdapters, err := buildAdapters(ctx, cfg.databaseURL, cfg.migrationsURL, logger)
	if err != nil {
		return err
	}
	defer closeAdapters()
	logTopics(logger, cfg.brokers)

	metrics, err := telemetry.NewMetricsHandler()
	if err != nil {
		return fmt.Errorf("metrics handler: %w", err)
	}
	// productMetrics is the Tier 2 business counter (ADR-0013). It only
	// fails on an invalid instrument name, a programming error.
	productMetrics, err := telemetry.NewProductMetrics()
	if err != nil {
		return fmt.Errorf("product metrics: %w", err)
	}
	readiness := buildReadiness(ad, outboundkafka.NewBrokerCheck(cfg.brokers).Check)
	httpServer := &http.Server{
		Addr:              getenv("HTTP_ADDR", ":8080"),
		Handler:           inboundhttp.NewRouter(buildServer(ad, readiness, metrics, productMetrics)),
		ReadHeaderTimeout: 5 * time.Second,
	}

	relay, stopRelay, err := startOutboxRelay(ad.outboxStore, cfg.brokers, logger)
	if err != nil {
		return err
	}
	return serveUntilSignal(ctx, logger, httpServer, readiness, relay, stopRelay)
}

// serveUntilSignal runs httpServer until SIGINT/SIGTERM, until ctx is
// cancelled, or until a listen error, then shuts down in the fleet order: (1)
// /readyz flips to 503, (2) wait the drain delay, (3) drain HTTP, (4) stop and
// await the relay LAST so nothing committed above is stranded; the caller's
// deferred pool.Close runs after all of it.
func serveUntilSignal(ctx context.Context, logger *slog.Logger, httpServer *http.Server, readiness *inboundhttp.Readiness,
	relay *worker, stopRelay func(),
) error {
	errCh := make(chan error, 1)
	go func() {
		logger.Info("http server listening", "addr", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	sigCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	select {
	case err := <-errCh:
		stopRelay()
		await(logger, relay)
		return err
	case <-sigCtx.Done():
	}

	readiness.SetNotReady()
	if delay := shutdownDrainDelay(logger); delay > 0 {
		logger.Info("shutdown: readiness flipped to not-ready; waiting for traffic to drain", "drain_delay", delay.String())
		time.Sleep(delay)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	err := httpServer.Shutdown(shutdownCtx)

	stopRelay()
	await(logger, relay)
	return err
}

// await waits (bounded) for w to return; nil w is a no-op.
func await(logger *slog.Logger, w *worker) {
	if w == nil {
		return
	}
	select {
	case <-w.done:
	case <-time.After(workerDrainTimeout):
		logger.Warn("worker did not stop before the shutdown drain deadline", "worker", w.name)
	}
}

// setupTelemetry installs the OTLP trace and metric providers (never blocks
// on a missing Collector) and returns a bounded flush-on-exit closer.
func setupTelemetry(logger *slog.Logger) (func(), error) {
	otelCtx, otelCancel := context.WithTimeout(context.Background(), 10*time.Second)
	otelShutdown, err := telemetry.Setup(otelCtx, inboundhttp.DefaultServiceName, getenv("SERVICE_VERSION", "dev"),
		getenv("OTEL_EXPORTER_OTLP_ENDPOINT", telemetry.DefaultOTLPEndpoint))
	otelCancel()
	if err != nil {
		return nil, fmt.Errorf("telemetry setup: %w", err)
	}
	return func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := otelShutdown(shutdownCtx); err != nil {
			logger.Error("telemetry shutdown failed", "error", err)
		}
	}, nil
}

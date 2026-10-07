// Command api is product-master's composition root: it wires env config
// into adapters, adapters into use cases, and use cases into the HTTP router,
// the legacy importer (ADR 0003) and the outbox relay.
//
// Environment:
//
//	HTTP_ADDR                     listen address (default :8080)
//	DATABASE_URL                  Postgres DSN; unset = in-memory adapters
//	MIGRATIONS_DATABASE_URL       direct DSN for the boot migrations (default DATABASE_URL)
//	EVENT_PUBLISHER               kafka | log (default log)
//	KAFKA_BROKERS                 comma-separated brokers (EVENT_PUBLISHER=kafka, importer)
//	OUTBOX_RELAY_INTERVAL         relay poll interval (Go duration or seconds, default 1s)
//	LEGACY_IMPORT_CONSUMER_GROUP  stable group of the legacy importer; unset = not started
//	OTEL_EXPORTER_OTLP_ENDPOINT   OTLP/gRPC collector (default localhost:4317)
//	SHUTDOWN_DRAIN_DELAY          wait after flipping /readyz before closing (default 5s)
//	LOG_LEVEL, SERVICE_VERSION, ENVIRONMENT, CORS_ALLOWED_ORIGINS
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	inboundhttp "github.com/claudioed/product-master/internal/adapters/inbound/http"
	inboundkafka "github.com/claudioed/product-master/internal/adapters/inbound/kafka"
	"github.com/claudioed/product-master/internal/adapters/outbound/clock"
	outboundkafka "github.com/claudioed/product-master/internal/adapters/outbound/kafka"
	"github.com/claudioed/product-master/internal/adapters/outbound/memory"
	outboxrelay "github.com/claudioed/product-master/internal/adapters/outbound/outbox"
	"github.com/claudioed/product-master/internal/adapters/outbound/postgres"
	"github.com/claudioed/product-master/internal/adapters/outbound/telemetry"
	"github.com/claudioed/product-master/internal/application/ports"
	"github.com/claudioed/product-master/internal/application/usecases"
	"github.com/claudioed/product-master/internal/bootretry"
)

// shutdownTimeout bounds the HTTP server's graceful drain.
const shutdownTimeout = 10 * time.Second

// workerDrainTimeout bounds how long shutdown waits for the importer and the
// relay to return after their context is cancelled.
const workerDrainTimeout = 10 * time.Second

// DefaultShutdownDrainDelay is how long shutdown waits, after flipping
// /readyz to not-ready, before closing the listener (SHUTDOWN_DRAIN_DELAY
// overrides it; "0" disables it).
const DefaultShutdownDrainDelay = 5 * time.Second

func main() {
	if err := run(); err != nil {
		slog.Error("service exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	logger := newLogger(getenv("LOG_LEVEL", "info"))
	slog.SetDefault(logger)

	otelShutdown, err := setupTelemetry(logger)
	if err != nil {
		return err
	}
	defer otelShutdown()

	// closeAdapters (pool.Close) is deferred FIRST so it runs LAST, after
	// the HTTP drain, the importer and the relay have all stopped.
	ad, closeAdapters, err := buildAdapters(context.Background(), os.Getenv("DATABASE_URL"), logger)
	if err != nil {
		return err
	}
	defer closeAdapters()

	metrics, err := telemetry.NewMetricsHandler()
	if err != nil {
		return fmt.Errorf("metrics handler: %w", err)
	}
	readiness := &inboundhttp.Readiness{}
	httpServer := &http.Server{
		Addr:              getenv("HTTP_ADDR", ":8080"),
		Handler:           inboundhttp.NewRouter(buildServer(ad, readiness, metrics)),
		ReadHeaderTimeout: 5 * time.Second,
	}

	importer, closeImporter, err := startLegacyImporter(ad, logger)
	if err != nil {
		return err
	}
	relay, closeRelay, err := startOutboxRelay(ad.outboxStore, logger)
	if err != nil {
		closeImporter()
		return err
	}
	return serveUntilSignal(logger, httpServer, readiness, importer, closeImporter, relay, closeRelay)
}

// adapters is the set of outbound adapters the composition root wires.
type adapters struct {
	products    ports.ProductRepository
	outbox      ports.OutboxRepository
	outboxStore outboxrelay.Store
	processed   ports.ProcessedEvents
	uow         ports.UnitOfWork
}

// writer bundles the ports every write use case needs.
func (a adapters) writer() usecases.Writer {
	return usecases.Writer{Products: a.products, Outbox: a.outbox, Encoder: outboundkafka.NewEncoder(), UoW: a.uow, Clock: clock.System{}}
}

func buildServer(ad adapters, readiness *inboundhttp.Readiness, metrics http.Handler) *inboundhttp.Server {
	w := ad.writer()
	return &inboundhttp.Server{
		RegisterProduct:   &usecases.RegisterProduct{Writer: w},
		ClassifyProduct:   &usecases.ClassifyProduct{Writer: w},
		DeclareDimensions: &usecases.DeclareDimensions{Writer: w},
		RecordMeasurement: &usecases.RecordMeasurement{Writer: w},
		GetProduct:        &usecases.GetProduct{Products: ad.products},
		ListProducts:      &usecases.ListProducts{Products: ad.products},
		Readiness:         readiness,
		Metrics:           metrics,
	}
}

// buildAdapters wires Postgres when databaseURL is set (running the
// embedded migrations first) or in-memory adapters otherwise.
func buildAdapters(ctx context.Context, databaseURL string, logger *slog.Logger) (adapters, func(), error) {
	noop := func() {}
	if databaseURL == "" {
		logger.Info("DATABASE_URL not configured; using in-memory adapters")
		products, ob, processed := memory.NewProductRepo(), memory.NewOutboxRepo(), memory.NewProcessedEventRepo()
		return adapters{
			products: products, outbox: ob, outboxStore: ob, processed: processed,
			uow: memory.NewUnitOfWork(products, ob, processed),
		}, noop, nil
	}

	// The first outbound dial of an injected pod can be reset (Istio native
	// sidecars), so migrations and the first ping retry with backoff; on
	// exhaustion the LAST error is returned and the process refuses to boot.
	migrationsURL := migrationsDatabaseURL(databaseURL)
	if err := bootretry.Retry(ctx, logger, "run migrations", func() error {
		return postgres.RunMigrations(migrationsURL)
	}); err != nil {
		return adapters{}, noop, err
	}
	pool, err := postgres.NewPool(ctx, databaseURL)
	if err != nil {
		return adapters{}, noop, err
	}
	if err := bootretry.Retry(ctx, logger, "ping postgres", func() error { return pool.Ping(ctx) }); err != nil {
		pool.Close()
		return adapters{}, noop, err
	}
	logger.Info("postgres adapters configured")
	ob := postgres.NewOutboxRepo(pool)
	return adapters{
		products: postgres.NewProductRepo(pool), outbox: ob, outboxStore: ob,
		processed: postgres.NewProcessedEventRepo(pool), uow: postgres.NewUnitOfWork(pool),
	}, pool.Close, nil
}

// migrationsDatabaseURL returns MIGRATIONS_DATABASE_URL when set (a direct
// DSN: golang-migrate's advisory lock does not survive PgBouncer transaction
// pooling), else databaseURL.
func migrationsDatabaseURL(databaseURL string) string {
	return getenv("MIGRATIONS_DATABASE_URL", databaseURL)
}

// worker is a started background loop and the channel closed once it
// returned.
type worker struct {
	name string
	done chan struct{}
}

// startLegacyImporter starts the ADR 0003 importer only when
// LEGACY_IMPORT_CONSUMER_GROUP is set (it then needs KAFKA_BROKERS). The
// reader dials lazily inside Run, so a broker outage never blocks boot.
func startLegacyImporter(ad adapters, logger *slog.Logger) (*worker, func(), error) {
	group := os.Getenv("LEGACY_IMPORT_CONSUMER_GROUP")
	if group == "" {
		logger.Info("LEGACY_IMPORT_CONSUMER_GROUP not set; legacy classification importer not started")
		return nil, func() {}, nil
	}
	raw := os.Getenv("KAFKA_BROKERS")
	if raw == "" {
		return nil, nil, errors.New("LEGACY_IMPORT_CONSUMER_GROUP requires KAFKA_BROKERS")
	}
	brokers := strings.Split(raw, ",")
	importUC := &usecases.ImportLegacyClassification{Writer: ad.writer(), ProcessedEvents: ad.processed}
	consumer := inboundkafka.NewLegacyImporter(brokers, group, importUC, logger)

	ctx, stop := context.WithCancel(context.Background())
	w := &worker{name: "legacy-importer", done: make(chan struct{})}
	go func() {
		defer close(w.done)
		logger.Info("legacy classification importer running", "topic", inboundkafka.LegacyTopic, "group_id", group, "brokers", brokers)
		if err := consumer.Run(ctx); !errors.Is(err, context.Canceled) {
			logger.Error("legacy classification importer stopped", "error", err)
		}
	}()
	return w, func() {
		stop()
		_ = consumer.Close()
	}, nil
}

// Event publisher modes (EVENT_PUBLISHER).
const (
	publisherLog   = "log"
	publisherKafka = "kafka"
)

// parsePublisherMode validates EVENT_PUBLISHER ("" and "log" = log sink).
func parsePublisherMode(raw string) (string, error) {
	switch raw {
	case "", publisherLog:
		return publisherLog, nil
	case publisherKafka:
		return publisherKafka, nil
	default:
		return "", fmt.Errorf("unknown EVENT_PUBLISHER %q (want kafka or log)", raw)
	}
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

// startOutboxRelay starts the relay goroutine with the EVENT_PUBLISHER sink.
// Kafka is dialled lazily on the first send, never here.
func startOutboxRelay(store outboxrelay.Store, logger *slog.Logger) (*worker, func(), error) {
	mode, err := parsePublisherMode(os.Getenv("EVENT_PUBLISHER"))
	if err != nil {
		return nil, nil, err
	}
	var (
		sink      outboxrelay.Sink = outboxrelay.LogSink{Logger: logger}
		closeSink                  = func() {}
	)
	if mode == publisherKafka {
		raw := os.Getenv("KAFKA_BROKERS")
		if raw == "" {
			return nil, nil, errors.New("EVENT_PUBLISHER=kafka requires KAFKA_BROKERS")
		}
		kafkaSink := outboundkafka.NewRelaySink(strings.Split(raw, ","))
		sink = kafkaSink
		closeSink = func() {
			if err := kafkaSink.Close(); err != nil {
				logger.Error("kafka relay sink close failed", "error", err)
			}
		}
	}
	interval := parseRelayInterval(os.Getenv("OUTBOX_RELAY_INTERVAL"), logger)
	relay := outboxrelay.NewRelay(store, sink, logger, outboxrelay.WithInterval(interval))

	ctx, stop := context.WithCancel(context.Background())
	w := &worker{name: "outbox-relay", done: make(chan struct{})}
	go func() {
		defer close(w.done)
		defer closeSink()
		logger.Info("outbox relay running", "publisher", mode, "interval", interval, "topic", outboundkafka.Topic)
		if err := relay.Run(ctx); !errors.Is(err, context.Canceled) {
			logger.Error("outbox relay stopped", "error", err)
		}
	}()
	return w, stop, nil
}

// serveUntilSignal runs httpServer until SIGINT/SIGTERM (or a listen error),
// then shuts down in the fleet order: (1) /readyz flips to 503, (2) wait the
// drain delay, (3) drain HTTP, (4) stop and await the importer (an outbox
// writer), (5) stop and await the relay LAST so nothing committed above is
// stranded; the caller's deferred pool.Close runs after all of it.
func serveUntilSignal(logger *slog.Logger, httpServer *http.Server, readiness *inboundhttp.Readiness,
	importer *worker, closeImporter func(), relay *worker, closeRelay func(),
) error {
	errCh := make(chan error, 1)
	go func() {
		logger.Info("http server listening", "addr", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	select {
	case err := <-errCh:
		closeImporter()
		closeRelay()
		return err
	case <-ctx.Done():
	}

	readiness.SetNotReady()
	if delay := shutdownDrainDelay(logger); delay > 0 {
		logger.Info("shutdown: readiness flipped to not-ready; waiting for traffic to drain", "drain_delay", delay.String())
		time.Sleep(delay)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	err := httpServer.Shutdown(shutdownCtx)

	closeImporter()
	await(logger, importer)
	closeRelay()
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

// newLogger builds the JSON logger; LOG_LEVEL debug|info|warn|error.
func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

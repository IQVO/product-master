// Command mcp is the composition root for the product-master MCP server
// (docs/adr/0005-mcp-server-adoption.md): it wires env config to the
// outbound product repository, the repository to the SAME use cases the
// REST adapter uses, and those to the inbound MCP adapter, then serves MCP
// over Streamable HTTP (and nothing else: no stdio, no SSE). It is a second,
// independent deployable alongside cmd/api.
//
// The surface is READ-ONLY except for two tools (ADR 0011 amends ADR 0005):
// declare_pack_level and retire_pack_level run DeclarePackLevel and
// RetirePackLevel, so this binary writes the product row, the pack level rows
// and the encoded events into the transactional outbox in ONE Postgres
// transaction, exactly as cmd/api does. It never starts the outbox relay and
// never dials Kafka (no EVENT_PUBLISHER, no KAFKA_BROKERS): the relay in
// cmd/api publishes what this binary enqueued. Like cmd/api it has no
// in-memory mode (ADR 0010): without DATABASE_URL it refuses to boot.
//
// There is NO auth of any kind (fleet-wide revert 2026-09-11): no keys, no
// bearer checks. Access control is the in-cluster ClusterIP boundary.
//
// Environment:
//
//	DATABASE_URL                 REQUIRED. Postgres DSN
//	MCP_ADDR                     listen address (default :8090)
//	MIGRATIONS_DATABASE_URL      direct DSN for the boot migrations (default DATABASE_URL)
//	OTEL_EXPORTER_OTLP_ENDPOINT  OTLP/gRPC collector (default localhost:4317)
//	LOG_LEVEL, SERVICE_VERSION, ENVIRONMENT
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	inboundmcp "github.com/claudioed/product-master/internal/adapters/inbound/mcp"
	"github.com/claudioed/product-master/internal/adapters/outbound/clock"
	outboundkafka "github.com/claudioed/product-master/internal/adapters/outbound/kafka"
	"github.com/claudioed/product-master/internal/adapters/outbound/postgres"
	"github.com/claudioed/product-master/internal/adapters/outbound/telemetry"
	"github.com/claudioed/product-master/internal/application/usecases"
	"github.com/claudioed/product-master/internal/bootretry"
)

// shutdownTimeout bounds the HTTP server's graceful drain.
const shutdownTimeout = 10 * time.Second

// errNoDatabase is the boot error of a missing DATABASE_URL.
var errNoDatabase = errors.New("DATABASE_URL is required: this context runs on real Postgres")

func main() {
	if err := run(); err != nil {
		slog.Error("mcp server exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	logger := newLogger(getenv("LOG_LEVEL", "info"))
	slog.SetDefault(logger)

	// Fail fast, before telemetry, any connection or the listener.
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return errNoDatabase
	}

	otelCtx, otelCancel := context.WithTimeout(context.Background(), 10*time.Second)
	otelShutdown, err := telemetry.Setup(otelCtx, mcpServiceName, getenv("SERVICE_VERSION", "dev"),
		getenv("OTEL_EXPORTER_OTLP_ENDPOINT", telemetry.DefaultOTLPEndpoint))
	otelCancel()
	if err != nil {
		return err
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := otelShutdown(shutdownCtx); err != nil {
			logger.Error("telemetry shutdown failed", "error", err)
		}
	}()

	deps, closeAdapters, err := buildDeps(context.Background(), logger, databaseURL, getenv("MIGRATIONS_DATABASE_URL", databaseURL))
	if err != nil {
		return err
	}
	defer closeAdapters()

	srv := &http.Server{
		Addr:              getenv("MCP_ADDR", ":8090"),
		Handler:           newRouter(inboundmcp.Handler(inboundmcp.NewServer(deps))),
		ReadHeaderTimeout: 5 * time.Second,
	}
	return serveMCP(logger, srv)
}

// buildDeps migrates and connects Postgres and wires the use cases over it. The returned close func releases the pool. An empty databaseURL is a
// boot error: there is no fallback runtime (ADR 0010).
//
// Like every sibling's cmd/mcp this binary runs the idempotent embedded
// migrations on start, so it can boot against a fresh database before cmd/api
// has; golang-migrate's advisory lock makes concurrent starts safe.
// migrationsDatabaseURL is used ONLY for that step (a DIRECT Postgres DSN where
// DATABASE_URL points at PgBouncer, which cannot honour the session-scoped
// advisory lock); the runtime pool always uses databaseURL.
func buildDeps(ctx context.Context, logger *slog.Logger, databaseURL, migrationsDatabaseURL string) (inboundmcp.Deps, func(), error) {
	noop := func() {}
	if databaseURL == "" {
		return inboundmcp.Deps{}, noop, errNoDatabase
	}
	// The first outbound dial of an injected pod can be reset (Istio native
	// sidecars), so migrations and the first ping retry with backoff; on
	// exhaustion the process refuses to boot.
	if err := bootretry.Retry(ctx, logger, "run migrations", func() error {
		return postgres.RunMigrations(migrationsDatabaseURL)
	}); err != nil {
		return inboundmcp.Deps{}, noop, err
	}
	pool, err := postgres.NewPool(ctx, databaseURL)
	if err != nil {
		return inboundmcp.Deps{}, noop, err
	}
	if err := bootretry.Retry(ctx, logger, "ping postgres", func() error { return pool.Ping(ctx) }); err != nil {
		pool.Close()
		return inboundmcp.Deps{}, noop, err
	}
	logger.Info("postgres product repository configured")
	products := postgres.NewProductRepo(pool)
	return depsFor(usecases.Writer{
		Products: products, Outbox: postgres.NewOutboxRepo(pool), Encoder: outboundkafka.NewFanoutEncoder(),
		UoW: postgres.NewUnitOfWork(pool), Clock: clock.System{},
	}), pool.Close, nil
}

// depsFor wires the use cases over w: the read use cases over w.Products and
// the two write use cases of ADR 0011 over the whole Writer. The tests call it
// with the in-memory test doubles; the binary only with the Postgres ones.
func depsFor(w usecases.Writer) inboundmcp.Deps {
	return inboundmcp.Deps{
		GetProduct:       &usecases.GetProduct{Products: w.Products},
		ListProducts:     &usecases.ListProducts{Products: w.Products},
		ResolveGTIN:      &usecases.ResolveGTIN{Products: w.Products},
		DeclarePackLevel: &usecases.DeclarePackLevel{Writer: w},
		RetirePackLevel:  &usecases.RetirePackLevel{Writer: w},
	}
}

// serveMCP runs srv until it fails or SIGINT/SIGTERM arrives, then drains it
// gracefully.
func serveMCP(logger *slog.Logger, srv *http.Server) error {
	serverErr := make(chan error, 1)
	go func() {
		logger.Info("mcp server listening (Streamable HTTP)", "addr", srv.Addr)
		serverErr <- srv.ListenAndServe()
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-serverErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// newLogger builds the process-wide JSON logger. LOG_LEVEL maps
// debug|info|warn|error (case-insensitive), defaulting to Info.
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

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

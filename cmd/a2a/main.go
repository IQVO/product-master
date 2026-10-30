// Command a2a is the composition root for the product-master A2A agent
// (docs/adr/0012-a2a-inbound-adapter.md): it wires env config to the
// outbound product repository, the repository to the SAME use cases the REST
// and MCP adapters use, and those to the inbound A2A adapter, then serves
// Agent2Agent JSON-RPC over HTTP with the Agent Card at the well-known path.
// It is a third, independent deployable alongside cmd/api and cmd/mcp.
//
// A2A carries DELEGATION of a goal only (product_profile, declare_pack_level),
// never data sync: facts leave as CloudEvents through the transactional
// outbox. declare_pack_level is the one write, and exists because the MCP
// surface already offers it (ADR 0011): this binary writes the product row,
// the pack level rows and the encoded events into the outbox in ONE Postgres
// transaction, exactly as cmd/api and cmd/mcp do. It never starts the outbox
// relay and never dials Kafka (no EVENT_PUBLISHER, no KAFKA_BROKERS): the
// relay in cmd/api publishes what this binary enqueued. Like cmd/api it has
// no in-memory mode (ADR 0010): without DATABASE_URL it refuses to boot.
//
// There is NO auth of any kind (fleet-wide revert 2026-09-11): the Agent Card
// declares no security schemes. Access control is the in-cluster ClusterIP
// boundary.
//
// Environment:
//
//	DATABASE_URL                 REQUIRED. Postgres DSN
//	A2A_AGENT_URL                REQUIRED. Absolute http(s) URL of this agent's
//	                             JSON-RPC endpoint, advertised on the Agent Card
//	A2A_ADDR                     listen address (default :8093)
//	MIGRATIONS_DATABASE_URL      direct DSN for the boot migrations (default DATABASE_URL)
//	OTEL_EXPORTER_OTLP_ENDPOINT  OTLP/gRPC collector (default localhost:4317)
//	LOG_LEVEL, SERVICE_VERSION, ENVIRONMENT
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	inbounda2a "github.com/claudioed/product-master/internal/adapters/inbound/a2a"
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

// errNoAgentURL is the boot error for a missing A2A_AGENT_URL: the Agent Card
// must advertise the absolute URL clients dial (ADR 0012).
var errNoAgentURL = errors.New("A2A_AGENT_URL is required: the Agent Card must advertise the absolute URL of this agent's JSON-RPC endpoint (ADR 0012), e.g. http://<release>-a2a.<ns>.svc.cluster.local:8093/")

// bootConfig is the mandatory configuration of cmd/a2a: real Postgres and the
// URL the card advertises, with no default and no degraded mode behind either.
type bootConfig struct {
	databaseURL string
	agentURL    string
}

// checkAgentURL validates A2A_AGENT_URL: an absolute http(s) URL. The card is
// the discovery contract -- a relative or schemeless URL is a misconfiguration
// every client would hit, so it is refused at boot, never silently served.
func checkAgentURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return errNoAgentURL
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("A2A_AGENT_URL=%q is not an absolute http(s) URL: the Agent Card must advertise the endpoint clients dial (ADR 0012)", raw)
	}
	return nil
}

// loadBootConfig reads and validates the mandatory environment. Every problem
// is reported at once, each naming its variable, so the process fails fast at
// boot, before telemetry, any connection and the listener.
func loadBootConfig() (bootConfig, error) {
	cfg := bootConfig{databaseURL: os.Getenv("DATABASE_URL"), agentURL: strings.TrimSpace(os.Getenv("A2A_AGENT_URL"))}
	var errs []error
	if cfg.databaseURL == "" {
		errs = append(errs, errNoDatabase)
	}
	errs = append(errs, checkAgentURL(cfg.agentURL))
	if err := errors.Join(errs...); err != nil {
		return bootConfig{}, err
	}
	return cfg, nil
}

func main() {
	if err := run(); err != nil {
		slog.Error("a2a agent exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	logger := newLogger(getenv("LOG_LEVEL", "info"))
	slog.SetDefault(logger)

	// Fail fast, before telemetry, any connection or the listener.
	cfg, err := loadBootConfig()
	if err != nil {
		return err
	}

	otelCtx, otelCancel := context.WithTimeout(context.Background(), 10*time.Second)
	otelShutdown, err := telemetry.Setup(otelCtx, a2aServiceName, getenv("SERVICE_VERSION", "dev"),
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

	deps, closeAdapters, err := buildDeps(context.Background(), logger, cfg.databaseURL, getenv("MIGRATIONS_DATABASE_URL", cfg.databaseURL))
	if err != nil {
		return err
	}
	defer closeAdapters()

	card := inbounda2a.Card(inbounda2a.Config{AgentURL: cfg.agentURL, Version: getenv("SERVICE_VERSION", "dev")})
	srv := &http.Server{
		Addr:              getenv("A2A_ADDR", ":8093"),
		Handler:           newRouter(inbounda2a.Handler(inbounda2a.NewExecutor(deps)), card),
		ReadHeaderTimeout: 5 * time.Second,
	}
	return serveA2A(logger, srv)
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
func buildDeps(ctx context.Context, logger *slog.Logger, databaseURL, migrationsDatabaseURL string) (inbounda2a.Deps, func(), error) {
	noop := func() {}
	if databaseURL == "" {
		return inbounda2a.Deps{}, noop, errNoDatabase
	}
	// The first outbound dial of an injected pod can be reset (Istio native
	// sidecars), so migrations and the first ping retry with backoff; on
	// exhaustion the process refuses to boot.
	if err := bootretry.Retry(ctx, logger, "run migrations", func() error {
		return postgres.RunMigrations(migrationsDatabaseURL)
	}); err != nil {
		return inbounda2a.Deps{}, noop, err
	}
	pool, err := postgres.NewPool(ctx, databaseURL)
	if err != nil {
		return inbounda2a.Deps{}, noop, err
	}
	if err := bootretry.Retry(ctx, logger, "ping postgres", func() error { return pool.Ping(ctx) }); err != nil {
		pool.Close()
		return inbounda2a.Deps{}, noop, err
	}
	logger.Info("postgres product repository configured")
	products := postgres.NewProductRepo(pool)
	return depsFor(usecases.Writer{
		Products: products, Outbox: postgres.NewOutboxRepo(pool), Encoder: outboundkafka.NewFanoutEncoder(),
		UoW: postgres.NewUnitOfWork(pool), Clock: clock.System{},
	}), pool.Close, nil
}

// depsFor wires the use cases over w: the two read use cases over w.Products
// and the one write use case (ADR 0011, offered over A2A by ADR 0012) over the
// whole Writer. The tests call it
// with the in-memory test doubles; the binary only with the Postgres ones.
func depsFor(w usecases.Writer) inbounda2a.Deps {
	return inbounda2a.Deps{
		GetProduct:       &usecases.GetProduct{Products: w.Products},
		ResolveGTIN:      &usecases.ResolveGTIN{Products: w.Products},
		DeclarePackLevel: &usecases.DeclarePackLevel{Writer: w},
	}
}

// serveA2A runs srv until it fails or SIGINT/SIGTERM arrives, then drains it
// gracefully.
func serveA2A(logger *slog.Logger, srv *http.Server) error {
	serverErr := make(chan error, 1)
	go func() {
		logger.Info("a2a agent listening (JSON-RPC over HTTP)", "addr", srv.Addr)
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

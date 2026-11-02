package main

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	inboundhttp "github.com/claudioed/product-master/internal/adapters/inbound/http"
	"github.com/claudioed/product-master/internal/adapters/outbound/clock"
	outboundkafka "github.com/claudioed/product-master/internal/adapters/outbound/kafka"
	outboxrelay "github.com/claudioed/product-master/internal/adapters/outbound/outbox"
	"github.com/claudioed/product-master/internal/adapters/outbound/postgres"
	"github.com/claudioed/product-master/internal/application/ports"
	"github.com/claudioed/product-master/internal/application/usecases"
	"github.com/claudioed/product-master/internal/bootretry"
)

// adapters is the set of outbound adapters the composition root wires. They
// are the real ones: Postgres for the repositories, the unit of work and the
// outbox, the system clock. There is no in-memory variant in the binary
// (internal/architecture's TestRuntimeBinaryHasNoInMemoryAdapters).
type adapters struct {
	products    ports.ProductRepository
	outbox      ports.OutboxRepository
	outboxStore outboxrelay.Store
	processed   ports.ProcessedEvents
	uow         ports.UnitOfWork
	// idempotency stores the Idempotency-Key rows of every POST/PATCH.
	idempotency ports.IdempotencyStore
	// ping is the Postgres liveness check behind /readyz.
	ping func(context.Context) error
}

// writer bundles the ports every write use case needs. The FanoutEncoder
// writes each event to the integration AND the analytics topic (two outbox
// rows, one CloudEvents id, one transaction; ADR 0006).
func (a adapters) writer() usecases.Writer {
	return usecases.Writer{Products: a.products, Outbox: a.outbox, Encoder: outboundkafka.NewFanoutEncoder(), UoW: a.uow, Clock: clock.System{}}
}

func buildServer(ad adapters, readiness *inboundhttp.Readiness, metrics http.Handler, productMetrics ports.ProductMetrics) *inboundhttp.Server {
	w := ad.writer()
	return &inboundhttp.Server{
		RegisterProduct:   &usecases.RegisterProduct{Writer: w, Metrics: productMetrics},
		ClassifyProduct:   &usecases.ClassifyProduct{Writer: w},
		DeclareDimensions: &usecases.DeclareDimensions{Writer: w},
		RecordMeasurement: &usecases.RecordMeasurement{Writer: w},
		GetProduct:        &usecases.GetProduct{Products: ad.products},
		ListProducts:      &usecases.ListProducts{Products: ad.products},

		DeclarePackLevel:           &usecases.DeclarePackLevel{Writer: w},
		RetirePackLevel:            &usecases.RetirePackLevel{Writer: w},
		RecordPackLevelMeasurement: &usecases.RecordPackLevelMeasurement{Writer: w},
		ResolveGTIN:                &usecases.ResolveGTIN{Products: ad.products},

		Idempotency: ad.idempotency,
		Readiness:   readiness,
		Metrics:     metrics,
	}
}

// buildReadiness makes /readyz real: the drain gate AND a Postgres ping AND a
// Kafka metadata check (a broker answers a topic-less metadata request;
// reachability only, never topics: the broker auto-creates them on first
// write). The verdict is cached for inboundhttp.DefaultReadinessTTL.
func buildReadiness(a adapters, kafkaCheck func(context.Context) error) *inboundhttp.Readiness {
	return inboundhttp.NewReadiness(0, 0,
		inboundhttp.Dependency{Name: "postgres", Check: a.ping},
		inboundhttp.Dependency{Name: "kafka", Check: kafkaCheck},
	)
}

// buildAdapters runs the embedded migrations against migrationsURL and wires
// the Postgres adapters over databaseURL. The returned func closes what was
// opened. An empty databaseURL is a boot error: there is no fallback runtime.
func buildAdapters(ctx context.Context, databaseURL, migrationsURL string, logger *slog.Logger) (adapters, func(), error) {
	noop := func() {}
	if databaseURL == "" {
		return adapters{}, noop, errNoDatabase
	}

	// The first outbound dial of an injected pod can be reset (Istio native
	// sidecars), so migrations and the first ping retry with backoff; on
	// exhaustion the LAST error is returned and the process refuses to boot.
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
	return postgresAdapters(pool), pool.Close, nil
}

func postgresAdapters(pool *pgxpool.Pool) adapters {
	ob := postgres.NewOutboxRepo(pool)
	return adapters{
		products: postgres.NewProductRepo(pool), outbox: ob, outboxStore: ob,
		processed: postgres.NewProcessedEventRepo(pool), uow: postgres.NewUnitOfWork(pool),
		idempotency: postgres.NewIdempotencyStore(pool),
		ping:        pool.Ping,
	}
}

// migrationsDatabaseURL returns MIGRATIONS_DATABASE_URL when set (a direct
// DSN: golang-migrate's advisory lock does not survive PgBouncer transaction
// pooling), else databaseURL.
func migrationsDatabaseURL(databaseURL string) string {
	return getenv("MIGRATIONS_DATABASE_URL", databaseURL)
}

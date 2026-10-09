---
id: configuration
title: Configuration reference
sidebar_label: Configuration
---

# Configuration reference

Every binary is configured by environment variables only: there is no config
file and no flag. This page lists every variable each binary reads, taken from
its composition root (`cmd/<binary>/main.go`) and from the two adapters that
read the environment themselves (`internal/adapters/inbound/http/server.go`
and `internal/adapters/outbound/telemetry/telemetry.go`).

An empty value is treated like an unset one everywhere (every binary's
`getenv` helper falls back to the default when the value is `""`).

There is no authentication variable on any binary: REST, MCP and the reports
API are unauthenticated fleet-wide, and access control is the cluster boundary.

## `cmd/api` (REST API, outbox relay, legacy importer)

| Variable | Default | Required | Meaning | Source |
| --- | --- | --- | --- | --- |
| `HTTP_ADDR` | `:8080` | no | Listen address of the REST server. | `cmd/api/main.go` (`run`) |
| `DATABASE_URL` | unset | no | Postgres DSN of the OLTP database. Unset means **in-memory** adapters (products, outbox and processed events live in the process and vanish on restart). Set means: run the embedded migrations, open a `pgxpool` (max 10 connections, `statement_timeout` 5s) and ping it, both with the boot retry. | `cmd/api/main.go` (`buildAdapters`), `internal/adapters/outbound/postgres/pool.go` |
| `MIGRATIONS_DATABASE_URL` | value of `DATABASE_URL` | no | DSN used **only** for the golang-migrate step. Point it at a direct Postgres connection when `DATABASE_URL` goes through PgBouncer: golang-migrate's session advisory lock does not survive transaction pooling. The runtime pool always uses `DATABASE_URL`. | `cmd/api/main.go` (`migrationsDatabaseURL`) |
| `EVENT_PUBLISHER` | `log` | no | Sink of the outbox relay. `log` (or empty) logs each drained message and marks it published; `kafka` writes it to Kafka. Any other value stops the boot with `unknown EVENT_PUBLISHER`. | `cmd/api/main.go` (`parsePublisherMode`) |
| `KAFKA_BROKERS` | unset | when `EVENT_PUBLISHER=kafka` or `LEGACY_IMPORT_CONSUMER_GROUP` is set | Comma-separated broker list. Split on `,` without trimming, so do not put spaces after the commas. The boot fails with `EVENT_PUBLISHER=kafka requires KAFKA_BROKERS` or `LEGACY_IMPORT_CONSUMER_GROUP requires KAFKA_BROKERS` when it is missing. Kafka is dialled lazily, so a broker that is down at boot does not stop the process. | `cmd/api/main.go` (`startOutboxRelay`, `startLegacyImporter`) |
| `OUTBOX_RELAY_INTERVAL` | `1s` | no | Sleep between relay passes that did not fill a batch of 100. Go duration (`500ms`, `2s`) or plain seconds (`0.5`, `2`). Malformed or non-positive values log a WARN and use `1s`. | `cmd/api/main.go` (`parseRelayInterval`), `internal/adapters/outbound/outbox/relay.go` |
| `LEGACY_IMPORT_CONSUMER_GROUP` | unset | no | Consumer group of the ADR 0003 legacy classification importer on `warehouse.inventory.events`. Unset means the importer is **not started**. Use a stable id so a rescheduled pod resumes from its committed offsets. | `cmd/api/main.go` (`startLegacyImporter`) |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | no | Wait between flipping `/readyz` to `503` and closing the listener on SIGTERM. Go duration only; `0` disables the wait; negative or unparsable values log a WARN and use `5s`. | `cmd/api/main.go` (`shutdownDrainDelay`) |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | no | OTLP/gRPC collector (insecure) for traces and metrics. Export never blocks: without a collector the telemetry is dropped and the service runs normally. | `cmd/api/main.go` (`setupTelemetry`), `internal/adapters/outbound/telemetry/telemetry.go` |
| `LOG_LEVEL` | `info` | no | `debug`, `info`, `warn` or `error` (case-insensitive); anything else is `info`. Unlike the other three binaries, `cmd/api` does not accept `warning`. | `cmd/api/main.go` (`newLogger`) |
| `SERVICE_VERSION` | `dev` | no | `service.version` resource attribute on every span and metric. The chart sets it to the image tag. | `cmd/api/main.go` (`setupTelemetry`) |
| `ENVIRONMENT` | `local` | no | `deployment.environment.name` resource attribute on every span and metric. | `internal/adapters/outbound/telemetry/telemetry.go` (`Environment`) |
| `CORS_ALLOWED_ORIGINS` | `http://localhost:5173` | no | Comma-separated browser origins allowed by the CORS middleware (entries are trimmed). Allowed methods `GET` and `PUT`, headers `Accept` and `Content-Type`, no credentials. Read once, when the router is built. In the kind cluster Kong's CORS plugin handles browsers, so the chart leaves it unset. | `internal/adapters/inbound/http/server.go` (`corsAllowedOrigins`) |

## `cmd/mcp` (read-only MCP server)

| Variable | Default | Required | Meaning | Source |
| --- | --- | --- | --- | --- |
| `MCP_ADDR` | `:8090` | no | Listen address. MCP Streamable HTTP is served at `/` and `/mcp`, `GET /healthz` beside it. | `cmd/mcp/main.go` (`run`) |
| `DATABASE_URL` | unset | no | The same OLTP database as `cmd/api`. Unset means an **empty** in-memory repository (it is not shared with a separately started `cmd/api`). Set means migrate, connect (max 10 connections) and ping with the boot retry. | `cmd/mcp/main.go` (`buildDeps`) |
| `MIGRATIONS_DATABASE_URL` | value of `DATABASE_URL` | no | Direct DSN for the boot migrations only. `cmd/mcp` runs the same idempotent embedded migrations as `cmd/api`, so it can start first on a fresh database. | `cmd/mcp/main.go` (`run`, `buildDeps`) |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | no | OTLP/gRPC collector. Service name `product-master-mcp`. | `cmd/mcp/main.go`, `cmd/mcp/router.go` |
| `LOG_LEVEL` | `info` | no | `debug`, `info`, `warn`/`warning` or `error`. | `cmd/mcp/main.go` (`newLogger`) |
| `SERVICE_VERSION` | `dev` | no | `service.version` resource attribute. | `cmd/mcp/main.go` |
| `ENVIRONMENT` | `local` | no | `deployment.environment.name` resource attribute. | `internal/adapters/outbound/telemetry/telemetry.go` |

`cmd/mcp` reads no Kafka variable and no `EVENT_PUBLISHER`: it never starts the
outbox relay and never dials a broker.

## `cmd/product-projector` (analytics writer)

| Variable | Default | Required | Meaning | Source |
| --- | --- | --- | --- | --- |
| `ANALYTICS_DATABASE_URL` | none | **yes** | DSN of the separate **analytical** database (`product_master_analytics` in the kind cluster), read-write and **direct** (not PgBouncer): the projector runs the embedded analytical migrations on start. Missing: exits with `ANALYTICS_DATABASE_URL is required`. Pool: max 5 connections, `statement_timeout` 10s. | `cmd/product-projector/main.go` (`loadConfig`), `internal/adapters/outbound/analyticsstore/pool.go` |
| `KAFKA_BROKERS` | none | **yes** | Comma-separated brokers (entries are trimmed, empty ones dropped). Missing: exits with `KAFKA_BROKERS is required`. | `cmd/product-projector/main.go` (`loadConfig`) |
| `ANALYTICS_CONSUMER_GROUP` | `product-master-analytics` | no | The projector's fixed consumer group on `warehouse.product-master.analytics`. A new group id starts from the earliest offset and rebuilds the model from history. | `cmd/product-projector/main.go` (`defaultConsumerGroup`) |
| `ADMIN_ADDR` | `:8091` | no | Listen address of the admin server (`/healthz`, `/readyz` only). | `cmd/product-projector/main.go` |
| `LOG_LEVEL` | `info` | no | `debug`, `info`, `warn`/`warning` or `error`. | `cmd/product-projector/main.go` (`newLogger`) |

The projector does not set up OpenTelemetry, so it reads neither
`OTEL_EXPORTER_OTLP_ENDPOINT`, `SERVICE_VERSION` nor `ENVIRONMENT`, and it never
opens the OLTP database (`DATABASE_URL` is not read).

## `cmd/product-reports` (analytics reader)

| Variable | Default | Required | Meaning | Source |
| --- | --- | --- | --- | --- |
| `ANALYTICS_DATABASE_URL` | none | **yes** | DSN of the analytical database, ideally a read-only role. Missing: exits with `ANALYTICS_DATABASE_URL is required`. The pool also forces `default_transaction_read_only=on` (max 5 connections, `statement_timeout` 15s). In the chart this variable is fed from the secret key `ANALYTICS_READER_DATABASE_URL`. | `cmd/product-reports/main.go`, `internal/adapters/outbound/analyticsstore/pool.go` (`NewReadOnlyPool`) |
| `HTTP_ADDR` | `:8092` | no | Listen address of the reports API. | `cmd/product-reports/main.go` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | no | OTLP/gRPC collector. Service name `product-master-reports`. | `cmd/product-reports/main.go` |
| `LOG_LEVEL` | `info` | no | `debug`, `info`, `warn`/`warning` or `error`. | `cmd/product-reports/main.go` (`newLogger`) |
| `SERVICE_VERSION` | `dev` | no | `service.version` resource attribute. | `cmd/product-reports/main.go` |
| `ENVIRONMENT` | `local` | no | `deployment.environment.name` resource attribute (the chart does not set it for this Deployment, so it reports `local`). | `internal/adapters/outbound/telemetry/telemetry.go` |

## Fixed values (not configurable)

These are constants in code; changing them needs a code change.

| Value | Constant | Source |
| --- | --- | --- |
| Outbox relay batch size: 100 rows per pass | `DefaultBatchSize` | `internal/adapters/outbound/outbox/relay.go` |
| Boot retry: 5 attempts, backoff 1s doubling | `bootretry.Retries`, `bootretry.Delay` | `internal/bootretry/bootretry.go` |
| HTTP graceful drain 10s; worker drain 10s each (importer, relay) | `shutdownTimeout`, `workerDrainTimeout` | `cmd/api/main.go` |
| Consumer retry backoff: 200 ms doubling, capped at 5 s | `DefaultRetryInitial`, `DefaultRetryMax` | `internal/adapters/inbound/kafka/kafka.go` |
| Request body limit 64 KiB; unknown JSON fields rejected | `maxBodyBytes` | `internal/adapters/inbound/http/response.go` |
| List page size: default 100, max 500 | `DefaultListLimit`, `MaxListLimit` | `internal/application/usecases/queries.go` |
| Report range: default 30 days, max 366 days | `DefaultWindow`, `MaxWindow` | `internal/analytics/report/range.go` |
| OTLP metric export interval 30s | `metricExportInterval` | `internal/adapters/outbound/telemetry/telemetry.go` |

## Helm values that map to these variables

`charts/product-master/values.yaml` renders the variables above; the mapping
is:

| Values key | Variable | Binary |
| --- | --- | --- |
| `config.httpAddr` | `HTTP_ADDR` | api |
| `config.logLevel` | `LOG_LEVEL` | api, mcp, projector, reports |
| `config.eventPublisher` | `EVENT_PUBLISHER` (the chart refuses `kafka` without `kafka.enabled`) | api |
| `config.outboxRelayInterval` | `OUTBOX_RELAY_INTERVAL` (not rendered when empty) | api |
| `config.legacyImportConsumerGroup` | `LEGACY_IMPORT_CONSUMER_GROUP` (not rendered when empty) | api |
| `config.shutdownDrainDelay` | `SHUTDOWN_DRAIN_DELAY` (not rendered when empty) | api |
| `config.corsAllowedOrigins` | `CORS_ALLOWED_ORIGINS` (not rendered when empty) | api |
| `environment` | `ENVIRONMENT` | api, mcp |
| `image.tag` (or chart `appVersion`) | `SERVICE_VERSION` | api, mcp, reports |
| `otel.enabled`, `otel.endpoint` | `OTEL_EXPORTER_OTLP_ENDPOINT` | api, mcp, reports |
| `database.existingSecret` / `database.url`, key `database.existingSecretKey` | `DATABASE_URL` | api, mcp |
| key `database.migrationsExistingSecretKey` (optional secret key) | `MIGRATIONS_DATABASE_URL` | api, mcp |
| `kafka.enabled`, `kafka.brokers` | `KAFKA_BROKERS` | api (only when enabled), projector |
| `mcp.httpAddr` | `MCP_ADDR` | mcp |
| `analytics.projector.adminAddr` | `ADMIN_ADDR` | projector |
| `analytics.projector.consumerGroup` | `ANALYTICS_CONSUMER_GROUP` | projector |
| `analytics.reports.httpAddr` | `HTTP_ADDR` | reports |
| `analytics.database.*` (secret keys `ANALYTICS_DATABASE_URL`, `ANALYTICS_READER_DATABASE_URL`) | `ANALYTICS_DATABASE_URL` | projector (read-write key), reports (reader key) |

See the [runbook](/docs/operations/runbook) for how these come together in a
deployment.

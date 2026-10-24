---
id: configuration
title: Configuration reference
sidebar_label: Configuration
---

# Configuration reference

Every binary is configured by environment variables only: there is no config
file and no flag. Postgres and Kafka are the only runtime
([ADR 0010](/docs/adr/0010-postgres-and-kafka-are-the-only-runtime)): the
mandatory variables below have no default and no fallback, and a missing or
rejected value stops the boot with an error that names the variable, before the
listener binds. This page lists every variable each binary reads, taken from
its composition root (`cmd/<binary>/main.go`) and from the two adapters that
read the environment themselves (`internal/adapters/inbound/http/server.go`
and `internal/adapters/outbound/telemetry/telemetry.go`).

An empty value is treated like an unset one everywhere (every binary's
`getenv` helper falls back to the default when the value is `""`).

There is no authentication variable on any binary: REST, MCP and the reports
API are unauthenticated fleet-wide, and access control is the cluster boundary.

## `cmd/api` (REST API, outbox relay)

| Variable | Default | Required | Meaning | Source |
| --- | --- | --- | --- | --- |
| `HTTP_ADDR` | `:8080` | no | Listen address of the REST server. | `cmd/api/main.go` (`run`) |
| `DATABASE_URL` | none | **yes** | Postgres DSN of the OLTP database. Missing: exits with `DATABASE_URL is required: this context runs on real Postgres`. Runs the embedded migrations, opens a `pgxpool` (max 10 connections, `statement_timeout` 5s) and pings it, both with the boot retry. | `cmd/api/config.go` (`loadBootConfig`), `cmd/api/adapters.go` (`buildAdapters`), `internal/adapters/outbound/postgres/pool.go` |
| `MIGRATIONS_DATABASE_URL` | value of `DATABASE_URL` | no | DSN used **only** for the golang-migrate step. Point it at a direct Postgres connection when `DATABASE_URL` goes through PgBouncer: golang-migrate's session advisory lock does not survive transaction pooling. The runtime pool always uses `DATABASE_URL`. | `cmd/api/adapters.go` (`migrationsDatabaseURL`) |
| `KAFKA_BROKERS` | none | **yes** | Comma-separated broker list (entries are trimmed, empty ones dropped). Missing: exits with `KAFKA_BROKERS is required: this context runs on real Kafka`. The outbox relay always runs on it. Kafka is dialled lazily, so a broker that is down at boot does not stop the process; `/readyz` reports it instead. | `cmd/api/config.go` (`brokersFromEnv`, `loadBootConfig`), `cmd/api/relay.go` (`startOutboxRelay`) |
| `EVENT_PUBLISHER` | `kafka` | no | Kept because the chart and warehouse-infra set it. Unset and `kafka` are the only accepted values: `log` stops the boot with `EVENT_PUBLISHER=log is not supported: this context publishes to real Kafka`, anything else with `unknown EVENT_PUBLISHER`. There is no log sink. | `cmd/api/config.go` (`checkEventPublisher`) |
| `OUTBOX_RELAY_INTERVAL` | `1s` | no | Sleep between relay passes that did not fill a batch of 100. Go duration (`500ms`, `2s`) or plain seconds (`0.5`, `2`). Malformed or non-positive values log a WARN and use `1s`. | `cmd/api/config.go` (`parseRelayInterval`), `internal/adapters/outbound/outbox/relay.go` |
| `OUTBOX_MAX_ATTEMPTS` | `20` | no | Relay attempt cap ([ADR 0007](/docs/adr/0007-outbox-relay-attempt-cap)): a row that failed this many times is no longer claimed and is logged once at ERROR; it stays in `outbox_events` until re-armed (`UPDATE outbox_events SET attempts = 0 WHERE id = ...`, see the [runbook](/docs/operations/runbook)). `0` = unlimited. Negative or unparsable values log a WARN and use `20`. Chart: `config.outboxMaxAttempts`. | `cmd/api/config.go` (`outboxMaxAttempts`), `internal/adapters/outbound/outbox/relay.go` |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | no | Wait between flipping `/readyz` to `503` and closing the listener on SIGTERM. Go duration only; `0` disables the wait; negative or unparsable values log a WARN and use `5s`. | `cmd/api/config.go` (`shutdownDrainDelay`) |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | no | OTLP/gRPC collector (insecure) for traces and metrics. Export never blocks: without a collector the telemetry is dropped and the service runs normally. | `cmd/api/main.go` (`setupTelemetry`), `internal/adapters/outbound/telemetry/telemetry.go` |
| `LOG_LEVEL` | `info` | no | `debug`, `info`, `warn`/`warning` or `error` (case-insensitive); anything else is `info`. Every binary accepts `warning` like `warn`. | `cmd/api/config.go` (`newLogger`) |
| `SERVICE_VERSION` | `dev` | no | `service.version` resource attribute on every span and metric. The chart sets it to the image tag. | `cmd/api/main.go` (`setupTelemetry`) |
| `ENVIRONMENT` | `local` | no | `deployment.environment.name` resource attribute on every span and metric. | `internal/adapters/outbound/telemetry/telemetry.go` (`Environment`) |
| `CORS_ALLOWED_ORIGINS` | `http://localhost:5173` (the warehouse-console shell's dev port) | no | Comma-separated browser origins allowed by the CORS middleware (entries are trimmed). Allowed methods `GET` and `PUT`, headers `Accept` and `Content-Type`, no credentials. Read once, when the router is built. Add `http://localhost:5191` to run the standalone remote (`web/`, `npm run dev`) against a local API. In the kind cluster Kong's CORS plugin handles browsers, so the chart leaves it unset. | `internal/adapters/inbound/http/server.go` (`corsAllowedOrigins`) |

## `cmd/mcp` (MCP server; read-only except two pack level tools)

| Variable | Default | Required | Meaning | Source |
| --- | --- | --- | --- | --- |
| `MCP_ADDR` | `:8090` | no | Listen address. MCP Streamable HTTP is served at `/` and `/mcp`, `GET /healthz` beside it. | `cmd/mcp/main.go` (`run`) |
| `DATABASE_URL` | none | **yes** | The same OLTP database as `cmd/api`. Missing: exits with `DATABASE_URL is required: this context runs on real Postgres` (no in-memory repository). Migrates, connects (max 10 connections) and pings with the boot retry. | `cmd/mcp/main.go` (`run`, `buildDeps`) |
| `MIGRATIONS_DATABASE_URL` | value of `DATABASE_URL` | no | Direct DSN for the boot migrations only. `cmd/mcp` runs the same idempotent embedded migrations as `cmd/api`, so it can start first on a fresh database. | `cmd/mcp/main.go` (`run`, `buildDeps`) |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | no | OTLP/gRPC collector. Service name `product-master-mcp`. | `cmd/mcp/main.go`, `cmd/mcp/router.go` |
| `LOG_LEVEL` | `info` | no | `debug`, `info`, `warn`/`warning` or `error`. | `cmd/mcp/main.go` (`newLogger`) |
| `SERVICE_VERSION` | `dev` | no | `service.version` resource attribute. | `cmd/mcp/main.go` |
| `ENVIRONMENT` | `local` | no | `deployment.environment.name` resource attribute. | `internal/adapters/outbound/telemetry/telemetry.go` |

`cmd/mcp` reads no Kafka variable and no `EVENT_PUBLISHER`: it never starts the
outbox relay and never dials a broker. Its two write tools (`declare_pack_level`,
`retire_pack_level`, ADR 0011) insert the pack level rows and the outbox rows
into `DATABASE_URL`'s database in one transaction; the relay in `cmd/api`
publishes them, so the database role of `DATABASE_URL` needs write access to
`products`, `product_pack_levels` and `outbox_events`.

## `cmd/product-projector` (analytics writer)

| Variable | Default | Required | Meaning | Source |
| --- | --- | --- | --- | --- |
| `ANALYTICS_DATABASE_URL` | none | **yes** | DSN of the separate **analytical** database (`product_master_analytics` in the kind cluster), read-write and **direct** (not PgBouncer): the projector runs the embedded analytical migrations on start. Missing: exits with `ANALYTICS_DATABASE_URL is required`. Pool: max 5 connections, `statement_timeout` 10s. | `cmd/product-projector/main.go` (`loadConfig`), `internal/adapters/outbound/analyticsstore/pool.go` |
| `KAFKA_BROKERS` | none | **yes** | Comma-separated brokers (entries are trimmed, empty ones dropped). Missing: exits with `KAFKA_BROKERS is required`. | `cmd/product-projector/main.go` (`loadConfig`) |
| `ANALYTICS_CONSUMER_GROUP` | none | **yes** | The projector's fixed consumer group on `warehouse.product-master.analytics`. Missing: exits with `ANALYTICS_CONSUMER_GROUP is required`. A new group id starts from the earliest offset and rebuilds the model from history. The chart always sets it (`analytics.projector.consumerGroup`, `product-master-analytics`). | `cmd/product-projector/main.go` (`loadConfig`) |
| `ADMIN_ADDR` | `:8091` | no | Listen address of the admin server (`/healthz`, `/readyz` only). | `cmd/product-projector/main.go` |
| `LOG_LEVEL` | `info` | no | `debug`, `info`, `warn`/`warning` or `error`. | `cmd/product-projector/main.go` (`newLogger`) |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | no | OTLP/gRPC collector. Service name `product-master-projector` ([ADR 0008](/docs/adr/0008-projector-telemetry-and-environment)). Export never blocks boot. | `cmd/product-projector/main.go` (`setupTelemetry`) |
| `SERVICE_VERSION` | `dev` | no | `service.version` resource attribute. | `cmd/product-projector/main.go` (`setupTelemetry`) |
| `ENVIRONMENT` | `local` | no | `deployment.environment.name` resource attribute. | `internal/adapters/outbound/telemetry/telemetry.go` |

The projector never opens the OLTP database (`DATABASE_URL` is not read).

## `cmd/product-reports` (analytics reader)

| Variable | Default | Required | Meaning | Source |
| --- | --- | --- | --- | --- |
| `ANALYTICS_DATABASE_URL` | none | **yes** | DSN of the analytical database, ideally a read-only role. Missing: exits with `ANALYTICS_DATABASE_URL is required`. The pool also forces `default_transaction_read_only=on` (max 5 connections, `statement_timeout` 15s). In the chart this variable is fed from the secret key `ANALYTICS_READER_DATABASE_URL`. | `cmd/product-reports/main.go`, `internal/adapters/outbound/analyticsstore/pool.go` (`NewReadOnlyPool`) |
| `HTTP_ADDR` | `:8092` | no | Listen address of the reports API. | `cmd/product-reports/main.go` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | no | OTLP/gRPC collector. Service name `product-master-reports`. | `cmd/product-reports/main.go` |
| `LOG_LEVEL` | `info` | no | `debug`, `info`, `warn`/`warning` or `error`. | `cmd/product-reports/main.go` (`newLogger`) |
| `SERVICE_VERSION` | `dev` | no | `service.version` resource attribute. | `cmd/product-reports/main.go` |
| `ENVIRONMENT` | `local` | no | `deployment.environment.name` resource attribute. The chart sets it from `environment` ([ADR 0008](/docs/adr/0008-projector-telemetry-and-environment)). | `internal/adapters/outbound/telemetry/telemetry.go` |

## Fixed values (not configurable)

These are constants in code; changing them needs a code change.

| Value | Constant | Source |
| --- | --- | --- |
| Outbox relay batch size: 100 rows per pass | `DefaultBatchSize` | `internal/adapters/outbound/outbox/relay.go` |
| Boot retry: 5 attempts, backoff 1s doubling, slept only between attempts (1+2+4+8 = 15 s in total) | `bootretry.Retries`, `bootretry.Delay` | `internal/bootretry/bootretry.go` |
| HTTP graceful drain 10s; relay drain 10s | `shutdownTimeout`, `workerDrainTimeout` | `cmd/api/main.go` |
| `/readyz` dependency checks: verdict cached 3s, one round bounded to 2s | `DefaultReadinessTTL`, `DefaultReadinessTimeout` | `internal/adapters/inbound/http/readiness.go` |
| Consumer retry backoff: 200 ms doubling, capped at 5 s | `DefaultRetryInitial`, `DefaultRetryMax` | `internal/adapters/inbound/kafka/kafka.go` |
| Request body limit 64 KiB; unknown JSON fields rejected | `maxBodyBytes` | `internal/adapters/inbound/http/response.go` |
| List page size: default 100, max 500 | `DefaultListLimit`, `MaxListLimit` | `internal/application/usecases/queries.go` |
| Report range: default 30 days, max 366 days | `DefaultWindow`, `MaxWindow` | `internal/analytics/report/range.go` |
| OTLP metric export interval 30s | `metricExportInterval` | `internal/adapters/outbound/telemetry/telemetry.go` |

## Helm values that map to these variables

`charts/product-master/values.yaml` renders the variables above; the mapping
is. The chart refuses to render (naming the value) without `database.url` or
`database.existingSecret`, without `kafka.brokers`, with `config.eventPublisher`
other than `kafka`, with `kafka.enabled=false`, with a `*_MODE` variable in
`extraEnv`, and with analytics enabled but no analytical DSN source or no
`analytics.projector.consumerGroup`:

| Values key | Variable | Binary |
| --- | --- | --- |
| `config.httpAddr` | `HTTP_ADDR` | api |
| `config.logLevel` | `LOG_LEVEL` | api, mcp, projector, reports |
| `config.eventPublisher` | `EVENT_PUBLISHER` (default `kafka`; the chart refuses any other value) | api |
| `config.outboxRelayInterval` | `OUTBOX_RELAY_INTERVAL` (not rendered when empty) | api |
| `config.outboxMaxAttempts` | `OUTBOX_MAX_ATTEMPTS` (default `20`) | api |
| `config.shutdownDrainDelay` | `SHUTDOWN_DRAIN_DELAY` (not rendered when empty) | api |
| `config.corsAllowedOrigins` | `CORS_ALLOWED_ORIGINS` (not rendered when empty) | api |
| `environment` | `ENVIRONMENT` | api, mcp, projector, reports |
| `image.tag` (or chart `appVersion`) | `SERVICE_VERSION` | api, mcp, projector, reports |
| `otel.enabled`, `otel.endpoint` | `OTEL_EXPORTER_OTLP_ENDPOINT` | api, mcp, projector, reports |
| `database.existingSecret` / `database.url`, key `database.existingSecretKey` | `DATABASE_URL` | api, mcp |
| key `database.migrationsExistingSecretKey` (optional secret key) | `MIGRATIONS_DATABASE_URL` | api, mcp |
| `kafka.brokers` (required, no default; `kafka.enabled` is accepted but `false` is refused) | `KAFKA_BROKERS` | api, projector |
| `mcp.httpAddr` | `MCP_ADDR` | mcp |
| `analytics.projector.adminAddr` | `ADMIN_ADDR` | projector |
| `analytics.projector.consumerGroup` | `ANALYTICS_CONSUMER_GROUP` | projector |
| `analytics.reports.httpAddr` | `HTTP_ADDR` | reports |
| `analytics.database.*` (secret keys `ANALYTICS_DATABASE_URL`, `ANALYTICS_READER_DATABASE_URL`) | `ANALYTICS_DATABASE_URL` | projector (read-write key), reports (reader key) |

See the [runbook](/docs/operations/runbook) for how these come together in a
deployment.

The api Deployment also mounts the chart ConfigMap (`templates/configmap.yaml`:
`HTTP_ADDR`, `LOG_LEVEL`, `EVENT_PUBLISHER`, `ENVIRONMENT`,
`OUTBOX_RELAY_INTERVAL`, `OUTBOX_MAX_ATTEMPTS`, `SHUTDOWN_DRAIN_DELAY`,
`CORS_ALLOWED_ORIGINS`, `OTEL_EXPORTER_OTLP_ENDPOINT`, `KAFKA_BROKERS`; the
optional ones only when set) with `envFrom` (gap fix D14). Every key
in it is read by `cmd/api`. The explicit `env` entries carry the same values
and win for the same key; the `checksum/config` annotation rolls the pods
when the ConfigMap changes. The mcp, projector and reports Deployments do not
mount it. `charts/product-master/tests/render_test.sh` (CI job `chart-tests`)
asserts the mount.

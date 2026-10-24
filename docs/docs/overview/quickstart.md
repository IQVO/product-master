---
id: quickstart
title: Quickstart
sidebar_label: Quickstart
---

# Quickstart

From a fresh clone to a running `product-master` with data in it. Every
command below was taken from the `Makefile`, the four `cmd/*/main.go` files and
`apis/openapi.yaml`; the request and response examples were captured from a
local `cmd/api` run.

## Prerequisites

| Tool | Version | Needed for |
| --- | --- | --- |
| Go | the `go` line of `go.mod` (1.27.x) | building and testing every binary |
| Docker | any recent | `make integration` (testcontainers starts `postgres:16-alpine` and `confluentinc/confluent-local:7.6.1` itself) and a local Postgres |
| `golangci-lint` | v2.14.0 (pinned in `Makefile` and CI) | `make lint`, `make check` |
| `gremlins` | v0.6.0 | `make mutation`, `make mutation-full` |
| `lefthook` | 1.7.0 or newer | the pre-commit and pre-push hooks (`lefthook install` once per clone) |
| Node.js | 20 for the docs site, 22 for `web/` | `docs/` and the `productmaster_mfe` remote |

There is no `docker-compose.yml` in this repo and no `make run` target: the
binaries are started with `go run`. They run on **real Postgres and real
Kafka only** ([ADR 0010](/docs/adr/0010-postgres-and-kafka-are-the-only-runtime)):
there is no in-memory mode and no log sink, and a binary started without its
mandatory variables exits at once, naming the variable.

## Build and test

```bash
make build        # go build ./...
make test         # go test ./... -race (unit, httptest, BDD; no database needed)
make check        # fmt-check vet build lint test -- the pre-push gate
make check-all    # check + coverage (90% gate) + arch-test + bdd
make integration  # go test -tags=integration ./... -race -count=1 (Docker required)
```

`make help` prints every target. The [testing page](/docs/development/testing)
explains what each one covers.

## Run the REST API

`cmd/api` needs a Postgres (`DATABASE_URL`) and a Kafka broker
(`KAFKA_BROKERS`). Start a Postgres, and use the cluster broker (see
[Kafka](#kafka-the-broker)):

```bash
docker run -d --name pm-postgres -e POSTGRES_PASSWORD=postgres -p 5432:5432 postgres:16-alpine
export DATABASE_URL='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable'
export KAFKA_BROKERS=localhost:9092
go run ./cmd/api   # listens on :8080
```

`cmd/api` runs the embedded migrations
(`internal/adapters/outbound/postgres/migrations`) on every start, so the
tables `products`, `outbox_events` and `processed_events` appear on the first
boot. Nothing else needs to be applied by hand. Without `DATABASE_URL` or
`KAFKA_BROKERS` it exits with status 1 before binding the port, reporting every
missing variable at once:

```text
{"level":"ERROR","msg":"service exited with error","error":"invalid configuration: DATABASE_URL is required: this context runs on real Postgres\nKAFKA_BROKERS is required: this context runs on real Kafka"}
```

The boot logs confirm the wiring:

```text
{"level":"INFO","msg":"postgres adapters configured"}
{"level":"INFO","msg":"kafka topics","brokers":["localhost:9092"],"produces":["warehouse.product-master.events","warehouse.product-master.analytics"],"consumes":[],"note":"this service never creates topics; the broker auto-creates them on the first write and readiness does not depend on them"}
{"level":"INFO","msg":"outbox relay running","interval":1000000000,"max_attempts":20,"brokers":["localhost:9092"],"topics":["warehouse.product-master.events","warehouse.product-master.analytics"]}
```

Without an OpenTelemetry Collector on `localhost:4317` you will also see an
INFO line `traces export: exporter export timeout ... connection refused`
every few seconds. It is harmless (export is non-blocking); point
`OTEL_EXPORTER_OTLP_ENDPOINT` at a collector to make it stop.

## Kafka (the broker)

The fleet has **one** Kafka broker: the in-cluster broker of the kind cluster,
reachable from your machine at `localhost:9092` through its external access
listener. There is no docker-compose Kafka any more. The relay writes to
`warehouse.product-master.events` and `warehouse.product-master.analytics`; the
broker auto-creates both topics on the first write, and `/readyz` does not wait
for them.

## First calls (seeding)

There is no seed script or fixture loader: seed data through the API. A
product must be registered before it can be classified or dimensioned.

```bash
# Liveness and readiness
curl -s localhost:8080/healthz        # {"status":"ok"}
curl -s localhost:8080/readyz         # {"status":"ready"}; 503 problem+json naming postgres or kafka when one is unreachable

# Register a SKU: 201 the first time, 200 on a repeat (description replaced or unchanged)
curl -s -X PUT localhost:8080/products/SKU-1 \
  -H 'Content-Type: application/json' -d '{"description":"Kettle 1.7 l"}'

# Classify it: 201 when it had no classification, 200 when replacing one
curl -s -X PUT localhost:8080/products/SKU-1/classification \
  -H 'Content-Type: application/json' -d '{"handlingTags":["Fragile"]}'

# Declare its dimensions (mm and g)
curl -s -X PUT localhost:8080/products/SKU-1/dimensions/declared \
  -H 'Content-Type: application/json' \
  -d '{"lengthMm":250,"widthMm":180,"heightMm":240,"weightG":1450}'

# Record a measurement; weight differs by more than 10%, so discrepancy=true
curl -s -X PUT localhost:8080/products/SKU-1/dimensions/measured \
  -H 'Content-Type: application/json' \
  -d '{"lengthMm":252,"widthMm":181,"heightMm":240,"weightG":1700,"measuredAt":"2026-10-09T10:00:00Z","deviceId":"dim-01"}'

# Read it back
curl -s localhost:8080/products/SKU-1/physical-profile
curl -s 'localhost:8080/products?classified=true&limit=10'
```

The measurement call returned (captured from a local run):

```json
{"declared":{"lengthMm":250,"widthMm":180,"heightMm":240,"weightG":1450,"volumeMm3":10800000},
 "measured":{"lengthMm":252,"widthMm":181,"heightMm":240,"weightG":1700,"volumeMm3":10946880,"measuredAt":"2026-10-09T10:00:00Z","deviceId":"dim-01"},
 "effective":{"lengthMm":252,"widthMm":181,"heightMm":240,"weightG":1700,"volumeMm3":10946880},
 "effectiveSource":"measured","discrepancy":true,"version":4}
```

Every accepted change raises one event, written twice to the outbox (once per
topic, same CloudEvents `id`), and the relay publishes both to Kafka. Read
them back with any consumer on `warehouse.product-master.events`.

Errors are RFC 7807 problem documents, for example an older measurement:

```json
{"type":"https://errors.product-master.warehouse-systems.dev/stale-measurement",
 "title":"A newer measurement is already recorded","status":409,
 "detail":"measurement is older than the current one",
 "instance":"/products/SKU-1/dimensions/measured"}
```

Valid handling tags are `Hazmat`, `Fragile`, `TemperatureSensitive`,
`Oversized` and `HighValue`; `TemperatureSensitive` needs a
`temperatureClass` (`Ambient`, `Chilled` or `Frozen`) and `dotHazardClass`
(1..9) is only allowed with `Hazmat`. The full contract is the generated
[API reference](/docs/api-reference).

## Run the MCP server

```bash
DATABASE_URL="$DATABASE_URL" go run ./cmd/mcp   # :8090, Streamable HTTP at / and /mcp
```

Give it the same `DATABASE_URL` as `cmd/api`; without one it exits with
`service exited with error` / `DATABASE_URL is required: this context runs on
real Postgres`. A raw session with curl:

```bash
curl -s -D - -X POST localhost:8090/mcp \
  -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"curl","version":"0"}}}'
# copy the Mcp-Session-Id response header, then:
curl -s -X POST localhost:8090/mcp \
  -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  -H 'Mcp-Session-Id: <id>' -d '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
```

The eight tools (six read-only, two pack level writes) are listed on [MCP tools](/docs/mcp/tools).

## Run the analytics read side

The projector and the reports server need a **separate** analytical database
and the Kafka broker:

```bash
docker exec pm-postgres createdb -U postgres product_master_analytics
export ANALYTICS_DATABASE_URL='postgres://postgres:postgres@localhost:5432/product_master_analytics?sslmode=disable'
KAFKA_BROKERS=localhost:9092 ANALYTICS_CONSUMER_GROUP=product-master-analytics \
  go run ./cmd/product-projector   # admin :8091 (/healthz, /readyz)
go run ./cmd/product-reports                                   # :8092
curl -s localhost:8092/reports/freshness
curl -s 'localhost:8092/reports/master-data-quality?from=2026-10-01T00:00:00Z&to=2026-10-09T00:00:00Z'
```

Events reach the projector through the relay of `cmd/api`; the projector exits
at boot without `ANALYTICS_CONSUMER_GROUP` (no default).

## Run the console remote

```bash
cd web && npm ci && npm run dev   # productmaster_mfe standalone on :5191
```

`web/` depends on a sibling checkout of `warehouse-ui-kit`
(`file:../../warehouse-ui-kit`), built first. In dev mode the remote calls
`http://localhost:8080` directly, so start `cmd/api` with
`CORS_ALLOWED_ORIGINS=http://localhost:5173,http://localhost:5191`: the
built-in default (`http://localhost:5173`, the console shell's dev port) does
not include the remote's own dev port.

## Build the docs site

```bash
cd docs && npm ci && npm run build
```

Next: [configuration](/docs/operations/configuration) for every variable and
the [runbook](/docs/operations/runbook) for the deployed setup.

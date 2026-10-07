---
id: runtime
title: Runtime and integration mechanics
sidebar_position: 4
---

# Runtime and integration mechanics

## Layers

Hexagonal architecture, enforced by `TestHexagonalArchitecture` and the
fitness tests in `internal/architecture`:

| Layer | Path |
| --- | --- |
| Domain | `internal/domain/product` |
| Application (use cases, ports) | `internal/application/{usecases,ports,repository,outbox}` |
| Inbound adapters | `internal/adapters/inbound/http` (REST), `internal/adapters/inbound/kafka` (legacy importer) |
| Outbound adapters | `internal/adapters/outbound/{postgres,memory,kafka,outbox,clock,telemetry}` |
| CloudEvents helper | `internal/adapters/kafka/cloudevents` |
| Composition root | `cmd/api/main.go` |

The domain imports nothing internal but itself; adapters never import each
other; only `cmd/api` wires them.

## Writing: one unit of work per command

Every write use case (`internal/application/usecases/writer.go`) runs inside
one `ports.UnitOfWork`: load the `Product`, apply the aggregate command, save
it guarded by the loaded version, encode the raised events
(`EventEncoder`) and insert them into `outbox_events`. A command that raises
no event saves and enqueues nothing. With Postgres the unit of work is one
transaction (`internal/adapters/outbound/postgres/unit_of_work.go`); without
`DATABASE_URL` the in-memory adapters provide the same contract.

## Publishing: transactional outbox

- The encoder (`internal/adapters/outbound/kafka/encoder.go`) mints the
  CloudEvents `id` once, at encode time, and the row stores it, so a relay
  retry republishes the same id.
- The relay (`internal/adapters/outbound/outbox/relay.go`) drains every
  `OUTBOX_RELAY_INTERVAL` (default 1s); a full batch (100 rows) is followed
  immediately by another pass. `OutboxRepo.Drain` claims rows with
  `FOR UPDATE SKIP LOCKED`, sends them one at a time in id order, marks each
  published, and stops at the first failure (recording `attempts` and
  `last_error`) so a later event of a SKU never overtakes an earlier one.
- `EVENT_PUBLISHER=kafka` uses `RelaySink`: a topic-less kafka-go writer with
  `RequireAll` acks, a 10 ms batch timeout, the `Hash` balancer on the key (the
  SKU) and auto topic creation. The default `log` sink only logs each message.

## Consuming: the legacy importer (migration only)

- Started only when `LEGACY_IMPORT_CONSUMER_GROUP` is set (it then needs
  `KAFKA_BROKERS`); reads `warehouse.inventory.events`.
- `FetchMessage`, handle, then `CommitMessages`; never `ReadMessage`.
- Deterministic problems (not a CloudEvent, another type, malformed payload,
  invalid classification) are logged at WARN and committed past.
- A transient failure retries the **same** message with capped exponential
  backoff (200 ms doubling to 5 s) until it succeeds or the process stops;
  there is no dead-letter topic. A failed offset commit is retried the same
  way.
- `ImportLegacyClassification` claims the CloudEvents `id` in
  `processed_events` (consumer `legacy-classification-importer`) in the same
  unit of work as the registration, the import and the outbox insert.

## CloudEvents envelope

Structured content mode, Kafka header
`content-type: application/cloudevents+json; charset=UTF-8`,
`source=/warehouse/product-master`, `subject` = Kafka key = the SKU,
`dataschema=urn:warehouse:product-master:events:<EventName>:v1`
(`internal/adapters/kafka/cloudevents/cloudevents.go`, ADR 0004).

## Shutdown order

On SIGINT/SIGTERM `cmd/api`: (1) flips `/readyz` to `503`, (2) waits
`SHUTDOWN_DRAIN_DELAY` (default 5s), (3) drains HTTP (10s), (4) stops and
awaits the importer, (5) stops and awaits the relay last, then closes the
pool, so nothing committed is stranded.

## Observability

OTLP traces and metrics (`OTEL_EXPORTER_OTLP_ENDPOINT`), `otelchi` route spans
and request-duration metrics on every REST route, and `GET /metrics` with the
Go runtime and process collectors (`internal/adapters/outbound/telemetry`).
There is no business metric yet.

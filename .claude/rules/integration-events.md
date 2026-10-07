---
paths:
  - "internal/adapters/**/kafka/**"
  - "internal/adapters/outbound/events/**"
  - "apis/asyncapi*"
---

# Cross-service integration events (Kafka)

This service PUBLISHES product master data on `warehouse.product-master.events`
(through the transactional outbox) and, during the migration only (ADR 0003),
CONSUMES inventory-storage's legacy `ProductClassified` from
`warehouse.inventory.events`. No analytics topic yet (ADR 0004).

## Events: CloudEvents 1.0 is MANDATORY

Every Kafka message this service produces or consumes (integration
`warehouse.<ctx>.events` AND analytics `warehouse.<ctx>.analytics`) is a
CloudEvents 1.0 event in structured content mode. This is a hard fleet rule,
not a preference — there is nothing to "choose" here:

- No flat envelope (`event_id`/`event_type`/`occurred_at`), no dual-write,
  no dual-read, no envelope toggle env var (`EVENT_ENVELOPE_MODE` is gone
  fleet-wide). `internal/architecture/fitness_test.go`'s
  `TestNoEventEnvelopeToggleOrFlatEnvelope` fails CI on any of them.
- Build/validate/(un)marshal with `github.com/cloudevents/sdk-go/v2/event`
  (latest v2) via ONE helper package,
  `internal/adapters/kafka/cloudevents/` — copy it from this template's
  `templates/cloudevents/cloudevents.go.tmpl` (+ its test) and change only
  the per-repo constants. Transport stays `segmentio/kafka-go` (no sdk-go
  protocol/client packages, no hand-rolled CloudEvent structs).
- Every produced message carries the Kafka header
  `content-type: application/cloudevents+json; charset=UTF-8`
  (`cloudevents.ContentTypeHeader()`), next to the W3C trace headers
  (`traceparent`/`tracestate` stay in headers, never duplicated into
  extension attributes). Message key = aggregate id, `kafkago.Hash{}`
  balancer.
- Required attributes: `specversion=1.0`; `id` (UUID v4 minted ONCE per
  domain event and persisted with the outbox row, so redelivery carries
  the same id); `source=/warehouse/<repo>`; `type`; `subject` (aggregate
  instance id, never empty); `time` (domain occurred-at, UTC);
  `datacontenttype=application/json`;
  `dataschema=urn:warehouse:<repo>:<events|analytics>:<EventName>:v<N>`.
  No custom extension attributes without an ADR.
- `type` = `com.warehouse.<subdomain>.<bounded-context>.<entity>.<EventName>`
  (this repo: `com.warehouse.wms.product-master`). The SAME `type` names the
  occurrence on both the integration and the analytics topic; `dataschema`
  names the payload shape. Breaking payload change => new `.v2` type + new
  dataschema version, never mutate an existing one.
- Consumers decode with `cloudevents.Decode` (validates), dispatch on the
  FULL `type` string (never a short name or suffix match), ignore unknown
  types, read `time`/`subject` from attributes and the payload via
  `DataAs`, dedupe on `id`, and DLQ/skip — WARN log + commit past, never
  crash, never block the partition, never fall back to parsing a legacy
  shape — anything that fails CloudEvents validation.
- Tests: a golden exact-JSON test per published `type` (all attributes +
  the `content-type` header); a legacy-flat-message-rejected test per
  consumer; Kafka integration tests via testcontainers only.

Full standard, subdomain table and the fleet's cross-service type
catalogue: the warehouse-docs repo's Event Standard page
(docs/strategic-design/event-standard-cloudevents.md there, not in this repo).
This repo's ADR: `docs/adr/0004-cloudevents-envelope-and-type-catalogue.md`.

### Published types

Topic `warehouse.product-master.events`; `subject` and Kafka key = the SKU.

| `type` | `dataschema` |
| --- | --- |
| `com.warehouse.wms.product-master.product.ProductRegistered` | `urn:warehouse:product-master:events:ProductRegistered:v1` |
| `com.warehouse.wms.product-master.product.ProductDescriptionChanged` | `urn:warehouse:product-master:events:ProductDescriptionChanged:v1` |
| `com.warehouse.wms.product-master.product.ProductClassified` | `urn:warehouse:product-master:events:ProductClassified:v1` |
| `com.warehouse.wms.product-master.product.ProductDimensionsDeclared` | `urn:warehouse:product-master:events:ProductDimensionsDeclared:v1` |
| `com.warehouse.wms.product-master.product.ProductMeasured` | `urn:warehouse:product-master:events:ProductMeasured:v1` |

Every payload carries `sku` and `version`. Consumers keep a local copy per SKU
and apply a message only when `version` > stored version (full-state per
concern: classification, physical profile). `ProductClassified` keeps
inventory-storage v1 field names (`sku`, `handling_tags`, `temperature_class`,
`dot_hazard_class`) plus `classification_source` and `version`. Optional fields
are omitted when unset. Golden exact-JSON tests pin every type.

### Consumed types

| `type` | topic | producer |
| --- | --- | --- |
| `com.warehouse.wms.inventory-storage.product.ProductClassified` | `warehouse.inventory.events` | inventory-storage (legacy, ADR 0003; removed at stage E) |

## Consumer group id

The legacy importer's group id comes from env `LEGACY_IMPORT_CONSUMER_GROUP`
(unset = importer not started). It is a STABLE group (at-least-once,
`FetchMessage` + `CommitMessages` after the unit of work that claims the
CloudEvents `id` and applies the import). Any consumer group id MUST be env-configurable, never a hardcoded string literal --
`internal/architecture/fitness_test.go`'s
TestKafkaConsumerGroupNeverHardcodedInline enforces this (a real incident:
wes-work-planning's hardcoded group id let a locally-run e2e-tests process
silently collide with the live in-cluster Deployment's consumer group on
the shared fleet Kafka broker).

If this consumer replays from FirstOffset on every start to build an
in-memory read model (rather than resuming from a committed offset), the
group id must additionally be UNIQUE PER PROCESS INSTANCE (hostname+PID+
timestamp), not just configurable -- see HARNESS.md's Kafka section for
why a shared group breaks that pattern specifically.

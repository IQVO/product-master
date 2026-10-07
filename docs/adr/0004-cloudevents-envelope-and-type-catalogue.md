# ADR 0004: CloudEvents envelope and type catalogue

## Status

Accepted (2026-10-06).

## Context

Every Kafka message in the fleet is a CloudEvents 1.0 event in structured
mode (fleet rule `.claude/rules/fleet/cloudevents.md`, warehouse-docs
`docs/strategic-design/event-standard-cloudevents.md`). This ADR is the
catalogue of every type product-master publishes or consumes;
`TestEventCatalogueMatchesContract` checks it against `apis/asyncapi.yaml`.

## Decision

### Envelope

- `specversion` `1.0`; `id` a UUID persisted with the outbox row (stable across
  relay retries; consumers dedupe on it); `source` `/warehouse/product-master`;
  `subject` the SKU; `time` the occurred-at instant in UTC;
  `datacontenttype` `application/json`;
  `dataschema` `urn:warehouse:product-master:events:<EventName>:v1`.
- Kafka value = the JSON event format; header
  `content-type: application/cloudevents+json; charset=UTF-8`; Kafka key = the
  SKU, so all events of one product stay ordered on one partition.
- Built and decoded only through `internal/adapters/kafka/cloudevents`.

### Published on `warehouse.product-master.events`

`<entity>` is the raising aggregate, lowercase, no separators: `product`.

| type | raised when |
|---|---|
| `com.warehouse.wms.product-master.product.ProductRegistered` | a SKU is registered |
| `com.warehouse.wms.product-master.product.ProductDescriptionChanged` | a registered product's description changes |
| `com.warehouse.wms.product-master.product.ProductClassified` | a classification is set or replaced |
| `com.warehouse.wms.product-master.product.ProductDimensionsDeclared` | declared dimensions/weight are set or replaced |
| `com.warehouse.wms.product-master.product.ProductMeasured` | a measurement is recorded |

Payload rules:

- Every payload carries `sku` and the aggregate `version` after the change.
  A consumer keeps a copy row per SKU and applies a message only when its
  `version` is greater than the stored one; that makes redelivery, replay and
  out-of-order delivery harmless.
- `ProductClassified.data` = `{sku, handling_tags[], temperature_class?,
  dot_hazard_class?, classification_source, version}`. `handling_tags` is in
  the stable enum order (Hazmat, Fragile, TemperatureSensitive, Oversized,
  HighValue); `temperature_class` and `dot_hazard_class` are omitted when
  unset (absent means none). The first four field names are identical to
  inventory-storage's `ProductClassified` v1, on purpose: consumers migrating
  from that type keep their field mapping.
- `ProductDimensionsDeclared.data` and `ProductMeasured.data` carry the FULL
  physical profile: `{sku, declared?, measured?, effective?, effective_source,
  discrepancy, version}` where `declared`/`effective` are `{length_mm,
  width_mm, height_mm, weight_g, volume_mm3}` and `measured` adds
  `measured_at` and an optional `device_id` (ADR 0002).
- A change that alters nothing (the same classification, the same declared
  dimensions, the same description) raises no event and does not bump
  `version`.
- A breaking payload change is a new `.v2` type and dataschema, never a
  mutation of v1.

### Consumed (migration only, ADR 0003)

| type | topic | consumer group env |
|---|---|---|
| `com.warehouse.wms.inventory-storage.product.ProductClassified` | `warehouse.inventory.events` | `LEGACY_IMPORT_CONSUMER_GROUP` |

Every other type on that topic is ignored. A message that is not a valid
CloudEvent is logged and skipped, never retried forever.

### Not published (yet)

No analytics topic in v1. `warehouse.product-master.analytics`, a projector
and reports follow in a later phase with their own ADR.

## Consequences

- Consumers need nothing but this catalogue and `apis/asyncapi.yaml`; there is
  no shared Go code.
- The `version` field is part of the published contract; consumers rely on it,
  so it can never be reset or reused for a SKU.

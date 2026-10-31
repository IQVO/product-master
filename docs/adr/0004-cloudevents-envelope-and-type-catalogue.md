# ADR 0004: CloudEvents envelope and type catalogue

## Status

Accepted (2026-10-06). Per-key ordering amended by [ADR 0007](./0007-outbox-relay-attempt-cap.md) (a row excluded by the relay attempt cap is overtaken).

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
| `com.warehouse.wms.product-master.product.ProductPackLevelDeclared` | a pack level is declared, or its flags or declared dimensions change (ADR 0011) |
| `com.warehouse.wms.product-master.product.ProductPackLevelRetired` | a pack level is end-dated, by `RetirePackLevel` or because a new GTIN superseded it (ADR 0011) |
| `com.warehouse.wms.product-master.product.ProductPackLevelMeasured` | a measurement of a pack level is recorded (ADR 0011) |

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
- The three pack level types (added 2026-10-24, ADR 0011) are integration-only
  (not on the analytics stream): `ProductPackLevelDeclared.data` =
  `{sku, level, gtin, child_gtin?, qty_of_child?, units_per_pack, dims?,
  is_orderable, is_despatch_unit, effective_from, version}`,
  `ProductPackLevelRetired.data` = `{sku, level, gtin, effective_to, reason,
  version}`, `ProductPackLevelMeasured.data` = `{sku, level, gtin, dims,
  measured_at, device_id?, version}`. `level` is `EACH | INNER | CASE |
  PALLET`; every `gtin` is the 14-digit zero-left-padded string; `dims` is
  `{length_mm, width_mm, height_mm, weight_g}`; the timestamps are RFC 3339
  UTC. Declaring a new GTIN at an active level emits `Retired` (reason
  `superseded`) then `Declared`, one `version` apart. They keep the
  snake_case of the five older payloads.
- A change that alters nothing (the same classification, the same declared
  dimensions, the same description) raises no event and does not bump
  `version`.
- A breaking payload change is a new `.v2` type and dataschema, never a
  mutation of v1.

### Consumed

Nothing. The ADR 0003 legacy importer (`LEGACY_IMPORT_CONSUMER_GROUP`,
inventory-storage's migration-only `ProductClassified`) was removed at stage
E (2026-10-17).

### Published on `warehouse.product-master.analytics` (ADR 0006)

The same five types (the pack level types of ADR 0011 are not on this stream), written by the same outbox in the same transaction as
the integration rows, with the **same `type` and the same `id` per
occurrence**, `subject` and Kafka key = the SKU, and
`dataschema` `urn:warehouse:product-master:analytics:<EventName>:v1`. The
payloads equal the integration payloads. Only this service's
`product-projector` consumes the topic (fixed consumer group from
`ANALYTICS_CONSUMER_GROUP`); poison goes to
`warehouse.product-master.analytics.dlq`. It is not an integration contract:
other contexts read `warehouse.product-master.events`.

| type | analytics dataschema |
|---|---|
| `com.warehouse.wms.product-master.product.ProductRegistered` | `urn:warehouse:product-master:analytics:ProductRegistered:v1` |
| `com.warehouse.wms.product-master.product.ProductDescriptionChanged` | `urn:warehouse:product-master:analytics:ProductDescriptionChanged:v1` |
| `com.warehouse.wms.product-master.product.ProductClassified` | `urn:warehouse:product-master:analytics:ProductClassified:v1` |
| `com.warehouse.wms.product-master.product.ProductDimensionsDeclared` | `urn:warehouse:product-master:analytics:ProductDimensionsDeclared:v1` |
| `com.warehouse.wms.product-master.product.ProductMeasured` | `urn:warehouse:product-master:analytics:ProductMeasured:v1` |

(Updated 2026-10-07: v1 shipped without an analytics topic; ADR 0006 added it.)

## Consequences

- Consumers need nothing but this catalogue and `apis/asyncapi.yaml`; there is
  no shared Go code.
- The `version` field is part of the published contract; consumers rely on it,
  so it can never be reset or reused for a SKU.

---
id: domain-events
title: Domain events
sidebar_label: Domain events
sidebar_position: 21
---

# Domain events

Every event this context publishes. All are CloudEvents 1.0 in
**structured** content mode (`internal/adapters/kafka/cloudevents`: the whole
event is the JSON message value, Kafka header
`content-type: application/cloudevents+json; charset=UTF-8`). Payload fields
come from `apis/asyncapi.yaml` and the payload structs in
`internal/adapters/outbound/kafka/encoder.go`; the
[event catalogue](/docs/api-reference/events) summarises the AsyncAPI file and
[ADR 0004](/docs/adr/0004-cloudevents-envelope-and-type-catalogue) is the type
catalogue (checked against the AsyncAPI file by
`TestEventCatalogueMatchesContract`).

## Published

Raised by the `Product` aggregate (`internal/domain/product/events.go`),
encoded by `internal/adapters/outbound/kafka/encoder.go` and inserted into
`outbox_events` inside the use case's unit of work; the relay in `cmd/api`
publishes them. Common attributes: `source=/warehouse/product-master`,
`subject` = Kafka key = **partition key** = the SKU (`kafkago.Hash`
balancer), `time` = domain occurred-at (UTC),
`dataschema=urn:warehouse:product-master:events:<EventName>:v1`.

| Full CloudEvents type | Topic | Producer use case | Payload (`data`) | Known consumers |
| --- | --- | --- | --- | --- |
| `com.warehouse.wms.product-master.product.ProductRegistered` | `warehouse.product-master.events` | `RegisterProduct` (new SKU) | `sku`, `description`, `version` (1) | `inbound-receiving` (`known_skus` copy, `PRODUCT_CONSUMER_GROUP`) |
| `com.warehouse.wms.product-master.product.ProductDescriptionChanged` | `warehouse.product-master.events` | `RegisterProduct` (existing SKU, different description) | `sku`, `description`, `version` | none |
| `com.warehouse.wms.product-master.product.ProductClassified` | `warehouse.product-master.events` | `ClassifyProduct` | `sku`, `handling_tags[]`, `temperature_class` (omitted when unset), `dot_hazard_class` (omitted when unset), `classification_source`, `version` | `inventory-storage` (`PRODUCT_MASTER_CONSUMER_GROUP`), `order-management`, `wes-work-planning`, `fulfillment-execution` (each `PRODUCT_CLASSIFICATION_CONSUMER_GROUP`), `slotting-optimization` (`PRODUCT_CONSUMER_GROUP`) |
| `com.warehouse.wms.product-master.product.ProductDimensionsDeclared` | `warehouse.product-master.events` | `DeclareDimensions` | full physical profile: `sku`, `declared`, `measured`, `effective` (each omitted when absent), `effective_source`, `discrepancy`, `version` | `slotting-optimization` (`PRODUCT_CONSUMER_GROUP`); the other uses ADR 0002 names are not built |
| `com.warehouse.wms.product-master.product.ProductMeasured` | `warehouse.product-master.events` | `RecordMeasurement` | the same full physical profile | `slotting-optimization` (`PRODUCT_CONSUMER_GROUP`) |

`declared` and `effective` are
`{length_mm, width_mm, height_mm, weight_g, volume_mm3}` (integers); `measured`
adds `measured_at` (RFC 3339, UTC) and
`device_id` (omitted when empty). `handling_tags` is in the stable order
Hazmat, Fragile, TemperatureSensitive, Oversized, HighValue. The first four
`ProductClassified` field names were kept identical to inventory-storage's
former v1 payload, on purpose, so consumers that migrated from it kept their
field mapping (ADR 0004); that type was retired at stage E and nothing
publishes it any more. `classification_source` is `native` for every
classification written since stage E; `legacy-import` appears only for a SKU
whose stored classification the ADR 0003 importer wrote and nobody has
re-classified since.

**Ordering and replay.** Events of one SKU share a partition, so they arrive
in order; the outbox relay stops a pass at the first failure so a later event
never overtakes an earlier one that is still being retried (a row excluded by
the relay attempt cap, ADR 0007, is overtaken). Every payload carries the `version` after the
change: a consumer applies a message only when its `version` is greater than
the stored one, which makes redelivery and replay harmless. A change that
alters nothing raises no event.

### Analytics copy

Every event above is ALSO written, in the same outbox transaction and under
the same CloudEvents `id`, to `warehouse.product-master.analytics`
(dataschema `urn:warehouse:product-master:analytics:<EventName>:v1`, same
payload), consumed only by this service's `product-projector` for the master
data quality report (ADR 0006).

### Not published

No "unclassify" event exists.

## Consumed

None from another context. The ADR 0003 legacy importer, which consumed
`com.warehouse.wms.inventory-storage.product.ProductClassified` from
`warehouse.inventory.events`, was removed at stage E (2026-10-17) together
with `LEGACY_IMPORT_CONSUMER_GROUP`, and inventory-storage no longer publishes
that type. The only consumer in this repository is `cmd/product-projector`,
which reads this context's own `warehouse.product-master.analytics` topic
(see [Runtime](/docs/overview/runtime)).

Source: `internal/domain/product/events.go`,
`internal/adapters/outbound/kafka/encoder.go`,
`internal/adapters/inbound/kafka/kafka.go`,
`cmd/api/main.go`, `apis/asyncapi.yaml`; the consumers' code on their
`develop` branches (see [Downstream consumers](/docs/ecosystem/downstream-consumers)).

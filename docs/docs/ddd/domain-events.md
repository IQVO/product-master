---
id: domain-events
title: Domain events
sidebar_label: Domain events
sidebar_position: 21
---

# Domain events

Every event this context publishes or consumes. All are CloudEvents 1.0 in
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
| `com.warehouse.wms.product-master.product.ProductRegistered` | `warehouse.product-master.events` | `RegisterProduct` (new SKU), `ImportLegacyClassification` (unknown SKU) | `sku`, `description`, `version` (1) | none |
| `com.warehouse.wms.product-master.product.ProductDescriptionChanged` | `warehouse.product-master.events` | `RegisterProduct` (existing SKU, different description) | `sku`, `description`, `version` | none |
| `com.warehouse.wms.product-master.product.ProductClassified` | `warehouse.product-master.events` | `ClassifyProduct`, `ImportLegacyClassification` | `sku`, `handling_tags[]`, `temperature_class` (omitted when unset), `dot_hazard_class` (omitted when unset), `classification_source`, `version` | `inventory-storage` (`PRODUCT_MASTER_CONSUMER_GROUP`), `order-management`, `wes-work-planning`, `fulfillment-execution` (each `PRODUCT_CLASSIFICATION_CONSUMER_GROUP`) |
| `com.warehouse.wms.product-master.product.ProductDimensionsDeclared` | `warehouse.product-master.events` | `DeclareDimensions` | full physical profile: `sku`, `declared`, `measured`, `effective` (each omitted when absent), `effective_source`, `discrepancy`, `version` | none (ADR 0002 names later uses) |
| `com.warehouse.wms.product-master.product.ProductMeasured` | `warehouse.product-master.events` | `RecordMeasurement` | the same full physical profile | none (ADR 0002 names later uses) |

`declared` and `effective` are
`{length_mm, width_mm, height_mm, weight_g, volume_mm3}` (integers); `measured`
adds `measured_at` (RFC 3339, UTC) and
`device_id` (omitted when empty). `handling_tags` is in the stable order
Hazmat, Fragile, TemperatureSensitive, Oversized, HighValue. The first four
`ProductClassified` field names are identical to inventory-storage's legacy v1
payload, on purpose, so consumers migrating from it keep their field mapping
(ADR 0004).

**Ordering and replay.** Events of one SKU share a partition, so they arrive
in order; the outbox relay stops a pass at the first failure so a later event
never overtakes an earlier one. Every payload carries the `version` after the
change: a consumer applies a message only when its `version` is greater than
the stored one, which makes redelivery and replay harmless. A change that
alters nothing raises no event.

Event order for a legacy import of an unknown SKU: `ProductRegistered`
(version 1) then `ProductClassified` (version 2), in one transaction.

### Not published

No analytics topic in v1 (ADR 0004). No "unclassify" event exists.

## Consumed

| Full CloudEvents type | Topic | Producer | Fields used | Effect | Consumer, group env, DLQ |
| --- | --- | --- | --- | --- | --- |
| `com.warehouse.wms.inventory-storage.product.ProductClassified` | `warehouse.inventory.events` | `inventory-storage` (since its ADR 0034 only the one-shot backfill command emits it) | `sku`, `handling_tags`, `temperature_class`, `dot_hazard_class`; CloudEvents `id` | `ImportLegacyClassification`: register an unknown SKU, set the classification with source `legacy-import` unless the current one is `native` | `LegacyImporter`, `LEGACY_IMPORT_CONSUMER_GROUP` (unset = off), no DLQ: deterministic failures are skipped, transient ones retried in place |

The consumer decodes with `cloudevents.Decode`, dispatches on the full `type`,
ignores every other type on the topic, and dedupes on the CloudEvents `id` in
`processed_events` inside the same unit of work as the effect. It is removed at
ADR 0003 stage E.

Source: `internal/domain/product/events.go`,
`internal/adapters/outbound/kafka/encoder.go`,
`internal/adapters/inbound/kafka/legacy_importer.go`, `kafka.go`,
`internal/application/usecases/import_legacy_classification.go`,
`cmd/api/main.go`, `apis/asyncapi.yaml`; the consumers' code on their
`develop` branches (see [Downstream consumers](/docs/ecosystem/downstream-consumers)).

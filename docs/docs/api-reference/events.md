---
id: events
title: Events (AsyncAPI)
sidebar_position: 99
---

# Events

Summary of `apis/asyncapi.yaml` (AsyncAPI 2.6.0, version 1.1.0). The file is
the authority for payload fields;
[ADR 0004](/docs/adr/0004-cloudevents-envelope-and-type-catalogue) is the type
catalogue.

## Envelope

Every message this service publishes is a **CloudEvents 1.0** event in
structured content mode with the Kafka header
`content-type: application/cloudevents+json; charset=UTF-8`.

| Attribute | Value |
| --- | --- |
| `specversion` | `1.0` |
| `id` | UUID minted once per domain event at encode time and persisted with the outbox row; consumers dedupe on it |
| `source` | `/warehouse/product-master` |
| `type` | `com.warehouse.wms.product-master.product.<EventName>` |
| `subject` | the SKU; also the Kafka key, so one product's events stay ordered on one partition |
| `time` | domain occurred-at, UTC |
| `datacontenttype` | `application/json` |
| `dataschema` | `urn:warehouse:product-master:events:<EventName>:v1` |

A breaking payload change gets a new `.v2` type and dataschema; an existing one
is never mutated.

## Published

Topic `warehouse.product-master.events`, written by the outbox relay. Every
payload carries `sku` and the aggregate `version` after the change.

| Type suffix (after `com.warehouse.wms.product-master.product.`) | Raised | Payload (`data`) |
| --- | --- | --- |
| `ProductRegistered` | `PUT /products/{sku}` for a new SKU | `sku`, `description`, `version` (always 1) |
| `ProductDescriptionChanged` | `PUT /products/{sku}` with a different description | `sku`, `description`, `version` |
| `ProductClassified` | `PUT /products/{sku}/classification` | `sku`, `handling_tags[]`, `temperature_class`?, `dot_hazard_class`?, `classification_source` (`native` for every event raised since stage E; the enum keeps `legacy-import`, the source of rows imported before it), `version` |
| `ProductDimensionsDeclared` | `PUT /products/{sku}/dimensions/declared` | full profile: `sku`, `declared`?, `measured`?, `effective`?, `effective_source`, `discrepancy`, `version` |
| `ProductMeasured` | `PUT /products/{sku}/dimensions/measured` | the same full profile |
| `ProductPackLevelDeclared` | `PUT /products/{sku}/pack-levels/{level}` (new level, changed flags or declared dimensions, or a new GTIN) | `sku`, `level`, `gtin`, `child_gtin`?, `qty_of_child`?, `units_per_pack`, `dims`?, `is_orderable`, `is_despatch_unit`, `effective_from`, `version` |
| `ProductPackLevelRetired` | `POST /products/{sku}/pack-levels/{level}:retire`, or `PUT` of a new GTIN at an active level (reason `superseded`, one `version` before the matching `Declared`) | `sku`, `level`, `gtin`, `effective_to`, `reason`, `version` |
| `ProductPackLevelMeasured` | `PUT /products/{sku}/pack-levels/{level}/measured` | `sku`, `level`, `gtin`, `dims`, `measured_at`, `device_id`?, `version` |

`?` marks a field omitted when unset. `handling_tags` is in the stable order
Hazmat, Fragile, TemperatureSensitive, Oversized, HighValue. `declared` and
`effective` are `{length_mm, width_mm, height_mm, weight_g, volume_mm3}`;
`measured` adds `measured_at` and an optional `device_id`. A change that
alters nothing raises no event.

The three pack level types ([ADR 0011](/docs/adr/0011-packaging-hierarchy-with-gs1-gtin))
are integration-only. Every `gtin` is the 14-digit zero-left-padded string, `level` is
`EACH | INNER | CASE | PALLET`, `dims` is `{length_mm, width_mm, height_mm, weight_g}` and
`units_per_pack` is the direct factor to eaches. Keep retired levels in the local copy:
a GTIN on an in-flight ASN still has to resolve.

**Consumer rule.** Keep one row per SKU and apply a message only when its
`version` is greater than the stored one. That makes redelivery, replay and
out-of-order delivery harmless. Payloads are full state per concern, so a
consumer overwrites, it never merges.

## Consumed

Nothing from another context. The ADR 0003 migration importer
(`LEGACY_IMPORT_CONSUMER_GROUP`, inventory-storage's legacy
`ProductClassified`) was removed at stage E (2026-10-17), and inventory-storage
no longer publishes that type. The only consumer in this repository reads this
service's own analytics stream (below).

## Analytics stream (ADR 0006)

The same five unit types (not the three pack level types) are ALSO written, in the same outbox transaction, to
`warehouse.product-master.analytics`: same `type`, same `id` per occurrence,
`dataschema` `urn:warehouse:product-master:analytics:<EventName>:v1`, payloads
equal to the integration payloads. Only this service's `product-projector`
consumes it (fixed consumer group `ANALYTICS_CONSUMER_GROUP`, poison to
`warehouse.product-master.analytics.dlq`) to feed the master data quality
report (`GET /reports/master-data-quality` on `product-reports`). It is not
an integration contract: subscribe to `warehouse.product-master.events`
instead ([ADR 0006](/docs/adr/0006-analytics-read-side)).

## Example

A `ProductClassified` event from the spec:

```json
{
  "specversion": "1.0",
  "id": "0f6d8a2b-3c4e-4d5f-8a9b-7c6d5e4f3a2b",
  "source": "/warehouse/product-master",
  "type": "com.warehouse.wms.product-master.product.ProductClassified",
  "subject": "SKU-1",
  "time": "2026-10-06T21:02:00Z",
  "datacontenttype": "application/json",
  "dataschema": "urn:warehouse:product-master:events:ProductClassified:v1",
  "data": {
    "sku": "SKU-1",
    "handling_tags": ["Hazmat", "TemperatureSensitive"],
    "temperature_class": "Frozen",
    "dot_hazard_class": 3,
    "classification_source": "native",
    "version": 3
  }
}
```

The DDD view of every event (producer use case, partition key, known
consumers) is on [Domain events](/docs/ddd/domain-events). The full spec is at
[`apis/asyncapi.yaml`](https://raw.githubusercontent.com/IQVO/product-master/main/apis/asyncapi.yaml).

---
id: events
title: Events (AsyncAPI)
sidebar_position: 99
---

# Events

Summary of `apis/asyncapi.yaml` (AsyncAPI 2.6.0, version 1.0.0). The file is
the authority for payload fields;
[ADR 0004](/docs/adr/0004-cloudevents-envelope-and-type-catalogue) is the type
catalogue.

## Envelope

Every message, published or consumed, is a **CloudEvents 1.0** event in
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
| `ProductRegistered` | `PUT /products/{sku}` for a new SKU; the legacy importer for an unknown SKU | `sku`, `description`, `version` (always 1) |
| `ProductDescriptionChanged` | `PUT /products/{sku}` with a different description | `sku`, `description`, `version` |
| `ProductClassified` | `PUT /products/{sku}/classification`; the legacy importer | `sku`, `handling_tags[]`, `temperature_class`?, `dot_hazard_class`?, `classification_source` (`native` or `legacy-import`), `version` |
| `ProductDimensionsDeclared` | `PUT /products/{sku}/dimensions/declared` | full profile: `sku`, `declared`?, `measured`?, `effective`?, `effective_source`, `discrepancy`, `version` |
| `ProductMeasured` | `PUT /products/{sku}/dimensions/measured` | the same full profile |

`?` marks a field omitted when unset. `handling_tags` is in the stable order
Hazmat, Fragile, TemperatureSensitive, Oversized, HighValue. `declared` and
`effective` are `{length_mm, width_mm, height_mm, weight_g, volume_mm3}`;
`measured` adds `measured_at` and an optional `device_id`. A change that
alters nothing raises no event.

**Consumer rule.** Keep one row per SKU and apply a message only when its
`version` is greater than the stored one. That makes redelivery, replay and
out-of-order delivery harmless. Payloads are full state per concern, so a
consumer overwrites, it never merges.

## Consumed (migration only)

| Type | Topic | Producer | Consumer group env var |
| --- | --- | --- | --- |
| `com.warehouse.wms.inventory-storage.product.ProductClassified` | `warehouse.inventory.events` | inventory-storage | `LEGACY_IMPORT_CONSUMER_GROUP` (unset = importer not started) |

Every other type on that topic is ignored; a message that is not a valid
CloudEvent is logged and skipped. The importer is removed at migration stage E
([ADR 0003](/docs/adr/0003-migration-from-inventory-storage)). Details on
[Upstream contracts](/docs/ecosystem/upstream-contracts).

No analytics topic exists in v1 (ADR 0004, "Not published (yet)").

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

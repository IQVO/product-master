---
id: use-cases
title: Use cases
sidebar_label: Use cases
sidebar_position: 2
---

# Use cases

Application services in `internal/application/usecases`. The four write use
cases embed `Writer` (`writer.go`), which runs load, command, version-guarded
save and outbox insert in one `ports.UnitOfWork`.

| Use case | Kind | Inbound | What it does |
| --- | --- | --- | --- |
| `RegisterProduct` | command | `PUT /products/{sku}` | Registers an unknown SKU at version 1 (`201`, `ProductRegistered`) or replaces an existing product's description (`200`, `ProductDescriptionChanged`, or no event when unchanged). |
| `ClassifyProduct` | command | `PUT /products/{sku}/classification` | Validates the classification **before** loading (so an invalid body is `400` even for an unknown SKU), then sets or replaces it with source `native`. `201` when the product had no classification, else `200`. `404` for an unregistered SKU. |
| `DeclareDimensions` | command | `PUT /products/{sku}/dimensions/declared` | Validates the bounds, then sets or replaces the declared dimensions (`ProductDimensionsDeclared`). `404` for an unregistered SKU. |
| `RecordMeasurement` | command | `PUT /products/{sku}/dimensions/measured` | Validates bounds, `measuredAt` (present, not in the future by the service clock) and `deviceId`, then records the latest measurement (`ProductMeasured`). `409 stale-measurement` for an older reading. |
| `ImportLegacyClassification` | command | legacy importer (Kafka) | Migration only (ADR 0003): claims the CloudEvents id, registers an unknown SKU with an empty description, applies the classification with source `legacy-import` unless the current one is `native`. Returns `applied`, `unchanged` or `duplicate`; an invalid message is `ErrInvalidLegacyImport`. |
| `GetProduct` | query | `GET /products/{sku}`, `GET /products/{sku}/classification`, `GET /products/{sku}/physical-profile` | Loads one product; the handlers render the whole product, its classification (`404 product-classification-not-found` when unclassified) or its physical profile. |
| `ListProducts` | query | `GET /products` | Pages by SKU in byte order (`limit` 1..500, default 100; opaque cursor = base64url of the last SKU), optionally filtered by `handlingTag` and `classified`. |

Every write is idempotent: the same request twice changes nothing the second
time. A version race between two writers is `repository.ErrConcurrentModification`
(`409 concurrent-modification`).

## Triggers, inputs, invariants and events

Each event below is written to the outbox twice in the same transaction (once
for `warehouse.product-master.events`, once for
`warehouse.product-master.analytics`) under one CloudEvents `id`, with type
`com.warehouse.wms.product-master.product.<EventName>`. A command that changes
nothing raises no event and does not bump `version`.

| Use case | Trigger | Inputs | Invariants checked (domain error, REST slug) | Events raised |
| --- | --- | --- | --- | --- |
| `RegisterProduct` | REST `PUT /products/{sku}` | `sku`, `description` | SKU 1..64 characters without whitespace, control characters or `/` (`invalid-sku`); description at most 200 characters, no control characters (`invalid-description`) | new SKU: `ProductRegistered` (version 1); existing SKU with a new description: `ProductDescriptionChanged`; same description: none |
| `ClassifyProduct` | REST `PUT /products/{sku}/classification` | `sku`, `handlingTags`, `temperatureClass`, `dotHazardClass` | at least one tag, closed tag set, no duplicates; `temperatureClass` iff `TemperatureSensitive`, closed class set; `dotHazardClass` 1..9 and only with `Hazmat` (one slug per rule, see [troubleshooting](/docs/operations/troubleshooting#rest-errors)); product registered (`product-not-found`); version guard | `ProductClassified` (source `native`), unless the same native classification is already set; confirming a `legacy-import` classification with the same values makes it native and is a change |
| `DeclareDimensions` | REST `PUT /products/{sku}/dimensions/declared` | `sku`, `lengthMm`, `widthMm`, `heightMm`, `weightG` | each dimension 1..20000 mm (`invalid-dimension`), weight 1..2000000 g (`invalid-weight`); all four fields present (`malformed-request`); product registered | `ProductDimensionsDeclared` (payload: the whole physical profile), unless the values are identical |
| `RecordMeasurement` | REST `PUT /products/{sku}/dimensions/measured` | `sku`, the four dimensions, `measuredAt`, optional `deviceId` | the same bounds; `measuredAt` present (`malformed-request`) and not after the service clock (`measured-at-in-future`); `deviceId` at most 64 characters; not older than the current measurement (`409 stale-measurement`); product registered | `ProductMeasured` (whole physical profile, with `discrepancy` when declared and measured differ by more than 10%); an identical measurement raises none; a different reading with the same `measuredAt` replaces it |
| `ImportLegacyClassification` | Kafka: `com.warehouse.wms.inventory-storage.product.ProductClassified` on `warehouse.inventory.events` (legacy importer in `cmd/api`) | CloudEvents `id`, `sku`, `handling_tags`, `temperature_class`, `dot_hazard_class` | same SKU and classification rules (a failure is `ErrInvalidLegacyImport`: logged, committed past); event id not already claimed in `processed_events` | unknown SKU: `ProductRegistered` (empty description) then `ProductClassified` (source `legacy-import`); known SKU: `ProductClassified` unless the current classification is `native` or identical |
| `GetProduct` | REST `GET /products/{sku}`, `/classification`, `/physical-profile`; MCP `get_product`, `get_product_classification`, `get_physical_profile` | `sku` | valid SKU (`invalid-sku`); exists (`product-not-found`); classified for the classification view (`product-classification-not-found`) | none |
| `ListProducts` | REST `GET /products`; MCP `list_products` | `limit` (default 100, max 500), `cursor`, `handlingTag`, `classified` | limit range, decodable cursor, known tag (`malformed-request`) | none |

The analytics side has no use case in `internal/application`: the projector
folds events straight into `report.Projection`
(`internal/analytics/report`, `internal/adapters/outbound/analyticsstore`) and
the reports server reads through `report.Reader`.

Acceptance scenarios for every use case are in `features/*.feature`
(godog, `features_test.go`).

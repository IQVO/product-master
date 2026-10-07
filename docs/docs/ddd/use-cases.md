---
id: use-cases
title: Use cases
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

Acceptance scenarios for every use case are in `features/*.feature`
(godog, `features_test.go`).

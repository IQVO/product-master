---
id: upstream-contracts
title: Upstream contracts
sidebar_position: 2
---

# Upstream contracts

`product-master` has **no** upstream contract. It reads no other context's
topic and has no outbound HTTP client (ADR 0001, "No live cross-context
lookup, in either direction"). The only Kafka consumer in this repository is
`cmd/product-projector`, which reads this service's own
`warehouse.product-master.analytics` topic.

Until stage E of [ADR 0003](/docs/adr/0003-migration-from-inventory-storage)
there was one migration-only contract with inventory-storage. It is kept below
as the migration record; none of it runs today.

## Retired: inventory-storage legacy `ProductClassified` (ADR 0003)

| Item | Value while it existed |
| --- | --- |
| Topic | `warehouse.inventory.events` |
| Type | `com.warehouse.wms.inventory-storage.product.ProductClassified` |
| Consumer group | env `LEGACY_IMPORT_CONSUMER_GROUP` (unset = importer not started) |
| Fields used | `sku`, `handling_tags`, `temperature_class`, `dot_hazard_class` (restated here, never imported from that service) |
| Idempotency | claim of the CloudEvents `id` in `processed_events` under consumer `legacy-classification-importer`, in the same unit of work as the effect |
| Effect | `ImportLegacyClassification`: register an unknown SKU (empty description) and set its classification with source `legacy-import`; skip when the current classification is `native`; no change when identical |
| Invalid input | not a CloudEvent, malformed payload, invalid SKU or classification: WARN and commit past |
| Transient failure | retry the same message, 200 ms doubling to 5 s, until it succeeds; no dead-letter topic |

### How it ended

Stage E was executed on 2026-10-17 after every row of inventory-storage's
`product_classifications` was verified present here (ADR 0003, stage E).

- product-master (#48): removed `internal/adapters/inbound/kafka/legacy_importer.go`,
  the `ImportLegacyClassification` use case and aggregate method,
  `LEGACY_IMPORT_CONSUMER_GROUP` in `cmd/api` and the chart, and the legacy
  godog features.
- inventory-storage (commit `002852f`, its ADR 0034): removed the
  `republish-product-classifications` backfill command, the legacy
  `ProductClassified` type from its publisher and AsyncAPI, and its retired
  `PUT` and `GET /products/{sku}/classification` endpoints. Its
  `ProductMasterConsumer`, which applies this context's `ProductClassified` to
  `product_classifications` as a local copy, stays.

What remains here: the `legacy-import` value of `classification_source` on
rows the importer wrote (`SourceLegacyImport`, still accepted by the
`products` CHECK constraint and the contracts' enums), and the
`processed_events` table, which keeps the importer's claims and has no writer
since.

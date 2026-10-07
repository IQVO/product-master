---
id: upstream-contracts
title: Upstream contracts
sidebar_position: 2
---

# Upstream contracts

`product-master` has **one** upstream contract, and it exists only for the
migration of classification ownership out of `inventory-storage`
([ADR 0003](/docs/adr/0003-migration-from-inventory-storage)). It disappears
at stage E. Messages are matched on the **full** CloudEvents `type`; unknown
types are ignored.

## inventory-storage: legacy `ProductClassified` (migration only)

| Item | Value |
| --- | --- |
| Topic | `warehouse.inventory.events` (`LegacyTopic` in `internal/adapters/inbound/kafka/legacy_importer.go`) |
| Type | `com.warehouse.wms.inventory-storage.product.ProductClassified` (`TypeLegacyProductClassified`), byte-identical to inventory-storage's AsyncAPI |
| Consumer group | env `LEGACY_IMPORT_CONSUMER_GROUP`; unset = importer not started (`startLegacyImporter` in `cmd/api/main.go`) |
| Fields used | `sku`, `handling_tags`, `temperature_class`, `dot_hazard_class` (`legacyClassifiedData`, restated here, never imported from that service) |
| Idempotency | claim of the CloudEvents `id` in `processed_events` under consumer `legacy-classification-importer`, in the same unit of work as the effect |
| Effect | `ImportLegacyClassification`: register an unknown SKU (empty description) and set its classification with source `legacy-import`; skip when the current classification is `native`; no change when identical |
| Invalid input | not a CloudEvent, malformed payload, invalid SKU or classification: WARN and commit past |
| Transient failure | retry the same message, 200 ms doubling to 5 s, until it succeeds; no dead-letter topic |

Every accepted import raises product-master's own `ProductRegistered` and/or
`ProductClassified` through the outbox, so downstream copies fill from one
topic only.

### Where inventory-storage stands

On `inventory-storage`'s `develop` the hand-over (its ADR 0034) is in place:
`PUT /products/{sku}/classification` there returns `410 Gone` with problem
type `classification-moved`, a one-shot backfill command re-emits every row of
its `product_classifications` table as the legacy event (stage B), and its
`ProductMasterConsumer` applies this context's `ProductClassified` to that
table as a local copy (stage C). The legacy type is therefore emitted only by
that backfill command until stage E.

## Not consumed

Nothing else. `product-master` has no outbound HTTP client and reads no other
topic (ADR 0001, "No live cross-context lookup, in either direction").

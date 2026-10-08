---
id: domain-message-flow
title: Domain message flow
sidebar_label: Domain message flow
sidebar_position: 15
---

# Domain message flow

Following ddd-crew [Domain Message Flow Modelling](https://github.com/ddd-crew/domain-message-flow-modelling).
Each arrow is numbered and prefixed `cmd:` (command), `evt:` (event) or `qry:`
(query); replies are notes. Only real messages appear: REST routes from
`NewRouter` and CloudEvents types from the encoder, the legacy importer and
the consumers on each sibling's `develop`.

## 1. A steward classifies a SKU and every consumer updates its copy

```mermaid
sequenceDiagram
  autonumber
  actor Steward as Master-data steward
  participant PM as product-master
  participant INV as inventory-storage
  participant OM as order-management
  participant WWP as wes-work-planning
  participant FE as fulfillment-execution
  Steward->>PM: cmd: PUT /products/SKU-1 with a description
  Note over Steward,PM: 201, version 1
  PM-->>INV: evt: ProductRegistered on warehouse.product-master.events
  Note over INV,FE: ProductRegistered is ignored by every consumer today
  Steward->>PM: cmd: PUT /products/SKU-1/classification Hazmat, DOT class 3
  Note over Steward,PM: 201, version 2, source native
  PM-->>INV: evt: ProductClassified version 2
  PM-->>OM: evt: ProductClassified version 2
  PM-->>WWP: evt: ProductClassified version 2
  PM-->>FE: evt: ProductClassified version 2
  Note over INV,FE: each upserts its local copy only if version 2 is greater than the stored one
```

Source: `internal/adapters/inbound/http/handlers.go`,
`internal/application/usecases/register_product.go`, `classify_product.go`,
`internal/adapters/outbound/kafka/encoder.go`; the four consumers listed on
[Downstream consumers](/docs/ecosystem/downstream-consumers).
Omits: the outbox relay hop between the commit and Kafka (one topic, the
fan-out is Kafka consumer groups), and the consumers' opt-in switches.

## 2. Declared, then measured, with a discrepancy

```mermaid
sequenceDiagram
  autonumber
  actor Steward as Master-data steward
  participant Device as Dimensioning device
  participant PM as product-master
  participant Topic as warehouse.product-master.events
  Steward->>PM: cmd: PUT /products/SKU-1/dimensions/declared 200x120x80 mm, 1500 g
  Note over Steward,PM: 200, effectiveSource declared
  PM-->>Topic: evt: ProductDimensionsDeclared with the full profile
  Device->>PM: cmd: PUT /products/SKU-1/dimensions/measured 205x121x82 mm, 1720 g, measuredAt
  Note over Device,PM: 200, effectiveSource measured, discrepancy true
  PM-->>Topic: evt: ProductMeasured with the full profile
  Device->>PM: cmd: PUT /products/SKU-1/dimensions/measured with an older measuredAt
  Note over Device,PM: 409 stale-measurement, no event
  Steward->>PM: qry: GET /products/SKU-1/physical-profile
  Note over Steward,PM: declared, measured, effective, discrepancy, version
```

Source: `internal/application/usecases/physical_profile.go`,
`internal/domain/product/physical.go`, `product.go` (`RecordMeasurement`),
the `ProductMeasured` example in `apis/asyncapi.yaml`.
Omits: consumers, because no sibling consumes the physical-profile events yet
(ADR 0002 names the later uses).

## 3. Migration: legacy classifications flow in, product-master's flow out

Stage A and B of [ADR 0003](/docs/adr/0003-migration-from-inventory-storage):
the importer runs (`LEGACY_IMPORT_CONSUMER_GROUP` set) and an operator runs
inventory-storage's one-shot backfill command. In the kind cluster the
importer runs with group `product-master-legacy-import`
(warehouse-infra `helm-values/product-master.yaml`), and the backfill was run
once on 2026-10-07 (6 rows). Stage E (removing the importer and the legacy
type) has not been run.

```mermaid
sequenceDiagram
  autonumber
  actor Operator
  participant INV as inventory-storage
  participant PM as product-master
  participant C as order-management, wes-work-planning, fulfillment-execution
  Operator->>INV: cmd: republish-product-classifications
  INV->>PM: evt: legacy ProductClassified for SKU-9 on warehouse.inventory.events
  Note over PM: SKU-9 unknown, registered, classified with source legacy-import
  PM-->>INV: evt: ProductRegistered version 1
  PM-->>INV: evt: ProductClassified version 2, source legacy-import
  PM-->>C: evt: ProductClassified version 2
  Note over INV: applying product-master events raises no event, so there is no loop
  Operator->>PM: qry: GET /products?classified=true, all pages
  Note over Operator,PM: count compared with the product_classifications row count
```

Source: `internal/adapters/inbound/kafka/legacy_importer.go`,
`internal/application/usecases/import_legacy_classification.go`,
`docs/adr/0003-migration-from-inventory-storage.md` (stages A to C and the
runbook); inventory-storage `cmd/inventory/republish.go` and its ADR 0034.
Omits: a re-run of the backfill after the cutover (every message is then
either skipped because the classification is `native` or identical, so no
change), and the deduplication of a redelivered legacy message by its
CloudEvents id.

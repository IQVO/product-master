---
id: bounded-context-canvas
title: Bounded context canvas
sidebar_label: Bounded context canvas
sidebar_position: 12
---

# Bounded context canvas

Following the ddd-crew [Bounded Context Canvas v5](https://github.com/ddd-crew/bounded-context-canvas).
Every message row below maps to a route in `NewRouter`
(`internal/adapters/inbound/http/server.go`) or to a topic and CloudEvents type
in `internal/adapters/inbound/kafka/legacy_importer.go` /
`internal/adapters/outbound/kafka/encoder.go`.

## Name

**Product Master** (`product-master`, Go module
`github.com/claudioed/product-master`, GitHub `IQVO/product-master`).

## Purpose

Be the single source of truth for SKU-level product master data: what a SKU
*is* for handling purposes (its classification) and physically (declared and
measured unit dimensions and weight). Publish every accepted change so the
contexts that act on these facts keep their own copy instead of asking at
request time. It answers no "where" or "how many" question (ADR 0001).

## Strategic Classification

| Axis | Verdict | Evidence |
| --- | --- | --- |
| Domain | **Supporting** | ADR 0001 "Classification"; see the [core domain chart](/docs/ddd/core-domain-chart) |
| Business model | **Compliance and risk reduction**: it earns nothing itself; it keeps hazmat, temperature and fragile handling correct in every downstream flow | the four consumers use the classification for stow placement and segregation, intake routing, release capabilities and seal-time segregation |
| Evolution | **Custom-built** (genesis) | Created 2026-10-06 (ADRs 0001-0004), migration from inventory-storage in progress (ADR 0003) |

## Domain Roles

| Role | Applies? | Notes |
| --- | --- | --- |
| Specification context | **Yes** (primary) | Stewards and devices specify what a SKU is; others act on it. |
| Gateway / Open Host Service | **Yes** | REST plus the `warehouse.product-master.events` Published Language. |
| Execution context | No | It never moves goods or work. |
| Analysis context | No | No computation over sets; `Discrepancy` is a per-product flag. |
| Analytics / reporting | No | No analytics topic in v1 (ADR 0004). |

## Inbound Communication

| Collaborator | Message | Type | Channel | Relationship |
| --- | --- | --- | --- | --- |
| Master-data steward / operator | Register product (or change its description) | Command | REST `PUT /products/{sku}` | This context is the OHS |
| Steward / operator | Classify product | Command | REST `PUT /products/{sku}/classification` | OHS |
| Steward / operator | Declare dimensions | Command | REST `PUT /products/{sku}/dimensions/declared` | OHS |
| Dimensioning device or operator | Record measurement | Command | REST `PUT /products/{sku}/dimensions/measured` | OHS |
| Operator, console, agent | Get product, classification, physical profile | Query | REST `GET /products/{sku}`, `/classification`, `/physical-profile` | OHS |
| Operator, console, agent | List products | Query | REST `GET /products` (filters `handlingTag`, `classified`) | OHS |
| `inventory-storage` | legacy `ProductClassified` | Event | Kafka `warehouse.inventory.events`, `com.warehouse.wms.inventory-storage.product.ProductClassified` | Conformist, migration only (ADR 0003); group `LEGACY_IMPORT_CONSUMER_GROUP` |

No REST endpoint is authenticated (fleet-wide revert of 2026-09-11). There is
no MCP server on `develop`.

## Outbound Communication

The context makes **no** synchronous call to any sibling and has no outbound
HTTP client (ADR 0001). Everything outbound leaves through the transactional
outbox and the relay in `cmd/api`.

| Collaborator | Message | Type | Channel | Relationship |
| --- | --- | --- | --- | --- |
| `inventory-storage`, `order-management`, `wes-work-planning`, `fulfillment-execution` | `ProductClassified` | Event | Kafka `warehouse.product-master.events`, `com.warehouse.wms.product-master.product.ProductClassified` | This context upstream, OHS + Published Language; each consumer keeps a version-guarded local copy |
| none today | `ProductRegistered` | Event | same topic, `com.warehouse.wms.product-master.product.ProductRegistered` | OHS + PL |
| none today | `ProductDescriptionChanged` | Event | same topic, `...product.ProductDescriptionChanged` | OHS + PL |
| none today (ADR 0002 names later uses) | `ProductDimensionsDeclared` | Event | same topic, `...product.ProductDimensionsDeclared` | OHS + PL |
| none today (ADR 0002 names later uses) | `ProductMeasured` | Event | same topic, `...product.ProductMeasured` | OHS + PL |

## Ubiquitous Language

Full glossary with code identifiers: [Ubiquitous language](/docs/ddd/ubiquitous-language).
Top terms: **Product**, **SKU**, **Classification**, **Handling tag**,
**Temperature class**, **DOT hazard class**, **Classification source**,
**Unit dimensions**, **Declared**, **Measurement**, **Effective values**,
**Discrepancy**, **Version**, **Local copy**.

## Business Decisions

1. A SKU is registered explicitly before it can be classified or dimensioned;
   an attribute of an unknown SKU is `404`, never an implicit registration
   (ADR 0001).
2. The classification taxonomy is closed and its invariants are inherited
   unchanged from inventory-storage ADRs 0009/0010, so the wire contract stays
   field-compatible (ADR 0001, ADR 0004).
3. Dimensions are whole millimetres and grams, bounded 1..20000 mm and
   1..2000000 g, never normalised for orientation (ADR 0002).
4. Effective = measured, else declared; the latest measurement by
   `measuredAt` wins and an older one is rejected (ADR 0002).
5. A discrepancy above 10 % is flagged, never rejected (ADR 0002).
6. `Oversized` is a steward decision; nothing derives tags from dimensions
   (ADR 0002).
7. A change that alters nothing raises no event and bumps no version; every
   event carries the version after the change (ADR 0004).
8. A legacy import never overwrites a `native` classification (ADR 0003).
9. DOT segregation rules are applied by the contexts that co-locate goods,
   not here (ADR 0001).

## Assumptions

- `measuredAt` is supplied by the caller and trusted, except that it may not
  be after the service clock.
- One fixed 10 % discrepancy tolerance fits every product category (ADR 0002
  calls a per-category tolerance a later decision).
- Consumers can live with eventual consistency of classifications (ADR 0001,
  Consequences).

## Verification Metrics

- Golden exact-JSON tests per published type (`TestEncoder_GoldenWireFormat`,
  `internal/adapters/outbound/kafka/encoder_test.go`).
- `TestEventCatalogueMatchesContract` checks ADR 0004 against
  `apis/asyncapi.yaml`.
- `make mutation-fast` (gremlins on `./internal/domain/product`) is blocking in
  CI; coverage gate 90 % on domain + application.
- Outbox atomicity and relay delivery proven with testcontainers Postgres and
  Kafka (`TestUseCasesCommitProductAndOutboxAtomically`,
  `TestRelay_RealPostgresAndKafka_PublishesCloudEventsKeyedBySKU`,
  `TestLegacyImporter_EndToEnd`).
- Migration: ADR 0003's runbook compares the count of classified products
  (`GET /products?classified=true`) with inventory-storage's
  `product_classifications` row count.
- No business metric is emitted yet.

## Open Questions

- Which consumer will take the physical profile first (expected package
  weight, slot fit, cube-based storage capacity; ADR 0002)?
- When is migration stage E (importer and legacy type removed) run?
- Should measurement history be kept (ADR 0002, Consequences)?
- Should the discrepancy tolerance become per category?
- Pack hierarchy, units of measure, lifecycle states and barcodes are out of
  scope for v1 (ADR 0001); which comes first?

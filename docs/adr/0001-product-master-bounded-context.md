# ADR 0001: product-master as the owner of SKU-level product master data

## Status

Accepted (2026-10-06).

## Context

The fleet has no bounded context for product master data. The only product
facts that exist live inside `inventory-storage`, which owns a
`ProductClassification` aggregate (its ADR 0009 and ADR 0010) next to
`StockUnit`: handling tags, temperature class and DOT hazard class. ADR 0009
already concluded that classification is "a property of the product, not of
any physical holding", but it put it in the stock ledger because no other home
existed.

That placement has costs:

- `order-management`, `wes-work-planning` and `fulfillment-execution` each
  call `GET /products/{sku}/classification` on `inventory-storage` at request
  time, each with a REST client, a `PRODUCT_CLASSIFICATION_MODE` switch and a
  circuit breaker. The 2026-10-05 audit flagged this as a hotspot;
  inventory-storage ADR 0031 (2026-10-06) started publishing
  `ProductClassified` as a first step.
- Nothing owns a SKU's physical characteristics. `fulfillment-execution`'s
  weigh check receives the expected weight from its caller,
  `facility-layout` models only what a slot can hold (`maxWeightKg`,
  `maxVolumeM3`), and no context can answer "how big and how heavy is one
  unit of this SKU".
- "What a SKU is" and "how many of it sit where" change for different
  reasons, at different rates, by different people (master-data stewards
  versus floor operations). Keeping them in one model couples a stewardship
  workflow to the stock ledger's availability and release cadence.

## Decision

Introduce `product-master` as a new bounded context and make it the single
source of truth for SKU-level product master data.

### Classification

- Strategic: **Supporting subdomain**. It is necessary for every warehouse
  flow and specific to this warehouse's handling rules, but it is not where
  the business differentiates.
- Tier: **WMS** ("what and where"). CloudEvents subdomain `wms`, the third
  `wms` context after `facility-layout` and `inventory-storage` (user
  decision 2026-10-06; the fleet subdomain table gains a row).
- Type prefix: `com.warehouse.wms.product-master.<entity>.<EventName>`,
  source `/warehouse/product-master`.

### The model (v1)

One aggregate, `Product`, identified by SKU. One aggregate because every
attribute below describes the same thing (one SKU) and is edited by the same
stewardship role; there is no invariant that spans two SKUs.

- `description`: optional operator text.
- `Classification` (value object, optional): the closed set of handling tags
  (`Hazmat`, `Fragile`, `TemperatureSensitive`, `Oversized`, `HighValue`),
  `TemperatureClass` (`Ambient`, `Chilled`, `Frozen`) required if and only if
  `TemperatureSensitive`, `DOTHazardClass` (1-9) optional and only with
  `Hazmat`. The invariants move unchanged from inventory-storage ADR 0009 and
  ADR 0010, so the wire contract can stay field-compatible.
- `PhysicalProfile` (value object): declared unit dimensions and weight, the
  latest measured dimensions and weight, and the derived effective values
  (ADR 0002).
- `version`: starts at 1 and increases by one on every accepted change. It
  guards the repository write (optimistic concurrency, 409 on a race) and is
  carried on every published event so downstream local copies can drop stale
  or replayed messages.

A product must be registered (`PUT /products/{sku}`) before it can be
classified or dimensioned. Attributes of an unknown SKU are a 404, not an
implicit registration: master data has an explicit beginning.

### Explicitly out of scope (later ADRs)

Pack hierarchy and units of measure (each, inner, case, pallet and their
conversion factors), lot, serial and expiry tracking policy, shelf life, kits
and bundles, velocity class, lifecycle states (discontinue/block), barcodes
and GTINs, and every commercial attribute (title, price, images). Commercial
catalog data belongs to a selling context, not to the warehouse.

`DOTHazardClass` segregation rules (49 CFR 177.848) are NOT owned here. This
context records the class; the contexts that physically co-locate goods
(`inventory-storage` per bin, `fulfillment-execution` per package) keep
applying their own segregation matrix to it.

### Context map

| Relationship | Pattern |
|---|---|
| product-master -> inventory-storage | Published Language (`warehouse.product-master.events`); inventory-storage keeps a local copy for stow-time placement and segregation checks |
| product-master -> order-management | Published Language; local copy for intake enrichment |
| product-master -> wes-work-planning | Published Language; local copy for release-time capabilities and the fragile flag |
| product-master -> fulfillment-execution | Published Language; local copy for seal-time segregation |
| inventory-storage -> product-master | Conformist, migration only: the legacy importer reads inventory-storage's `ProductClassified` (ADR 0003), removed at decommission |
| product-master -> warehouse-ops-agent, warehouse-console | Open Host Service (REST, MCP read tools) |

### No live cross-context lookup, in either direction

- product-master never calls a sibling context. It has no outbound HTTP
  client at all.
- Downstream contexts do not call product-master at request time either. They
  keep a local copy built from the published events (full-state per concern,
  keyed and partitioned by SKU, guarded by `version`). This removes the three
  live lookups and their breakers. The REST `GET` endpoints exist for
  operators, the console and agents, not for service-to-service reads.

## Consequences

- inventory-storage loses ownership of classification. Its own ADR records
  the hand-over, and its write endpoint is retired (ADR 0003).
- A new deployable (API, Postgres database, Kafka topic, Kong route) joins the
  fleet. `warehouse-infra` wires it by hand (`local.services`).
- Classification changes become eventually consistent downstream. That was
  already true for any cached reader; the new risk is a SKU classified moments
  before its first stow. Consumers keep their existing fail-open/fail-closed
  rules for an unknown SKU, unchanged.
- The fleet CloudEvents rule "wms is facility-layout and inventory-storage
  only" changes to name three contexts. The managed fleet rule and the
  warehouse-docs subdomain table change in the same wave.

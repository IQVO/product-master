# ADR 0003: Migrating classification ownership from inventory-storage

## Status

Accepted (2026-10-06). Companion decisions: inventory-storage ADR 0034
(hand-over) and one ADR in each of order-management, wes-work-planning and
fulfillment-execution (local copy instead of live lookup).

## Context

ADR 0001 moves `ProductClassification` out of inventory-storage. Existing
classifications live only in inventory-storage's `product_classifications`
table. inventory-storage started publishing
`com.warehouse.wms.inventory-storage.product.ProductClassified` on
`warehouse.inventory.events` on 2026-10-06 (its ADR 0031) but did not backfill
older rows onto the topic. Three services read classifications live over REST.

The migration must not lose a classification, must never let two contexts
both accept writes for the same fact at the same time, and must not create a
publish loop between the two contexts.

## Decision

A strangler migration in five stages, using event-carried state transfer only
(no shared database access, no cross-context REST calls).

### Stage A: product-master runs a legacy importer

`product-master` consumes `warehouse.inventory.events` with a stable consumer
group from env `LEGACY_IMPORT_CONSUMER_GROUP` (unset = importer not started).
It acts only on the full type
`com.warehouse.wms.inventory-storage.product.ProductClassified`, ignores every
other type, dedupes on the CloudEvents `id` in the same transaction as the
effect, and commits the offset afterwards. For each message, use case
`ImportLegacyClassification`:

- SKU unknown: register the product (empty description) and set the
  classification with `classificationSource=legacy-import`.
- SKU known, classification authored in product-master
  (`classificationSource=native`): skip. product-master is the authority;
  a legacy message never overwrites a native decision.
- SKU known, no classification or a legacy one: replace it, unless the
  incoming classification is identical (then no change, no version bump, no
  event).

Every accepted import raises product-master's own events
(`ProductRegistered`, `ProductClassified`) through the outbox, so
downstream copies fill from one topic only.

### Stage B: backfill

inventory-storage gains a one-shot command that re-emits every row of
`product_classifications` as its existing `ProductClassified` event through its
outbox (details in inventory-storage ADR 0034). The payload is a full-state
replacement, so running it twice is harmless. The importer above turns it into
product-master data.

### Stage C: cutover of write authority (inventory-storage)

- `PUT /products/{sku}/classification` on inventory-storage returns
  `410 Gone` with problem type `classification-moved`, naming
  product-master's endpoint.
- inventory-storage's write path stops raising `ProductClassified` (the
  `ClassifyProduct` use case is removed). The only remaining emitter of the
  legacy type is the one-shot backfill command of stage B, an explicit
  operator action, and both disappear at stage E.
- inventory-storage consumes product-master's `ProductClassified` and upserts
  its existing `product_classifications` table, guarded by `version`. That
  table becomes a local copy that StowStock reads exactly as before.
- inventory-storage's `GET /products/{sku}/classification` keeps working from
  the local copy and is marked deprecated until stage E.

Applying product-master's events raises no event in inventory-storage, so
there is no loop: product-master -> inventory-storage is one-way after stage
C. Running the backfill again after the cutover is harmless: every
re-emitted classification is either `native` in product-master (skipped) or
identical to what it already holds (no change).

### Stage D: downstream readers move to local copies

order-management, wes-work-planning and fulfillment-execution keep their
`ProductClassificationLookup` port and swap only the adapter: a Kafka consumer
of `warehouse.product-master.events` writes a local Postgres copy, and the
lookup reads it. `PRODUCT_CLASSIFICATION_MODE` becomes `kafka|permissive`;
`http` is removed and rejected at boot, so a stale deployment fails loudly
instead of silently degrading to permissive.

### Stage E: decommission (after the cluster is verified)

Remove inventory-storage's deprecated `GET` endpoint and its backfill command,
remove product-master's legacy importer and `LEGACY_IMPORT_CONSUMER_GROUP`,
and mark the inventory-storage `ProductClassified` type retired in
warehouse-docs.

### Runbook (first deployment)

1. Deploy product-master with `LEGACY_IMPORT_CONSUMER_GROUP` set, then
   inventory-storage (stage C) and the readers (stage D).
2. Run inventory-storage's backfill command once.
3. Check that the number of classified products in product-master
   (`GET /products?classified=true`, counting all pages) equals the row count
   of inventory-storage's `product_classifications`.
4. Probe asymmetrically: a Hazmat SKU stows into a hazmat zone (201) and is
   rejected from a non-hazmat zone (409).

Between steps 1 and 2, readers see no classification for older SKUs and apply
their existing unknown-SKU behaviour (fail-open for enrichment). Run step 2
right after product-master is ready, before routing traffic.

## Consequences

- Every downstream copy is fed from one producer and one topic; the importer
  and the legacy type disappear at stage E.
- `classificationSource` is a migration artefact. After stage E every new
  write is `native`; existing `legacy-import` rows stay labelled, which tells
  stewards which classifications were never reviewed in product-master.

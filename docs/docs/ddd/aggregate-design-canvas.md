---
id: aggregate-design-canvas
title: Aggregate design canvas
sidebar_label: Aggregate design canvas
sidebar_position: 14
---

# Aggregate design canvas

Following the ddd-crew [Aggregate Design Canvas v1.1](https://github.com/ddd-crew/aggregate-design-canvas).
There is one aggregate root in `internal/domain`: `Product`.

## Product

### 1. Name

`Product` (`internal/domain/product/product.go`).

### 2. Description

The master record of one SKU: an optional description, an optional handling
`Classification` with its `ClassificationSource`, and a `PhysicalProfile`
(declared dimensions plus the latest measurement). Identity is the `SKU`.
Every accepted change increments `version`, which guards persistence and rides
on every published event. The aggregate does no I/O; the caller passes `now`.

### 3. State Transitions

The aggregate has no status enum. Its observable states are whether it is
classified (and by which source) and which parts of the physical profile are
set; `version` increases on every transition drawn below except the
self-loops marked "no change".

```mermaid
stateDiagram-v2
  [*] --> Registered: Register(sku, description, now), version 1
  Registered --> Registered: ChangeDescription with other text
  Registered --> ClassifiedNative: Classify(c)
  [*] --> ClassifiedLegacy: Rehydrate of a stored legacy-import row
  ClassifiedLegacy --> ClassifiedNative: Classify(c), even with the same c
  ClassifiedNative --> ClassifiedNative: Classify with another c
  note right of ClassifiedLegacy
    Rehydrated rows only: source
    legacy-import from the ADR 0003
    importer, removed at stage E.
    No command enters this state.
  end note
```

```mermaid
stateDiagram-v2
  [*] --> NoProfile: Register, effectiveSource none
  NoProfile --> DeclaredOnly: DeclareDimensions(d)
  NoProfile --> MeasuredOnly: RecordMeasurement(m)
  DeclaredOnly --> DeclaredAndMeasured: RecordMeasurement(m)
  MeasuredOnly --> DeclaredAndMeasured: DeclareDimensions(d)
  DeclaredOnly --> DeclaredOnly: DeclareDimensions with other values
  MeasuredOnly --> MeasuredOnly: RecordMeasurement not older than current
  DeclaredAndMeasured --> DeclaredAndMeasured: DeclareDimensions or RecordMeasurement
  note right of DeclaredAndMeasured
    effectiveSource measured,
    discrepancy may be true.
    An older measurement is
    rejected ErrStaleMeasurement
  end note
```

Source: `internal/domain/product/product.go` (`Register`, `ChangeDescription`,
`Classify`, `setClassification`, `DeclareDimensions`, `RecordMeasurement`),
`internal/domain/product/physical.go`. `ClassifiedLegacy` is reachable only
through `Rehydrate`: a row whose `classification_source` is `legacy-import` was
written by the ADR 0003 importer before stage E (2026-10-17) removed it, and no
command sets that source any more (`SourceLegacyImport` in
`internal/domain/product/classification.go` stays a valid stored value).
Omits: `Rehydrate` (rebuilds a stored product without events) and the absence
of any delete, unclassify or unregister operation in v1. Identical commands
are no change in every state (no event, no version bump).

### 4. Enforced Invariants

| Invariant | Enforced by |
| --- | --- |
| SKU is 1..64 characters, no whitespace, control characters or `/`; never changes | `NewSKU` (`ErrInvalidSKU`); no setter |
| Description at most 200 characters, no control characters | `validateDescription` (`ErrInvalidDescription`) |
| Classification: at least one tag, closed set, no duplicates | `NewClassification` (`ErrNoHandlingTags`, `ErrUnknownHandlingTag`, `ErrDuplicateHandlingTag`) |
| `TemperatureClass` iff `TemperatureSensitive`, value in the closed set | `checkTemperature` (`ErrTemperatureClassRequired`, `ErrTemperatureClassNotApplicable`, `ErrUnknownTemperatureClass`) |
| `DOTHazardClass` 1..9 and only with `Hazmat` (0 = not recorded) | `checkDOT` (`ErrInvalidDOTHazardClass`, `ErrDOTHazardClassNotApplicable`); also `CHECK (dot_hazard_class BETWEEN 1 AND 9)` |
| A classification always has a source, and vice versa | `setClassification`; `CHECK ((handling_tags IS NULL) = (classification_source IS NULL))` |
| Dimensions 1..20000 mm, weight 1..2000000 g | `NewUnitDimensions` (`ErrInvalidDimension`, `ErrInvalidWeight`) |
| A measurement has `measuredAt`, not after the clock; device id at most 64 runes, no control characters | `NewMeasurement` (`ErrMissingMeasuredAt`, `ErrMeasuredAtInFuture`, `ErrInvalidDeviceID`) |
| The latest measurement is never replaced by an older one | `RecordMeasurement` (`ErrStaleMeasurement`) |
| `version` starts at 1, +1 per accepted change, unchanged by a no-op | `Register`, `bump`; `CHECK (version >= 1)`; the version-guarded `Save` |

### 5. Corrective Policies

- A REST caller gets a `400` with the slug of the broken rule, `404
  product-not-found` for an unregistered SKU, `409 stale-measurement` for an
  older reading, or `409 concurrent-modification` when another writer saved a
  newer version first; it must re-fetch and retry
  (`internal/adapters/inbound/http/errors.go`).
- A discrepancy above 10 % is **not** corrected automatically: the
  measurement is recorded and becomes effective, and the flag is published
  for stewards (ADR 0002).

### 6. Handled Commands

| Command | Use case | Inbound |
| --- | --- | --- |
| `RegisterProductCommand` | `RegisterProduct.Handle` (`Register` or `ChangeDescription`) | `PUT /products/{sku}` |
| `ClassifyProductCommand` | `ClassifyProduct.Handle` (`Classify`) | `PUT /products/{sku}/classification` |
| `DeclareDimensionsCommand` | `DeclareDimensions.Handle` (`DeclareDimensions`) | `PUT /products/{sku}/dimensions/declared` |
| `RecordMeasurementCommand` | `RecordMeasurement.Handle` (`RecordMeasurement`) | `PUT /products/{sku}/dimensions/measured` |
| `DeclarePackLevelCommand` | `DeclarePackLevel.Handle` (`DeclarePackLevel`) | `PUT /products/{sku}/pack-levels/{level}`, MCP `declare_pack_level` |
| `RetirePackLevelCommand` | `RetirePackLevel.Handle` (`RetirePackLevel`) | `POST /products/{sku}/pack-levels/{level}:retire`, MCP `retire_pack_level` |
| `RecordPackLevelMeasurementCommand` | `RecordPackLevelMeasurement.Handle` (`RecordPackLevelMeasurement`) | `PUT /products/{sku}/pack-levels/{level}/measured` |

### 7. Created Events

| Event | When | Full CloudEvents type |
| --- | --- | --- |
| `ProductRegistered` | `Register` | `com.warehouse.wms.product-master.product.ProductRegistered` |
| `ProductDescriptionChanged` | `ChangeDescription` with different text | `com.warehouse.wms.product-master.product.ProductDescriptionChanged` |
| `ProductClassified` | `Classify` that changes the classification or its source (confirming a `legacy-import` row makes it `native`) | `com.warehouse.wms.product-master.product.ProductClassified` |
| `ProductDimensionsDeclared` | `DeclareDimensions` with different values | `com.warehouse.wms.product-master.product.ProductDimensionsDeclared` |
| `ProductMeasured` | `RecordMeasurement` with a new reading | `com.warehouse.wms.product-master.product.ProductMeasured` |
| `ProductPackLevelDeclared` | `DeclarePackLevel` that declares a level or changes its flags or declared dimensions | `com.warehouse.wms.product-master.product.ProductPackLevelDeclared` |
| `ProductPackLevelRetired` | `RetirePackLevel`, or `DeclarePackLevel` with a new GTIN at an active level (reason `superseded`) | `com.warehouse.wms.product-master.product.ProductPackLevelRetired` |
| `ProductPackLevelMeasured` | `RecordPackLevelMeasurement` with a new reading | `com.warehouse.wms.product-master.product.ProductPackLevelMeasured` |

The `Product` aggregate also holds its `PackagingHierarchy` (ADR 0011): every pack level the SKU ever declared, retired ones kept. Its invariants: a GTIN is valid and identifies one (sku, level); an ACTIVE level's content quantity never changes; every level above EACH has an active lower child level and `qtyOfChild` of at least 2; a pack weighs at least its contents when both weights are known; at most one active level per `PackLevel`.

Each command raises at most one event, except `DeclarePackLevel` with a new GTIN at an active level, which raises two (Retired, then Declared, one `version` apart). The `FanoutEncoder` writes each unit event
as two outbox rows (and each pack level event as one, on the integration topic only) in the same unit of work (one on
`warehouse.product-master.events`, one on `warehouse.product-master.analytics`,
same CloudEvents id; ADR 0006).

### 8. Throughput (estimate)

*Estimate, not measured.* Writes are stewardship actions: registrations and
classifications when SKUs are onboarded (bursts during a catalogue load),
measurements when a unit passes a dimensioning device.
One SKU is rarely written twice in the same second, so version conflicts
should be rare. Reads by operators are occasional; services do not read it at
request time.

### 9. Size (estimate)

*Estimate.* One row per SKU, a fixed number of scalar columns plus a short tag
array (at most five tags). The aggregate never grows: there is no measurement
history and no child collection. Events per instance grow with its edit count;
each is two outbox rows (integration and analytics topic).

## Not aggregates: value objects and technical tables

| Model | Kind | Where | Why it is not an aggregate |
| --- | --- | --- | --- |
| `Classification` | Value object | `classification.go` | Immutable, replaced whole, no identity |
| `UnitDimensions`, `Measurement`, `PhysicalProfile` | Value objects | `physical.go` | Immutable parts of `Product`, replaced whole |
| `processed_events` | Idempotency guard | migration `0001`, `ProcessedEventRepo` | Held the claims of the legacy importer; since stage E no inbound consumer of `cmd/api` claims into it (the repo is still constructed in `cmd/api/adapters.go`, with no caller) |
| `outbox_events` | Transactional outbox | migration `0001`, `OutboxRepo` | Encoded messages awaiting the relay |

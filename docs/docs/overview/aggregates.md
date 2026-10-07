---
id: aggregates
title: Aggregates
sidebar_position: 2
---

# Aggregates

One aggregate carries the model. Domain code lives in
`internal/domain/product` and does no I/O.

## Product

Package `internal/domain/product` (`product.go`, `classification.go`,
`physical.go`, `events.go`, `sku.go`).

- **Identity**: `SKU`, 1..64 characters without whitespace, control
  characters or `/` (`NewSKU`, `ErrInvalidSKU`). It never changes.
- **Why one aggregate**: every attribute describes the same thing (one SKU)
  and is edited by the same stewardship role; no invariant spans two SKUs
  (ADR 0001).
- **State**: `description`, an optional `Classification` with its
  `ClassificationSource`, a `PhysicalProfile`, and `version`.
- **Commands** (each returns the raised domain events; a command that changes
  nothing returns no event and leaves `version` unchanged):

| Method | Raises | No change when |
| --- | --- | --- |
| `Register(sku, description, now)` | `ProductRegistered` (version 1) | never (creation) |
| `ChangeDescription(description, now)` | `ProductDescriptionChanged` | same text |
| `Classify(classification, now)` | `ProductClassified` (source `native`) | same classification already `native` |
| `ImportLegacyClassification(classification, now)` | `ProductClassified` (source `legacy-import`) | current classification is `native`, or identical and already `legacy-import` |
| `DeclareDimensions(dimensions, now)` | `ProductDimensionsDeclared` | same declared values |
| `RecordMeasurement(measurement, now)` | `ProductMeasured` | the identical reading (same dimensions, `measuredAt` and device) |

Confirming a legacy-imported classification with the same values through
`Classify` **is** a change: the source becomes `native`, the version bumps and
`ProductClassified` is raised again (`setClassification` compares source and
classification).

### Invariants

| Invariant | Enforced by |
| --- | --- |
| Description at most 200 characters, no control characters | `validateDescription`, `ErrInvalidDescription` |
| At least one handling tag, each from the closed set, no duplicates | `NewClassification`: `ErrNoHandlingTags`, `ErrUnknownHandlingTag`, `ErrDuplicateHandlingTag` |
| `TemperatureClass` required iff `TemperatureSensitive`, one of `Ambient`, `Chilled`, `Frozen` | `checkTemperature`: `ErrTemperatureClassRequired`, `ErrTemperatureClassNotApplicable`, `ErrUnknownTemperatureClass` |
| `DOTHazardClass` optional, 1..9, only with `Hazmat` (0 = not recorded) | `checkDOT`: `ErrInvalidDOTHazardClass`, `ErrDOTHazardClassNotApplicable` |
| Each dimension 1..20000 mm, weight 1..2000000 g | `NewUnitDimensions`: `ErrInvalidDimension`, `ErrInvalidWeight` |
| A measurement has a `measuredAt` that is not after the service clock; device id at most 64 characters without control characters | `NewMeasurement`: `ErrMissingMeasuredAt`, `ErrMeasuredAtInFuture`, `ErrInvalidDeviceID` |
| The latest measurement is never replaced by an older one | `RecordMeasurement`: `ErrStaleMeasurement` |
| A legacy import never overwrites a `native` classification | `ImportLegacyClassification` |
| `version` starts at 1 and increases by exactly one per accepted change | `Register`, `bump` |

### Persistence guard

`ProductRepository.Save(ctx, product, loadedVersion)` inserts when
`loadedVersion` is 0 (an existing SKU affects no row) and otherwise updates
only `WHERE sku = $1 AND version = <loaded>`. No affected row is
`repository.ErrConcurrentModification`, a `409 concurrent-modification`
(`internal/adapters/outbound/postgres/product_repository.go`).

## Not aggregates

| Model | Kind | Why it is not an aggregate |
| --- | --- | --- |
| `Classification` | Value object | Immutable, constructed only through `NewClassification`, compared with `Equal` |
| `PhysicalProfile`, `UnitDimensions`, `Measurement` | Value objects | Immutable parts of `Product`; replaced whole (ADR 0002) |
| `processed_events` | Idempotency guard | Claims of the legacy importer, no domain meaning |
| `outbox_events` | Transactional outbox | Already-encoded Kafka messages awaiting the relay |

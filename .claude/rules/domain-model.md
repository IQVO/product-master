---
paths:
  - "internal/domain/**"
  - "internal/application/**"
  - "features/**"
---

# Domain model: ubiquitous language, aggregates, events, use cases

## Ubiquitous Language (use these exact names)

- **Product**: the master record of one SKU. Registered explicitly before any
  attribute can be set. Aggregate root, identity = `SKU`.
- **SKU**: 1..64 characters, no whitespace, control characters or `/`.
- **Description**: operator text, 0..200 characters.
- **Classification**: how a SKU must be handled. `HandlingTags` (closed set:
  `Hazmat`, `Fragile`, `TemperatureSensitive`, `Oversized`, `HighValue`;
  non-empty, no duplicates), `TemperatureClass` (`Ambient`/`Chilled`/`Frozen`,
  required iff `TemperatureSensitive`), `DOTHazardClass` (1-9, optional, only
  with `Hazmat`). Inherited unchanged from inventory-storage ADR 0009/0010.
- **ClassificationSource**: `native` (authored here) or `legacy-import`
  (imported from inventory-storage during the migration, ADR 0003).
- **UnitDimensions**: length, width, height in whole mm (1..20000 each) and
  weight in whole g (1..2000000); `Volume` = l x w x h in mm3.
- **Declared dimensions**: vendor/steward supplied UnitDimensions.
- **Measurement**: measured UnitDimensions + `MeasuredAt` + optional `DeviceID`.
- **PhysicalProfile**: declared + latest measurement; `Effective` = measured if
  present else declared (`EffectiveSource` `measured|declared|none`);
  `Discrepancy` = both present and volume or weight differ by more than 10 %
  of declared (`|m-d|*10 > d`).
- **Version**: starts at 1, +1 per accepted change; published on every event.

## Aggregates

- **Product** (`internal/domain/product`): enforces every invariant above.
  Commands return the raised domain events; a command that changes nothing
  returns no event and leaves `Version` unchanged. Rules: recording a
  measurement older than the current one is `ErrStaleMeasurement`;
  `MeasuredAt` after the clock's now is `ErrMeasuredAtInFuture`; a legacy
  import never replaces a `native` classification.

## Domain events (all carry SKU + Version)

- `ProductRegistered` (description): on registration.
- `ProductDescriptionChanged` (description): description changed.
- `ProductClassified` (tags, temperature class, DOT class, source): set or replaced.
- `ProductDimensionsDeclared` (full physical profile): declared set or replaced.
- `ProductMeasured` (full physical profile): measurement recorded.

## Key use cases (`internal/application/usecases`)

- `RegisterProduct`: create (201) or change description (200).
- `ClassifyProduct`: set/replace classification (`native`); 404 if not registered.
- `DeclareDimensions`, `RecordMeasurement`: physical profile; 404 if not registered.
- `ImportLegacyClassification`: migration only (ADR 0003): register unknown SKU,
  skip when the current classification is `native`, no-op when identical.
- `GetProduct`, `ListProducts`: reads (cursor pagination by SKU).

Every write: load, apply the command, save with the loaded version as the
optimistic guard, and enqueue the events in the outbox in ONE unit of work.

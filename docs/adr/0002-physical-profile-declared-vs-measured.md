# ADR 0002: Physical profile, declared versus measured

## Status

Accepted (2026-10-06).

## Context

No context in the fleet knows how big or how heavy one unit of a SKU is.
Warehouses get that number from two sources that routinely disagree:

- **Declared**: what the vendor or the master-data steward says (catalogue
  data, packaging spec). Available before the first unit arrives, often wrong.
- **Measured**: what a dimensioning device (a cubiscan) or a person with a
  tape and a scale recorded on a real unit. Arrives later, is trusted more.

Downstream uses (expected package weight at the weigh check, cube-based slot
fit, cube-based storage capacity) need one number to act on, and stewards need
to see when the two sources disagree.

## Decision

`Product` carries a `PhysicalProfile` value object with two optional parts:

- `declared`: `UnitDimensions` = length, width, height in **whole
  millimetres** and weight in **whole grams**.
- `measured`: `UnitDimensions` plus `measuredAt` (when the unit was measured,
  supplied by the caller) and an optional `deviceId` (free text, e.g. the
  cubiscan's asset tag; empty for a manual measurement).

Rules:

1. **Integer units.** Millimetres and grams as integers, never floats, so two
   services comparing the same value never disagree on rounding. Consumers
   convert at their own edge (facility-layout uses kg and m3).
2. **Bounds.** Each dimension 1..20 000 mm, weight 1..2 000 000 g. Zero or
   negative values are rejected; the bounds catch unit mistakes (metres typed
   as millimetres) rather than model real limits.
3. **Orientation is not normalised.** Length, width and height are stored as
   given. Volume (`volumeMm3` = l x w x h) is orientation-free; a consumer that
   needs a fit check sorts the three sides itself.
4. **Effective values.** `effective` = `measured` if present, else `declared`,
   else absent. `effectiveSource` is `measured`, `declared` or `none`.
   Consumers act on `effective` only.
5. **Latest measurement wins, by `measuredAt`.** Recording a measurement whose
   `measuredAt` is earlier than the current measurement's is rejected
   (409 `stale-measurement`), so a delayed upload from a device cannot
   overwrite a newer reading. `measuredAt` may not be in the future relative to
   the service clock.
6. **Discrepancy.** When both parts exist, the profile reports
   `discrepancy=true` if the measured volume or the measured weight differs
   from the declared one by more than **10 %** of the declared value
   (`|m - d| * 10 > d`, integer arithmetic). The tolerance is fixed in v1; a
   per-category tolerance is a later decision. A discrepancy is information
   for stewards, not a rejection: the measurement is still recorded and still
   becomes effective.
7. **Classification is independent.** `Oversized` stays a steward decision.
   The model does not derive or validate handling tags from dimensions (no
   rule exists yet that the business has agreed to).

Events: `ProductDimensionsDeclared` and `ProductMeasured` each carry the full
physical profile state (declared, measured, effective, effective source,
discrepancy, version), so a consumer overwrites its copy without having to
merge partial updates.

## Consequences

- Downstream consumers (later phases, not this one): fulfillment-execution can
  derive expected package weight as effective unit weight x scanned quantity;
  inventory-storage can check a unit's volume against slot capacity at stow;
  warehouse-planning can compute cube-based storage capacity.
- A measurement history is not kept in v1 (only the latest). If audit of past
  readings becomes a need, it is an additive table and a new ADR.

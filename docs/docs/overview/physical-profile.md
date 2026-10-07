---
id: physical-profile
title: Physical profile
sidebar_position: 3
---

# Physical profile

How a product's size and weight are held. This page summarises
[ADR 0002](/docs/adr/0002-physical-profile-declared-vs-measured); the ADR is
the authority and `internal/domain/product/physical.go` is the implementation.

## Two sources that disagree

| Part | Who supplies it | Type | Set by |
| --- | --- | --- | --- |
| **Declared** | vendor or master-data steward (catalogue, packaging spec), before the first unit arrives | `UnitDimensions` | `PUT /products/{sku}/dimensions/declared` → `DeclareDimensions` |
| **Measured** | a dimensioning device (cubiscan) or a person, on a real unit | `Measurement` = `UnitDimensions` + `measuredAt` + optional `deviceId` | `PUT /products/{sku}/dimensions/measured` → `RecordMeasurement` |

## Rules

1. **Integer units.** Length, width and height in whole millimetres, weight in
   whole grams (`int64`), so two services never disagree on rounding.
2. **Bounds.** Each dimension 1..20000 mm (`MaxDimensionMm`), weight
   1..2000000 g (`MaxWeightG`). They catch unit mistakes, not real limits.
3. **Orientation is not normalised.** Sides are stored as given;
   `VolumeMm3() = length x width x height` is orientation-free.
4. **Effective values.** `Effective()` is the measurement if present, else the
   declared dimensions, else absent; `EffectiveSource()` is `measured`,
   `declared` or `none`. Consumers act on the effective values only.
5. **Latest measurement wins, by `measuredAt`.** An older reading than the
   current one is `ErrStaleMeasurement` (`409 stale-measurement`); a
   `measuredAt` after the service clock is `ErrMeasuredAtInFuture`
   (`400 measured-at-in-future`). A different reading with the *same*
   `measuredAt` replaces the current one (a correction); the identical reading
   is no change. `measuredAt` is normalised to UTC and truncated to
   microseconds, the precision Postgres stores.
6. **Discrepancy.** When both parts exist, `Discrepancy()` is true if the
   measured volume or the measured weight differs from the declared one by more
   than 10 % of the declared value (`|m - d| * 10 > d`, integer arithmetic).
   It is information for stewards, not a rejection.
7. **Classification is independent.** `Oversized` stays a steward decision;
   nothing derives or validates handling tags from dimensions.

## Worked example

| Step | Declared (mm, g) | Measured (mm, g) | Effective | `effectiveSource` | `discrepancy` | `version` |
| --- | --- | --- | --- | --- | --- | --- |
| Register `SKU-1` | none | none | none | `none` | `false` | 1 |
| Declare 100 x 100 x 100, 1000 g | 100 x 100 x 100, 1000 | none | declared | `declared` | `false` | 2 |
| Measure 100 x 100 x 105, 1050 g at 10:00 | same | 100 x 100 x 105, 1050 | measured | `measured` | `false` (5 % volume, 5 % weight) | 3 |
| Measure 100 x 100 x 120, 1000 g at 11:00 | same | 100 x 100 x 120, 1000 | measured | `measured` | `true` (20 % volume) | 4 |
| Measure again, dated 09:00 | same | unchanged | measured | `measured` | `true` | 4, rejected `409 stale-measurement` |

Each accepted step publishes either `ProductDimensionsDeclared` or
`ProductMeasured`, both carrying the **full** profile (declared, measured,
effective, effective source, discrepancy, version), so a consumer overwrites
its copy instead of merging.

## Not kept in v1

- A measurement history: only the latest measurement is stored
  (`measured_*` columns on `products`). ADR 0002 makes history an additive
  table with its own ADR if audit becomes a need.
- A per-category tolerance: the 10 % threshold is fixed.

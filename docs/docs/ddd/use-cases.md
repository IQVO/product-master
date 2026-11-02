---
id: use-cases
title: Use cases
sidebar_label: Use cases
sidebar_position: 2
---

# Use cases

Application services in `internal/application/usecases`. The seven write use
cases (four unit ones and the three pack level ones of ADR 0011) embed `Writer` (`writer.go`), which runs load, command, version-guarded
save and outbox insert in one `ports.UnitOfWork`.

| Use case | Kind | Inbound | What it does |
| --- | --- | --- | --- |
| `RegisterProduct` | command | `PUT /products/{sku}` | Registers an unknown SKU at version 1 (`201`, `ProductRegistered`) or replaces an existing product's description (`200`, `ProductDescriptionChanged`, or no event when unchanged). |
| `ClassifyProduct` | command | `PUT /products/{sku}/classification` | Validates the classification **before** loading (so an invalid body is `400` even for an unknown SKU), then sets or replaces it with source `native`. `201` when the product had no classification, else `200`. `404` for an unregistered SKU. |
| `DeclareDimensions` | command | `PUT /products/{sku}/dimensions/declared` | Validates the bounds, then sets or replaces the declared dimensions (`ProductDimensionsDeclared`). `404` for an unregistered SKU. |
| `RecordMeasurement` | command | `PUT /products/{sku}/dimensions/measured` | Validates bounds, `measuredAt` (present, not in the future by the service clock) and `deviceId`, then records the latest measurement (`ProductMeasured`). `409 stale-measurement` for an older reading. |
| `DeclarePackLevel` | command | `PUT /products/{sku}/pack-levels/{level}`; MCP `declare_pack_level` | Declares a pack level (`EACH`, `INNER`, `CASE`, `PALLET`) with its GTIN. Checks that no OTHER SKU holds the GTIN (`gtin-already-assigned`, backed by the unique index), then the aggregate's rules. `201` when the product had no level there, else `200`. A new GTIN at an active level end-dates the old level in the same transaction (`ProductPackLevelRetired` then `ProductPackLevelDeclared`). |
| `RetirePackLevel` | command | `POST /products/{sku}/pack-levels/{level}/retirements`; MCP `retire_pack_level` | End-dates the active level (`ProductPackLevelRetired`); the level and its GTIN are kept. Retiring a retired level changes nothing; a level never declared is `404 pack-level-not-found`. An empty reason is `retired`. |
| `RecordPackLevelMeasurement` | command | `PUT /products/{sku}/pack-levels/{level}/measured` | The per-level counterpart of `RecordMeasurement` (`ProductPackLevelMeasured`). `409 stale-measurement` for an older reading. |
| `ResolveGTIN` | query | `GET /gtins/{gtin}`; MCP `resolve_gtin` | Normalises the GTIN to 14 digits and answers the SKU and the level it identifies, retired levels included (`404 gtin-not-found`). |
| `GetProduct` | query | `GET /products/{sku}`, `GET /products/{sku}/classification`, `GET /products/{sku}/physical-profile`, `GET /products/{sku}/pack-levels` | Loads one product; the handlers render the whole product, its classification (`404 product-classification-not-found` when unclassified), its physical profile or its pack levels. |
| `ListProducts` | query | `GET /products` | Pages by SKU in byte order (`limit` 1..500, default 100; opaque cursor = base64url of the last SKU), optionally filtered by `handlingTag` and `classified`. |

No use case is triggered by Kafka: the ADR 0003 migration use case
`ImportLegacyClassification` and its importer were removed at stage E
(2026-10-17). Every write enters through REST.

Every write is idempotent: the same request twice changes nothing the second
time. A version race between two writers is `repository.ErrConcurrentModification`
(`409 concurrent-modification`).

## Triggers, inputs, invariants and events

Each unit event below is written to the outbox twice in the same transaction (once
for `warehouse.product-master.events`, once for
`warehouse.product-master.analytics`) under one CloudEvents `id`; the three pack
level events are written once, to the integration topic only (ADR 0011). The type is
`com.warehouse.wms.product-master.product.<EventName>`. A command that changes
nothing raises no event and does not bump `version`.

| Use case | Trigger | Inputs | Invariants checked (domain error, REST slug) | Events raised |
| --- | --- | --- | --- | --- |
| `RegisterProduct` | REST `PUT /products/{sku}` | `sku`, `description` | SKU 1..64 characters without whitespace, control characters or `/` (`invalid-sku`); description at most 200 characters, no control characters (`invalid-description`) | new SKU: `ProductRegistered` (version 1); existing SKU with a new description: `ProductDescriptionChanged`; same description: none |
| `ClassifyProduct` | REST `PUT /products/{sku}/classification` | `sku`, `handlingTags`, `temperatureClass`, `dotHazardClass` | at least one tag, closed tag set, no duplicates; `temperatureClass` iff `TemperatureSensitive`, closed class set; `dotHazardClass` 1..9 and only with `Hazmat` (one slug per rule, see [troubleshooting](/docs/operations/troubleshooting#rest-errors)); product registered (`product-not-found`); version guard | `ProductClassified` (source `native`), unless the same native classification is already set; confirming a `legacy-import` classification with the same values makes it native and is a change |
| `DeclareDimensions` | REST `PUT /products/{sku}/dimensions/declared` | `sku`, `lengthMm`, `widthMm`, `heightMm`, `weightG` | each dimension 1..20000 mm (`invalid-dimension`), weight 1..2000000 g (`invalid-weight`); all four fields present (`malformed-request`); product registered | `ProductDimensionsDeclared` (payload: the whole physical profile), unless the values are identical |
| `RecordMeasurement` | REST `PUT /products/{sku}/dimensions/measured` | `sku`, the four dimensions, `measuredAt`, optional `deviceId` | the same bounds; `measuredAt` present (`malformed-request`) and not after the service clock (`measured-at-in-future`); `deviceId` at most 64 characters; not older than the current measurement (`409 stale-measurement`); product registered | `ProductMeasured` (whole physical profile, with `discrepancy` when the measured volume or weight differs from the declared one by more than 10% of the declared value); an identical measurement raises none; a different reading with the same `measuredAt` replaces it |
| `DeclarePackLevel` | REST `PUT /products/{sku}/pack-levels/{level}`; MCP `declare_pack_level` | `sku`, `level`, `gtin`, `childGtin`, `qtyOfChild`, `unitsPerPack`, `dims`, `isOrderable`, `isDespatchUnit` | level one of `EACH`/`INNER`/`CASE`/`PALLET` (`invalid-pack-level`); GTIN 8, 12, 13 or 14 digits with a valid mod-10 check digit (`invalid-gtin`), indicator digit 9 rejected (`variable-measure-unsupported`); EACH has no child and one unit per pack, every other level `qtyOfChild` 2..1000000 (`invalid-pack-quantity`) and a `childGtin` that is an ACTIVE lower level of this product (`409 pack-level-order`); `unitsPerPack` equals `qtyOfChild` times the child's factor (`invalid-pack-quantity`); a GTIN identifies exactly one (sku, level), fleet-wide and for ever, retired ones included (`409 gtin-already-assigned`); the content quantity of an ACTIVE level never changes, a new quantity is a new GTIN (`409 pack-quantity-immutable`); declared weight not below `qtyOfChild` times the child's known weight (`409 pack-dimensions-below-contents`); product registered | `ProductPackLevelDeclared`; a new GTIN at an active level first raises `ProductPackLevelRetired` (reason `superseded`) at `version` + 1 and the Declared at + 2; the identical declaration raises none; changed flags or dimensions of an active level raise a new Declared |
| `RetirePackLevel` | REST `POST /products/{sku}/pack-levels/{level}/retirements`; MCP `retire_pack_level` | `sku`, `level`, `reason` | level valid; reason 1..200 characters without control characters, empty means `retired` (`invalid-reason`); the level was declared (`404 pack-level-not-found`) | `ProductPackLevelRetired`, unless the level is already retired |
| `RecordPackLevelMeasurement` | REST `PUT /products/{sku}/pack-levels/{level}/measured` | `sku`, `level`, the four dimensions, `measuredAt`, optional `deviceId` | the `RecordMeasurement` rules; an ACTIVE level exists (`404 pack-level-not-found`); measured weight not below the contents (`409 pack-dimensions-below-contents`) | `ProductPackLevelMeasured`, unless the reading is identical |
| `ResolveGTIN` | REST `GET /gtins/{gtin}`; MCP `resolve_gtin` | `gtin` | valid GTIN, not indicator 9; held by some pack level (`404 gtin-not-found`) | none |
| `GetProduct` | REST `GET /products/{sku}`, `/classification`, `/physical-profile`, `/pack-levels`; MCP `get_product`, `get_product_classification`, `get_physical_profile`, `get_pack_levels` | `sku` | valid SKU (`invalid-sku`); exists (`product-not-found`); classified for the classification view (`product-classification-not-found`) | none |
| `ListProducts` | REST `GET /products`; MCP `list_products` | `limit` (default 100, max 500), `cursor`, `handlingTag`, `classified` | limit range, decodable cursor, known tag (`malformed-request`) | none |

The analytics side has no use case in `internal/application`: the projector
folds events straight into `report.Projection`
(`internal/analytics/report`, `internal/adapters/outbound/analyticsstore`) and
the reports server reads through `report.Reader`.

Acceptance scenarios for every use case are in `features/*.feature`
(godog, `features/features_test.go`).

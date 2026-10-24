# ADR 0011: Packaging hierarchy with GS1 GTIN; eaches are the ledger unit

## Status

Proposed (2026-10-23). Nothing in this ADR is built; it pins the design so the
dependent contexts (inbound-receiving, inventory-storage and the other
consumers listed below) can be specified against stable names. Every change is
additive and optional: REST, MCP and the CloudEvents envelope
([ADR 0004](./0004-cloudevents-envelope-and-type-catalogue.md)) keep their
meaning and `v1` stays `v1`. The fleet-wide contract lives in
`warehouse-systems/.hermes/plans/mastercase-briefs/contracts.md` (sections 0
and 1); the adoption plan is
`warehouse-systems/.hermes/plans/2026-10-23_225746-mastercase-gtin-handling.md`.

## Context

Today a `Product` is `sku`, `description`, `classification` and a
`PhysicalProfile` ([ADR 0002](./0002-physical-profile-declared-vs-measured.md))
whose dimensions and weight are per unit only. There is no GTIN/EAN and no
notion of a packaging level anywhere in `internal/domain/product`. Every
quantity in the fleet is "units of the SKU".

Sellers will start sending master cartons (mastercases): one outer carton with
N identical sellable units, sometimes with inner packs of M units. Handling
them is not a receiving tweak. It needs a packaging hierarchy with GS1
identifiers owned by the context that owns the SKU, so that receiving,
storage, replenishment and picking can resolve a scanned barcode to a SKU and
a number of units, without breaking any ledger that already counts units.

Industry practice this follows (full list in the plan, section 2):

- GS1 packaging levels are Each, Inner pack, Case (all items share one GTIN),
  Mixed case, Pallet [1]. GDSN models them with a unit descriptor per level, a
  child GTIN and the quantity of the next lower level; orderable and despatch
  are flags, not implied by the level [7].
- A case gets its own GTIN-14 (indicator digit 1 to 8 plus the contained
  item's company prefix and item reference plus a check digit). Indicator 9 is
  reserved for variable-measure items. A GTIN-14 is printed in ITF-14 or
  GS1-128, never as EAN/UPC. Mixed cases get no derived GTIN-14 [2][4].
- The GTIN Management Standard rule on pack/case quantity: changing the number
  of items in a case requires a new GTIN for the changed level and every level
  above it [3].
- WMS practice is to keep inventory in a base unit and convert, storing the
  direct factor of each level rather than only chained ones [17][18].

## Decision

1. **D1. Eaches are the ledger unit.** Every existing `qty`/`quantity` field in
   product-master and in every consumer keeps meaning eaches. New pack fields
   carry their own names (`cases`, `unitsPerPack`, `qtyOfChild`); a bare `qty`
   never changes meaning. Pack levels are conversions plus identity metadata,
   not a second ledger.

2. **D2. The packaging hierarchy is part of the `Product` aggregate.** `Product`
   gains a `PackagingHierarchy`. The entity stays `product` and the event
   subject stays the `sku`. New value objects:
   - `GTIN`: validated by this context's own value object, never trusted from a
     client.
   - `PackLevel`: the enum `EACH | INNER | CASE | PALLET`. `EACH` is itself a
     pack level: the each GTIN is the `EACH` level, so there is no separate
     "GTIN assigned" concept or event. `INNER` exists in the model; the
     receiving and picking flows of consumers implement only `EACH` and `CASE`
     in v1.
   - `PackLevelDef`: `level`, `gtin`, `childGtin`, `qtyOfChild`,
     `unitsPerPack`, optional `dims` (`lengthMm`, `widthMm`, `heightMm`,
     `weightG`), `isOrderable`, `isDespatchUnit`, `effectiveFrom`, optional
     `effectiveTo`. A level other than `EACH` has `childGtin` (the GTIN of the
     next lower level) and `qtyOfChild >= 2`. `unitsPerPack` is the direct
     factor to eaches, stored and not recomputed by chaining.

   Invariants and their errors:

   | Error | Rule |
   | --- | --- |
   | `ErrInvalidGTIN` | wrong length or mod-10 check digit |
   | `ErrGtinAlreadyAssigned` | a GTIN identifies exactly one (sku, level), fleet-wide |
   | `ErrPackQuantityImmutable` | an ACTIVE level's `qtyOfChild`/`unitsPerPack` cannot change (G7 below) |
   | `ErrPackLevelOrder` | the child level must be lower than the level |
   | `ErrVariableMeasureUnsupported` | indicator digit 9 is rejected in v1 |
   | `ErrInvalidPackQuantity` | `qtyOfChild` below 2 for a non-`EACH` level |
   | `ErrPackDimensionsBelowContents` | case weight below `qtyOfChild` times the child weight, when both are known |

   Commands: `DeclarePackLevel`, `RetirePackLevel`, `RecordPackLevelMeasurement`.
   Measurements follow the declared-versus-measured split of ADR 0002.

3. **G2. GTIN normalisation to 14 digits.** A GTIN is stored and transmitted as
   a 14-digit, zero-left-padded string (`"00012345678905"`). Accepted input
   lengths are 8, 12, 13 and 14; the mod-10 check digit is validated on input
   and the normalised form is the only form that is persisted, compared,
   indexed or published. The path value of `GET /gtins/{gtin}` is normalised the same way.

4. **G7. GS1 immutability.** Once a pack level is ACTIVE its
   `qtyOfChild`/`unitsPerPack` cannot change (`ErrPackQuantityImmutable`). A new
   content quantity is a **new GTIN** at that level plus a re-declaration of
   every level above it. The old level is end-dated (`effectiveTo`, via
   `RetirePackLevel`) and kept, so that in-flight ASNs that reference the old
   GTIN still resolve. Further rules that follow from it:
   - the `EACH` level is a pack level like any other, with `unitsPerPack = 1`;
   - variable-measure items (indicator digit 9) are rejected in v1
     (`ErrVariableMeasureUnsupported`);
   - a mixed case has **no pack GTIN**. It is a logistic unit identified by an
     SSCC and a carton manifest, which belong to inbound-receiving and
     inventory-storage, not to product-master.

## Events

All events are CloudEvents 1.0, structured mode, type
`com.warehouse.wms.product-master.product.<Event>`, on the existing topic
`warehouse.product-master.events`, key = `sku`, with the envelope of ADR 0004
and the outbox of ADR 0007 and ADR 0009. `version` is the aggregate version,
as in the existing events. Three new types are added to the catalogue:

| Type | Data |
| --- | --- |
| `ProductPackLevelDeclared` | `sku`, `level`, `gtin`, `childGtin?`, `qtyOfChild?`, `unitsPerPack`, `dims?`, `isOrderable`, `isDespatchUnit`, `effectiveFrom`, `version` |
| `ProductPackLevelRetired` | `sku`, `level`, `gtin`, `effectiveTo`, `reason`, `version` |
| `ProductPackLevelMeasured` | `sku`, `level`, `gtin`, `dims`, `measuredAt`, `deviceId?`, `version` |

`?` marks an optional field. Existing events are unchanged. Consumers:
inbound-receiving, inventory-storage, slotting-optimization,
pick-face-replenishment, fulfillment-execution, wes-work-planning,
process-path-management and labor-performance (each through its own
projection), and network-fulfillment, which maps pack facts to the retail
network only through the Vendor API and its own mapping.

## REST and MCP additions

All additive; existing routes and tools are untouched.

- `GET /gtins/{gtin}` returns `sku`, `level`, `unitsPerPack`, `childGtin?`,
  `active`, `effectiveFrom`, `effectiveTo?`. The path GTIN is normalised to 14
  digits before the lookup; an invalid one is `ErrInvalidGTIN` (problem+json).
- `PUT /products/{sku}/pack-levels/{level}` runs `DeclarePackLevel`.
- `POST /products/{sku}/pack-levels/{level}:retire` runs `RetirePackLevel`.
- `GET /products/{sku}/pack-levels` lists the levels, retired ones included.
- MCP tools: `resolve_gtin`, `get_pack_levels`, `declare_pack_level`,
  `retire_pack_level`. The first two are read-only. The last two write, which
  departs from the read-only server of [ADR 0005](./0005-mcp-server-adoption.md):
  if this ADR is accepted it amends ADR 0005 for those two tools only, and the
  index records it.

## Consequences

- **Consumers get local projections; there are no synchronous lookups.** Each
  consumer builds a local, event-fed `PackagingView` from the three new events
  and resolves a scanned GTIN to (sku, units) on its own. No service calls
  `GET /gtins/{gtin}` on a hot path. This is mandatory for
  process-path-management and labor-performance and is the fleet norm for the
  rest. The REST route and the MCP tools serve people, tooling and backfills.
- No ledger changes meaning. Stock, reservations and receipts keep counting
  eaches, so nothing that works today needs a migration.
- A new case quantity costs a new GTIN, a new `PackLevelDef` and a
  re-declaration of the levels above. This is deliberate: it is what the
  trading partners do, and it keeps old ASNs resolvable through `effectiveTo`.
- `ErrGtinAlreadyAssigned` is enforced by this context alone, on the normalised
  GTIN, in its own database; there is no cross-service check. A GTIN seen by a consumer that has no projection yet is a consumer
  discrepancy (unknown GTIN), not a lookup.
- The write side grows: three commands, three events, one table of pack levels
  per product, and the analytics read side (ADR 0006) may later report on
  products without a declared each GTIN. That report is not part of this ADR.
- Not designed here: variable-measure items, commingling of stock by GTIN across
  owners, FEFO and the `INNER` receiving and picking flows.

## Alternatives rejected

- **A case as a separate SKU.** It is what many catalogues do informally, but it
  splits one product into two masters with two ledgers, forces a conversion
  between them in every consumer, and loses the link that makes breaking a case
  conserve quantity. It also makes "this GTIN is the case of that each" an
  unchecked convention.
- **A unit-of-measure conversion table without GTINs.** It gives the factors but
  not the identifier a scanner reads, so every receiving flow would need its own
  barcode-to-SKU table. It also cannot express GS1 immutability: a table row
  can be edited in place, the very thing [3] forbids.
- **Deriving the GTIN-14 on the fly from the each GTIN.** The indicator-digit
  form looks derivable, but the case GTIN is assigned by the brand owner and
  does not always follow the pattern, mixed cases have none, and a changed
  content quantity needs a different GTIN that cannot be computed. A derived
  value would be wrong exactly when it matters.

## External references

Numbering follows the adoption plan (section "Sources"); the pages were taken
from search results on 2026-10-23 and were not machine-verified.

- [1] GS1 US, Packaging Levels in Data Hub: [https://www.help.gs1us.org/packaging-level](https://www.help.gs1us.org/packaging-level)
- [2] GS1 US, Create a Shipping Case: [https://www.help.gs1us.org/create-a-case-and-generate-a-barcode](https://www.help.gs1us.org/create-a-case-and-generate-a-barcode)
- [3] GS1 GTIN Management Standard, Pack/case quantity: [https://www.gs1.org/en/standards/id-keys/gtin/gtin-decision-support-tool/packcase-quantity](https://www.gs1.org/en/standards/id-keys/gtin/gtin-decision-support-tool/packcase-quantity)
- [4] GS1 US, An Introduction to the GTIN: [https://documents.gs1us.org/adobe/assets/deliver/urn:aaid:aem:3b583151-bc32-4223-a190-5bb1ae565b0e/Guideline-An-Introduction-to-the-Global-Trade-Item-Number-GTIN.pdf](https://documents.gs1us.org/adobe/assets/deliver/urn:aaid:aem:3b583151-bc32-4223-a190-5bb1ae565b0e/Guideline-An-Introduction-to-the-Global-Trade-Item-Number-GTIN.pdf)
- [7] GS1 GDSN Trade Item Implementation Guide: [https://ref.gs1.org/guidelines/tiig/35.0.0/](https://ref.gs1.org/guidelines/tiig/35.0.0/)
- [17] Case pack vs eaches (GTIN mix-ups): [https://queenssurgical.net/post/case-pack-vs-eaches](https://queenssurgical.net/post/case-pack-vs-eaches)
- [18] Inventory UoM and case-pack conversions: [https://oceanportlink.com/blog/inventory-unit-of-measure-case-pack-control](https://oceanportlink.com/blog/inventory-unit-of-measure-case-pack-control)

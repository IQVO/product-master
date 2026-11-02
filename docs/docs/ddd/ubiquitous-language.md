---
id: ubiquitous-language
title: Ubiquitous language
sidebar_label: Ubiquitous language
sidebar_position: 1
---

# Ubiquitous language

Use these exact names in code, API and conversation. The repository's
`.claude/rules/domain-model.md` is the agent-facing source; every term below
maps to a code identifier in `internal/domain/product` unless another path is
given. Terms whose code name differs from the spoken or wire name are flagged
in the last column.

| Term | Meaning | Code identifier | Note |
| --- | --- | --- | --- |
| **Product** | The master record of one SKU. Registered explicitly before any attribute can be set. Aggregate root. | `Product` | |
| **SKU** | The product's identity: 1..64 characters, no whitespace, control characters or `/`. Never changes. | `SKU`, `NewSKU`, `MaxSKULength` | Kafka key and CloudEvents `subject` |
| **Register** | Create a product at version 1. Re-registering an existing SKU only replaces its description. | `Register`; use case `RegisterProduct` | REST `PUT /products/{sku}` |
| **Description** | Operator text, 0..200 characters, no control characters. | `description`, `MaxDescriptionLength`, `ChangeDescription` | |
| **Classification** | How a SKU must be handled: handling tags, temperature class, DOT hazard class. Immutable value object. | `Classification`, `NewClassification` | REST `handlingTags`, event `handling_tags` |
| **Handling tag** | One member of the closed set `Hazmat`, `Fragile`, `TemperatureSensitive`, `Oversized`, `HighValue`, reported in that stable order. | `HandlingTag`, `tagOrder`, `Tags()` | |
| **Temperature class** | Storage band a `TemperatureSensitive` product needs: `Ambient`, `Chilled`, `Frozen`. Required iff `TemperatureSensitive`. | `TemperatureClass`, `NoTemperatureClass` | |
| **DOT hazard class** | Top-level US DOT hazard class 1..9, optional, only with `Hazmat`. 0 means "not recorded". | `DOTHazardClass`, `NoDOTHazardClass` | Segregation rules are NOT owned here (ADR 0001) |
| **Classification source** | Where a classification was authored: `native` (here; every write since stage E) or `legacy-import` (written by the ADR 0003 importer before stage E and not re-classified since). | `ClassificationSource`, `SourceNative`, `SourceLegacyImport` | `classification_source` / `classificationSource` |
| **Legacy import** | Historical: applying an inventory-storage classification during the migration, never overwriting `native`. | none any more: `ImportLegacyClassification` was removed at stage E (2026-10-17) | ADR 0003; only the `legacy-import` source value remains |
| **Unit dimensions** | Length, width, height in whole mm (1..20000) and weight in whole g (1..2000000) of one unit. | `UnitDimensions`, `NewUnitDimensions` | |
| **Volume** | `length x width x height` in mm3, orientation-free. | `VolumeMm3()` | `volume_mm3` / `volumeMm3` |
| **Declared dimensions** | Vendor- or steward-supplied unit dimensions. | `PhysicalProfile.Declared()`, `DeclareDimensions` | REST `.../dimensions/declared` |
| **Measurement** | Measured unit dimensions plus when (`measuredAt`) and optionally by which device (`deviceId`). | `Measurement`, `NewMeasurement`, `RecordMeasurement` | REST `.../dimensions/measured` |
| **Stale measurement** | A measurement dated before the current one; rejected. | `ErrStaleMeasurement` | `409 stale-measurement` |
| **Physical profile** | Declared dimensions plus the latest measurement, with derived effective values and discrepancy. | `PhysicalProfile` | |
| **Effective values** | Measured if present, else declared, else none. What consumers act on. | `Effective()`, `EffectiveSource()` (`measured`, `declared`, `none`) | |
| **Discrepancy** | Both parts present and measured volume or weight differs from declared by more than 10 % of declared. Information, not a rejection. | `Discrepancy()`, `exceedsTenPercent` | |
| **Version** | Starts at 1, +1 per accepted change; guards the write and rides on every event. | `version`, `Version()`, `bump` | Consumers apply only a greater `version` |
| **No change** | A command whose result equals the current state: no event, no version bump, nothing saved. | commands returning `nil` events; `Writer.persist` | |
| **Pack level** | One packaging level of a SKU: `EACH`, `INNER`, `CASE` or `PALLET`, identified by a GTIN (ADR 0011). | `PackLevel`, `PackLevelDef`, `PackagingHierarchy` | REST `.../pack-levels/{level}` |
| **GTIN** | The 14-digit zero-left-padded GS1 number of a pack level; 8, 12, 13 and 14 digit input with a valid mod-10 check digit is accepted. One GTIN identifies exactly one (sku, level), fleet-wide, for ever. | `gs1.GTIN` (`internal/domain/shared/gs1`), `ParseGTIN` | indicator digit 9 (variable measure) is rejected |
| **Units per pack** | The direct factor from a pack level to eaches (1 for EACH), stored, not recomputed by chaining. Eaches are the ledger unit; a bare quantity never means cases. | `PackLevelDef.UnitsPerPack()` | REST `unitsPerPack`, event `units_per_pack` |
| **Quantity of child** | How many packs of the child level make one of this level (at least 2). | `PackLevelDef.QtyOfChild()` | REST `qtyOfChild` |
| **Supersede** | Declaring a new GTIN at a level that is active: the old level is end-dated (`superseded`) and kept, the new one starts. The only way to change a content quantity. | `DeclarePackLevel` | `ProductPackLevelRetired` then `ProductPackLevelDeclared` |
| **Retire** | End-date the active level (`effectiveTo`); the GTIN keeps resolving, inactive, and is never reassigned. | `RetirePackLevel` | REST `POST .../retirements` |
| **Local copy** | A downstream context's own table of this context's facts, fed by events and guarded by `version`. | (in the consumers) | ADR 0001, ADR 0003 stage D |
| **Unit of work** | One atomic transaction around load, command, guarded save and outbox insert. | `ports.UnitOfWork` | |
| **Outbox** | The table events wait in until the relay publishes them. | `outbox_events`, `ports.OutboxRepository`, `outbox.Relay` | |

## Vocabulary only (not implemented)

ADR 0001 lists these as explicitly out of scope for v1, to be decided by later
ADRs: lot / serial / expiry tracking policy, shelf life, kits and bundles, velocity
class, lifecycle states (discontinue, block), and every commercial attribute
(title, price, images). Pack hierarchy, units of measure and GTINs left this list
with ADR 0011; variable-measure items (GTIN indicator digit 9), commingling of
stock by GTIN across owners and mixed cases (no pack GTIN) are still not
designed. There is also no "unclassify"
command or event in v1, and no measurement history (ADR 0002).

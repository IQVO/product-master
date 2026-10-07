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
| **Classification source** | Where a classification was authored: `native` (here) or `legacy-import` (from inventory-storage during the migration). | `ClassificationSource`, `SourceNative`, `SourceLegacyImport` | |
| **Legacy import** | Applying an inventory-storage classification during the migration; never overwrites `native`. | `ImportLegacyClassification` (aggregate method and use case) | ADR 0003, removed at stage E |
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
| **Local copy** | A downstream context's own table of this context's facts, fed by events and guarded by `version`. | (in the consumers) | ADR 0001, ADR 0003 stage D |
| **Unit of work** | One atomic transaction around load, command, guarded save and outbox insert. | `ports.UnitOfWork` | |
| **Outbox** | The table events wait in until the relay publishes them. | `outbox_events`, `ports.OutboxRepository`, `outbox.Relay` | |

## Vocabulary only (not implemented)

ADR 0001 lists these as explicitly out of scope for v1, to be decided by later
ADRs: pack hierarchy and units of measure (each, inner, case, pallet),
lot / serial / expiry tracking policy, shelf life, kits and bundles, velocity
class, lifecycle states (discontinue, block), barcodes and GTINs, and every
commercial attribute (title, price, images). There is also no "unclassify"
command or event in v1, and no measurement history (ADR 0002).

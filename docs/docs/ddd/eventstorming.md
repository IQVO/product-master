---
id: eventstorming
title: EventStorming
sidebar_label: EventStorming
sidebar_position: 16
---

# EventStorming (design level)

Notation from the ddd-crew
[EventStorming glossary and cheat sheet](https://github.com/ddd-crew/eventstorming-glossary-cheat-sheet).
Three processes, each read left to right. Hotspots are real, documented gaps
(ADRs, code comments), not invented ones.

## Legend

```mermaid
flowchart LR
  A([Actor]):::actor
  C[Command]:::command
  G[Aggregate]:::aggregate
  E[Domain event]:::event
  P[Policy]:::policy
  R[Read model]:::readmodel
  X[External system]:::external
  H[Hotspot]:::hotspot
  classDef actor fill:#fff7a8,stroke:#b8a400,color:#000,font-size:11px
  classDef command fill:#4aa3df,stroke:#1f6fa3,color:#fff
  classDef aggregate fill:#f7d84a,stroke:#b39b12,color:#000
  classDef event fill:#f6a04d,stroke:#b8661c,color:#000
  classDef policy fill:#c39bd3,stroke:#7d4f91,color:#000
  classDef readmodel fill:#7dcea0,stroke:#2f8a57,color:#000
  classDef external fill:#f1948a,stroke:#a9483e,color:#000
  classDef hotspot fill:#e74c3c,stroke:#8e1f14,color:#fff
```

Source: ddd-crew cheat-sheet colours as specified for the fleet.
Omits: nothing; this is the key for the three diagrams below.

## 1. Register and classify a SKU

```mermaid
flowchart LR
  ST([Master-data steward]):::actor --> C1[RegisterProduct]:::command
  C1 --> A1[Product]:::aggregate
  A1 --> E1[ProductRegistered]:::event
  A1 --> E2[ProductDescriptionChanged]:::event
  ST --> C2[ClassifyProduct]:::command
  C2 --> A1
  A1 --> E3[ProductClassified]:::event
  E1 --> P1[Every accepted change goes to warehouse.product-master.events through the outbox]:::policy
  E2 --> P1
  E3 --> P1
  P1 --> INV[inventory-storage]:::external
  P1 --> OM[order-management]:::external
  P1 --> WWP[wes-work-planning]:::external
  P1 --> FE[fulfillment-execution]:::external
  A1 --> R1[GET /products, product, classification]:::readmodel
  H1[A SKU classified moments before its first stow may reach a consumer late]:::hotspot -.- P1
  H2[No unclassify command or event in v1]:::hotspot -.- C2

  classDef actor fill:#fff7a8,stroke:#b8a400,color:#000,font-size:11px
  classDef command fill:#4aa3df,stroke:#1f6fa3,color:#fff
  classDef aggregate fill:#f7d84a,stroke:#b39b12,color:#000
  classDef event fill:#f6a04d,stroke:#b8661c,color:#000
  classDef policy fill:#c39bd3,stroke:#7d4f91,color:#000
  classDef readmodel fill:#7dcea0,stroke:#2f8a57,color:#000
  classDef external fill:#f1948a,stroke:#a9483e,color:#000
  classDef hotspot fill:#e74c3c,stroke:#8e1f14,color:#fff
```

Source: `internal/application/usecases/register_product.go`,
`classify_product.go`, `writer.go`, `internal/domain/product/product.go`,
`internal/adapters/outbound/kafka/encoder.go`; ADR 0001 (Consequences:
eventual consistency).
Omits: the rejection paths (`400` slugs, `404 product-not-found`) and the
consumers' own policies. Only `ProductClassified` is consumed today.

## 2. Declare and measure the physical profile

```mermaid
flowchart LR
  ST([Master-data steward]):::actor --> C1[DeclareDimensions]:::command
  DV([Dimensioning device or operator]):::actor --> C2[RecordMeasurement]:::command
  C1 --> A1[Product]:::aggregate
  C2 --> A1
  A1 --> E1[ProductDimensionsDeclared]:::event
  A1 --> E2[ProductMeasured]:::event
  E1 --> P1[Every accepted change goes to the topic through the outbox]:::policy
  E2 --> P1
  A1 --> R1[Physical profile: effective values, effective source, discrepancy]:::readmodel
  H1[No consumer of the physical profile yet]:::hotspot -.- P1
  H2[Discrepancy tolerance fixed at 10 percent, no history of readings]:::hotspot -.- R1

  classDef actor fill:#fff7a8,stroke:#b8a400,color:#000,font-size:11px
  classDef command fill:#4aa3df,stroke:#1f6fa3,color:#fff
  classDef aggregate fill:#f7d84a,stroke:#b39b12,color:#000
  classDef event fill:#f6a04d,stroke:#b8661c,color:#000
  classDef policy fill:#c39bd3,stroke:#7d4f91,color:#000
  classDef readmodel fill:#7dcea0,stroke:#2f8a57,color:#000
  classDef external fill:#f1948a,stroke:#a9483e,color:#000
  classDef hotspot fill:#e74c3c,stroke:#8e1f14,color:#fff
```

Source: `internal/application/usecases/physical_profile.go`,
`internal/domain/product/physical.go`, ADR 0002 (Consequences).
Omits: `ErrStaleMeasurement` and `ErrMeasuredAtInFuture` (see the
[sequence diagrams](/docs/ddd/sequence-diagrams)).

## 3. Import legacy classifications (migration only)

```mermaid
flowchart LR
  OP([Operator]):::actor --> X0[inventory-storage backfill command]:::external
  X0 --> E1[legacy ProductClassified]:::event
  INV[inventory-storage]:::external --> E1
  E1 --> P1[Whenever a legacy classification arrives, import it unless native]:::policy
  P1 --> C1[ImportLegacyClassification]:::command
  C1 --> A1[Product]:::aggregate
  A1 --> E2[ProductRegistered]:::event
  A1 --> E3[ProductClassified source legacy-import]:::event
  E3 --> P2[Every accepted change goes to the topic through the outbox]:::policy
  E2 --> P2
  P2 --> INV2[inventory-storage local copy]:::external
  H1[classificationSource legacy-import marks rows never reviewed here]:::hotspot -.- E3
  H2[Importer and legacy type removed at stage E, date not set]:::hotspot -.- P1

  classDef actor fill:#fff7a8,stroke:#b8a400,color:#000,font-size:11px
  classDef command fill:#4aa3df,stroke:#1f6fa3,color:#fff
  classDef aggregate fill:#f7d84a,stroke:#b39b12,color:#000
  classDef event fill:#f6a04d,stroke:#b8661c,color:#000
  classDef policy fill:#c39bd3,stroke:#7d4f91,color:#000
  classDef readmodel fill:#7dcea0,stroke:#2f8a57,color:#000
  classDef external fill:#f1948a,stroke:#a9483e,color:#000
  classDef hotspot fill:#e74c3c,stroke:#8e1f14,color:#fff
```

Source: `internal/adapters/inbound/kafka/legacy_importer.go`,
`internal/application/usecases/import_legacy_classification.go`,
`internal/domain/product/product.go` (`ImportLegacyClassification`),
ADR 0003 (stages A, B, E and Consequences).
Omits: the processed-event claim and the retry loop (see the
[sequence diagrams](/docs/ddd/sequence-diagrams)).

## Sticky inventory

| Sticky | Kind | Code evidence |
| --- | --- | --- |
| Master-data steward, dimensioning device, operator | Actor | REST callers of `internal/adapters/inbound/http/handlers.go` |
| `RegisterProduct`, `ClassifyProduct`, `DeclareDimensions`, `RecordMeasurement`, `ImportLegacyClassification` | Command | `internal/application/usecases/*.go` |
| `Product` | Aggregate | `internal/domain/product/product.go` |
| `ProductRegistered`, `ProductDescriptionChanged`, `ProductClassified`, `ProductDimensionsDeclared`, `ProductMeasured` | Domain event (published) | `internal/domain/product/events.go`, `internal/adapters/outbound/kafka/encoder.go` |
| legacy `ProductClassified` | Domain event (consumed) | `TypeLegacyProductClassified` in `legacy_importer.go` |
| Publish every accepted change through the outbox | Policy | `Writer.persist` in `writer.go`, `outbox.Relay` |
| Import unless native | Policy | `LegacyImporter.HandleMessage`, `Product.ImportLegacyClassification` |
| Product, classification, physical profile, product list | Read model | `GetProduct`, `ListProducts` in `queries.go` |
| inventory-storage, order-management, wes-work-planning, fulfillment-execution | External system | their `ProductClassified` consumers on `develop` |
| Late classification before first stow | Hotspot | ADR 0001, Consequences |
| No unclassify | Hotspot | `Product` has no method that removes a classification, and ADR 0004 lists no such event |
| No physical-profile consumer | Hotspot | ADR 0002, Consequences ("later phases") |
| Fixed 10 % tolerance, no measurement history | Hotspot | ADR 0002 rule 6 and Consequences |
| `legacy-import` rows never reviewed | Hotspot | ADR 0003, Consequences |
| Stage E not scheduled | Hotspot | ADR 0003, stage E ("after the cluster is verified") |

---
id: context-map
title: Context map
sidebar_label: Context map
sidebar_position: 13
---

# Context map

This context's slice of the fleet map, following ddd-crew
[Context Mapping](https://github.com/ddd-crew/context-mapping). Every edge is
labelled `U` (upstream) / `D` (downstream) with the pattern on each side and
the technology. Solid edges are live in code, dotted edges are migration-only,
and grey nodes are relationships ADR 0001 names that have no code yet.
Neighbour classifications come from the warehouse-docs contexts table.

```mermaid
flowchart LR
  PM(("product-master<br/>Supporting"))

  INVU["inventory-storage<br/>Core<br/>as legacy source"]
  INV["inventory-storage<br/>Core<br/>as local-copy consumer"]
  OM["order-management<br/>Generic/Supporting"]
  WWP["wes-work-planning<br/>Core"]
  FE["fulfillment-execution<br/>Core"]
  OPS["warehouse-ops-agent Supporting<br/>warehouse-console frontend shell"]

  INVU -. "U: legacy PL / D: Conformist, migration only<br/>Kafka legacy ProductClassified" .-> PM
  PM -- "U: OHS+PL / D: local copy<br/>Kafka ProductClassified" --> INV
  PM -- "U: OHS+PL / D: local copy<br/>Kafka ProductClassified" --> OM
  PM -- "U: OHS+PL / D: local copy<br/>Kafka ProductClassified" --> WWP
  PM -- "U: OHS+PL / D: local copy<br/>Kafka ProductClassified" --> FE
  PM ~~~ OPS

  classDef absent fill:#eeeeee,stroke:#999999,color:#555555,stroke-dasharray: 4 4
  class OPS absent
```

Source: `docs/adr/0001-product-master-bounded-context.md` (Context map),
`docs/adr/0003-migration-from-inventory-storage.md`,
`internal/adapters/inbound/kafka/legacy_importer.go`,
`internal/adapters/outbound/kafka/encoder.go`, `cmd/api/main.go`; on the
consumer side (all on `develop`) inventory-storage
`internal/adapters/inbound/kafka/product_master_consumer.go`, order-management
and wes-work-planning `internal/adapters/inbound/kafka/product_classification_consumer.go`,
fulfillment-execution `internal/adapters/inbound/kafka/product_classified_consumer.go`.
Omits: the Kafka broker, Kong and the operators who call the REST API.
`inventory-storage` is drawn twice only to keep the upstream and downstream
edges readable; it is one context.

## Relationships

| # | Upstream | Downstream | Patterns (U / D) | Technology | Status | Evidence |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | `product-master` | `inventory-storage` | OHS + Published Language / local copy behind its own consumer (its existing `product_classifications` table, version-guarded) | Kafka `warehouse.product-master.events`, `com.warehouse.wms.product-master.product.ProductClassified` | **Live**, opt-in by `PRODUCT_MASTER_CONSUMER_GROUP` | inventory-storage ADR 0034, `product_master_consumer.go` |
| 2 | `product-master` | `order-management` | OHS + PL / local copy behind the `ProductClassificationLookup` port | same type | **Live** with `PRODUCT_CLASSIFICATION_MODE=kafka` (default `permissive`) | order-management ADR 0036 |
| 3 | `product-master` | `wes-work-planning` | OHS + PL / local copy | same type | **Live** with `PRODUCT_CLASSIFICATION_MODE=kafka` (default `permissive`) | wes-work-planning ADR 0035 |
| 4 | `product-master` | `fulfillment-execution` | OHS + PL / local copy | same type | **Live** with `PRODUCT_CLASSIFICATION_MODE=kafka` (default `permissive`) | fulfillment-execution ADR 0039 |
| 5 | `inventory-storage` | `product-master` | legacy Published Language / **Conformist**: the importer takes inventory-storage's v1 payload field-for-field (`legacyClassifiedData`) | Kafka `warehouse.inventory.events`, `com.warehouse.wms.inventory-storage.product.ProductClassified` | **Migration only**, opt-in by `LEGACY_IMPORT_CONSUMER_GROUP`; removed at ADR 0003 stage E | `legacy_importer.go`, ADR 0003 |
| 6 | `product-master` | `warehouse-ops-agent`, `warehouse-console` | Open Host Service (REST, MCP read tools) | REST | **Named by ADR 0001, not built**: no MCP server here, no client in either repository on `develop` | ADR 0001 context map |

The other published types (`ProductRegistered`, `ProductDescriptionChanged`,
`ProductDimensionsDeclared`, `ProductMeasured`) have no consumer yet.

There is no Shared Kernel and no Partnership: no sibling Go package is
imported (the legacy payload is restated locally), and the published payloads
are the only shared contract. `product-master` calls no sibling at request
time, and no sibling calls it at request time (ADR 0001).

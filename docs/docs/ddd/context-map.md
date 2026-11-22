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
the technology. Every edge is live: built in both repositories on `develop`
and wired in the kind cluster (warehouse-infra `terraform/apps.yaml` and
`helm-values/`). There is no upstream context any more: the migration-only
edge from inventory-storage (ADR 0003 legacy importer) was removed at stage E.
Neighbour classifications come from the warehouse-docs subdomain
classification table.

```mermaid
flowchart LR
  PM(("product-master<br/>Supporting"))

  INV["inventory-storage<br/>Core"]
  OM["order-management<br/>Generic/Supporting"]
  WWP["wes-work-planning<br/>Core"]
  FE["fulfillment-execution<br/>Core"]
  SO["slotting-optimization<br/>Supporting"]
  IR["inbound-receiving<br/>Supporting"]
  PFR["pick-face-replenishment<br/>Supporting"]
  NIP["network-inventory-planning<br/>Core"]
  RP["returns-processing<br/>Supporting"]
  OPS["warehouse-ops-agent<br/>Supporting, read-only"]
  CON["warehouse-console<br/>shell hosting productmaster_mfe"]

  PM -- "U: OHS+PL / D: local copy<br/>Kafka ProductClassified,<br/>ProductPackLevelDeclared, ProductPackLevelRetired" --> INV
  PM -- "U: OHS+PL / D: local copy<br/>Kafka ProductClassified" --> OM
  PM -- "U: OHS+PL / D: local copy<br/>Kafka ProductClassified,<br/>ProductPackLevelDeclared, ProductPackLevelRetired,<br/>ProductPackLevelMeasured" --> WWP
  PM -- "U: OHS+PL / D: local copy<br/>Kafka ProductClassified,<br/>ProductPackLevelDeclared, ProductPackLevelRetired" --> FE
  PM -- "U: OHS+PL / D: local copy<br/>Kafka ProductClassified,<br/>ProductDimensionsDeclared, ProductMeasured,<br/>ProductPackLevelDeclared, ProductPackLevelRetired,<br/>ProductPackLevelMeasured" --> SO
  PM -- "U: OHS+PL / D: local copy<br/>Kafka ProductRegistered,<br/>ProductPackLevelDeclared, ProductPackLevelRetired" --> IR
  PM -- "U: OHS+PL / D: local copy<br/>Kafka ProductPackLevelDeclared, ProductPackLevelRetired" --> PFR
  PM -- "U: OHS+PL / D: local copy<br/>Kafka ProductPackLevelDeclared, ProductPackLevelRetired" --> NIP
  PM -- "U: OHS+PL / D: local copy<br/>Kafka ProductClassified" --> RP
  PM -- "U: OHS / D: Conformist, schema-pinned client<br/>MCP read tools" --> OPS
  PM -- "U: ships its own remote / D: hosts it<br/>Module Federation, the remote calls REST via Kong" --> CON
```

Source: `docs/adr/0001-product-master-bounded-context.md` (Context map),
`docs/adr/0003-migration-from-inventory-storage.md` (stage E),
`docs/adr/0005-mcp-server-adoption.md`,
`internal/adapters/outbound/kafka/encoder.go`, `cmd/api/main.go`,
`cmd/mcp/main.go`, `web/vite.config.ts`; on the
consumer side (all on `develop`) inventory-storage
`internal/adapters/inbound/kafka/product_master_consumer.go`, order-management
and wes-work-planning `internal/adapters/inbound/kafka/product_classification_consumer.go`,
fulfillment-execution `internal/adapters/inbound/kafka/product_classified_consumer.go`,
slotting-optimization and inbound-receiving
`internal/adapters/inbound/kafka/product_consumer.go`, and for the pack level
types inbound-receiving `packaging_consumer.go`, slotting-optimization
`packlevel_consumer.go`, pick-face-replenishment `productmaster_consumer.go`,
fulfillment-execution, network-inventory-planning and wes-work-planning
`pack_level_consumer.go`, returns-processing
`internal/adapters/inbound/kafka/product_classes.go`,
warehouse-ops-agent `internal/adapters/outbound/mcpclient/product_master.go`,
warehouse-console `src/App.tsx` and `vite.config.ts` (`productmaster_mfe`).
Omits: the Kafka broker, Kong, the web gateway and the operators who call the
REST API directly.

## Relationships

| # | Upstream | Downstream | Patterns (U / D) | Technology | Status | Evidence |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | `product-master` | `inventory-storage` | OHS + Published Language / local copy behind its own consumer (its existing `product_classifications` table, version-guarded) | Kafka `warehouse.product-master.events`, `com.warehouse.wms.product-master.product.ProductClassified`, plus `ProductPackLevelDeclared` and `ProductPackLevelRetired` into its `PackagingView` (inventory-storage ADR 0046) | **Live**: the consumer starts only when `PRODUCT_MASTER_CONSUMER_GROUP` is set; warehouse-infra sets it (`productMasterConsumerGroup: inventory-storage-product-master`) | inventory-storage ADR 0034, `product_master_consumer.go`, `cmd/inventory/productmaster.go` |
| 2 | `product-master` | `order-management` | OHS + PL / local copy behind the `ProductClassificationLookup` port | same type | **Live** with `PRODUCT_CLASSIFICATION_MODE=kafka` (binary default `permissive`; warehouse-infra sets `kafka`) | order-management ADR 0036, `cmd/order/product_classification.go` |
| 3 | `product-master` | `wes-work-planning` | OHS + PL / local copy | same type, plus `ProductPackLevelDeclared`, `ProductPackLevelRetired` and `ProductPackLevelMeasured` into its `PackView` (ADR 0045, group `PACK_VIEW_CONSUMER_GROUP`) | **Live** with `PRODUCT_CLASSIFICATION_MODE=kafka` (binary default `permissive`; warehouse-infra sets `kafka` for `cmd/wes` and `cmd/mcp`) | wes-work-planning ADR 0035, `cmd/wes/classification.go` |
| 4 | `product-master` | `fulfillment-execution` | OHS + PL / local copy | same type, plus `ProductPackLevelDeclared` and `ProductPackLevelRetired` into its `PackView` (ADR 0054, group `PACK_VIEW_CONSUMER_GROUP`, required) | **Live**: `kafka` is the only mode (`permissive`, `off` and `http` are boot errors) and `PRODUCT_CLASSIFICATION_CONSUMER_GROUP` is required | fulfillment-execution ADR 0039, `cmd/execution/classification.go` |
| 5 | `product-master` | `slotting-optimization` | OHS + PL / local copy of the handling class and the effective unit size | `ProductClassified`, `ProductDimensionsDeclared`, `ProductMeasured` on the same topic, plus the three pack level types into its `PackLevelView` (ADR 0012) | **Live**: `PRODUCT_MODE=kafka` and `PACK_LEVEL_MODE=kafka` are the only modes; `PRODUCT_CONSUMER_GROUP` and `PACK_LEVEL_CONSUMER_GROUP` are required | slotting-optimization ADR 0003 and 0012, `cmd/api/main.go` (`consumerSpecs`) |
| 6 | `product-master` | `inbound-receiving` | OHS + PL / `known_skus` local copy (an ASN line for an unknown SKU is rejected) | `ProductRegistered` on the same topic, plus `ProductPackLevelDeclared` and `ProductPackLevelRetired` into its packaging view (inbound-receiving ADR 0013) | **Live**: `PRODUCT_MODE=kafka` is the only mode and `PRODUCT_CONSUMER_GROUP` is required; `PACKAGING_CONSUMER_GROUP` is required too | inbound-receiving ADR 0003 and 0013, `cmd/api/main.go` |
| 7 | `product-master` | `pick-face-replenishment` | OHS + PL / local packaging copy (the eaches factor per pack level) | `ProductPackLevelDeclared` and `ProductPackLevelRetired` on the same topic | **Live**: `PACK_LEVEL_MODE=kafka` is the only mode and `PACK_LEVEL_CONSUMER_GROUP` is required; warehouse-infra sets `packLevelConsumerGroup: pick-face-replenishment-pack-levels` | pick-face-replenishment ADR 0009, `internal/adapters/inbound/kafka/productmaster_consumer.go` |
| 8 | `product-master` | `network-inventory-planning` | OHS + PL / local `pack_levels` view | `ProductPackLevelDeclared` and `ProductPackLevelRetired` on the same topic | **Live**: `PACK_LEVEL_CONSUMER_GROUP` is required; warehouse-infra sets `packLevelConsumerGroup: network-inventory-planning-pack-level` | network-inventory-planning ADR 0017, `internal/adapters/inbound/kafka/pack_level_consumer.go` |
| 9 | `product-master` | `returns-processing` | OHS + PL / local `product_classifications` copy (hazmat flag, DOT class and temperature class for the disposition policy) | `ProductClassified` on the same topic (the only type it acts on) | **Live**: `PRODUCT_CLASSIFICATION_CONSUMER_GROUP` is required; warehouse-infra sets `productClassificationConsumerGroup: returns-processing-product-classification` | returns-processing ADR 0003, `internal/adapters/inbound/kafka/product_classes.go` |
| 10 | `product-master` | `warehouse-ops-agent` | Open Host Service (read-only MCP, ADR 0005) / Conformist: a schema-pinned client of the four read tools (`get_product`, `list_products`, `get_product_classification`, `get_physical_profile`); its `find_master_data_gaps` tool and `GET /master-data-gaps` page through `list_products` | MCP Streamable HTTP, `product-master-mcp:8090/mcp` (`PRODUCT_MASTER_MCP_ENDPOINT`) | **Live** and deployed; unset endpoint = feature off | warehouse-ops-agent ADR 0020, `cmd/mcp/main.go` |
| 11 | `product-master` | `warehouse-console` | this context ships its own UI, the `productmaster_mfe` Module Federation remote (`web/`); the console shell hosts it on `/product-master/*` behind a Product Master tile | Module Federation (`/mfes/product-master/`); the remote calls this context's REST API through Kong (`/api/product-master`) | **Live** and deployed | warehouse-console #66, `web/vite.config.ts`, `web/src/config.ts` |

`ProductDescriptionChanged` is the only published type with no consumer: every
consumer above ignores it by type.

There is no Shared Kernel and no Partnership: no sibling Go package is
imported, and the published payloads are the only shared contract.
`product-master` calls no sibling at request time, and no domain context calls
it at request time (ADR 0001); only the read-side Customers do (rows 10 and 11:
the ops-agent over MCP, the console remote over REST).

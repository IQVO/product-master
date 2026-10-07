---
id: downstream-consumers
title: Downstream consumers
sidebar_position: 1
---

# Downstream consumers

`product-master` is upstream of four contexts through its Published Language,
`warehouse.product-master.events`. None of them calls this service at request
time: each keeps a **local copy** keyed by SKU, fed by a Kafka consumer and
guarded by `version` (ADR 0001, ADR 0003 stage D). The table reflects each
consumer's code on its `develop` branch.

| Consumer | Type consumed | Consumer group env | Mode switch | What the copy is for | Its ADR |
| --- | --- | --- | --- | --- | --- |
| `inventory-storage` (Core) | `com.warehouse.wms.product-master.product.ProductClassified` | `PRODUCT_MASTER_CONSUMER_GROUP` (unset = not started; needs `DATABASE_URL` and `KAFKA_BROKERS`) | none | its existing `product_classifications` table, read by `StowStock` for placement (hazmat zone, temperature class) and same-bin DOT segregation | 0034 |
| `order-management` (Generic/Supporting) | `...product.ProductClassified` | `PRODUCT_CLASSIFICATION_CONSUMER_GROUP` | `PRODUCT_CLASSIFICATION_MODE=kafka` or `permissive` (default); `http` is rejected at boot | intake enrichment: derived product attributes for path eligibility routing | 0036 |
| `wes-work-planning` (Core) | `...product.ProductClassified` | `PRODUCT_CLASSIFICATION_CONSUMER_GROUP` | `PRODUCT_CLASSIFICATION_MODE=kafka` or `permissive` (default) | release-time capabilities and the fragile flag | 0035 |
| `fulfillment-execution` (Core) | `...product.ProductClassified` | `PRODUCT_CLASSIFICATION_CONSUMER_GROUP` | `PRODUCT_CLASSIFICATION_MODE=kafka` or `permissive` (default); `http` is rejected at boot | seal-time package segregation | 0039 |

Classifications of neighbours come from the warehouse-docs contexts table.

Evidence (each on the consumer's `develop`):
`inventory-storage` `internal/adapters/inbound/kafka/product_master_consumer.go`
and `cmd/inventory/productmaster.go`;
`order-management` `internal/adapters/inbound/kafka/product_classification_consumer.go`
and `cmd/order/product_classification.go`;
`wes-work-planning` `internal/adapters/inbound/kafka/product_classification_consumer.go`
and `cmd/wes/classification.go`;
`fulfillment-execution` `internal/adapters/inbound/kafka/product_classified_consumer.go`
and `cmd/execution/classification.go`.

## Events with no consumer yet

`ProductRegistered`, `ProductDescriptionChanged`, `ProductDimensionsDeclared`
and `ProductMeasured` are published but no sibling consumes them today. ADR
0002 names the intended later uses of the physical profile:
`fulfillment-execution` (expected package weight = effective unit weight x
scanned quantity), `inventory-storage` (unit volume against slot capacity at
stow) and `warehouse-planning` (cube-based storage capacity). None is built.

## Operators, console and agents

ADR 0001 lists `warehouse-ops-agent` and `warehouse-console` as Open Host
Service customers (REST and MCP read tools). On `develop` this repository
serves REST only; neither of those repositories calls it yet.

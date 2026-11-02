---
id: downstream-consumers
title: Downstream consumers
sidebar_position: 1
---

# Downstream consumers

`product-master` is upstream of six contexts through its Published Language,
`warehouse.product-master.events`. None of them calls this service at request
time: each keeps a **local copy** keyed by SKU, fed by a Kafka consumer and
guarded by `version` (ADR 0001, ADR 0003 stage D). The table reflects each
consumer's code on its `develop` branch and its wiring in warehouse-infra
`develop` (`terraform/apps.yaml` deploys all six; the values file named in the
last column sets the switch).

| Consumer | Types consumed | Consumer group env | Mode switch (binary) | Wired in warehouse-infra | What the copy is for | Its ADR |
| --- | --- | --- | --- | --- | --- | --- |
| `inventory-storage` (Core) | `com.warehouse.wms.product-master.product.ProductClassified` | `PRODUCT_MASTER_CONSUMER_GROUP` (unset = not started; needs `DATABASE_URL` and `KAFKA_BROKERS`) | none | yes: `productMasterConsumerGroup: inventory-storage-product-master` (`helm-values/inventory-storage.yaml`) | its existing `product_classifications` table, read by `StowStock` for placement (hazmat zone, temperature class) and same-bin DOT segregation | 0034 |
| `order-management` (Generic/Supporting) | `...product.ProductClassified` | `PRODUCT_CLASSIFICATION_CONSUMER_GROUP` | `PRODUCT_CLASSIFICATION_MODE=kafka` or `permissive` (default); `http` is rejected at boot | yes: `productClassification.mode: kafka` (`helm-values/order-management.yaml`) | intake enrichment: derived product attributes for path eligibility routing | 0036 |
| `wes-work-planning` (Core) | `...product.ProductClassified` | `PRODUCT_CLASSIFICATION_CONSUMER_GROUP` | `PRODUCT_CLASSIFICATION_MODE=kafka` or `permissive` (default) | yes: `PRODUCT_CLASSIFICATION_MODE=kafka` for `cmd/wes` and `cmd/mcp` (`helm-values/wes-work-planning.yaml`) | release-time capabilities and the fragile flag | 0035 |
| `fulfillment-execution` (Core) | `...product.ProductClassified` | `PRODUCT_CLASSIFICATION_CONSUMER_GROUP` (required) | `PRODUCT_CLASSIFICATION_MODE`: `kafka` (default) is the only mode; `permissive`, `off` and `http` are boot errors | yes: `PRODUCT_CLASSIFICATION_MODE=kafka` (`helm-values/fulfillment-execution.yaml`) | seal-time package segregation | 0039 |
| `slotting-optimization` (Supporting) | `...product.ProductClassified`, `...product.ProductDimensionsDeclared`, `...product.ProductMeasured` | `PRODUCT_CONSUMER_GROUP` (required) | `PRODUCT_MODE`: `kafka` (default) is the only mode; `permissive` and `off` are boot errors | yes: `productMode: kafka`, `productConsumerGroup: slotting-optimization-product` (`helm-values/slotting-optimization.yaml`) | handling class and effective unit size (the physical profile) of each SKU for slot plans | 0003 |
| `inbound-receiving` (Supporting) | `...product.ProductRegistered` | `PRODUCT_CONSUMER_GROUP` (required) | `PRODUCT_MODE`: `kafka` (default) is the only mode; `permissive` and `off` are boot errors | yes: `productMode: kafka`, `productConsumerGroup: inbound-receiving-product` (`helm-values/inbound-receiving.yaml`) | `known_skus`: an ASN line for an unknown SKU is rejected at registration | 0003 |

Classifications of neighbours come from the warehouse-docs subdomain
classification table.

Every message carries the CloudEvents `content-type` header and, when the
writing request had an active span, the W3C `traceparent` (plus
`tracestate`/`baggage` when present) of that request
([ADR 0009](/docs/adr/0009-trace-context-crosses-the-outbox)). A consumer that
starts a span should call `otel.GetTextMapPropagator().Extract` on the message
headers first; no consumer may require the header.

Evidence (each on the consumer's `develop`):
`inventory-storage` `internal/adapters/inbound/kafka/product_master_consumer.go`
and `cmd/inventory/productmaster.go`;
`order-management` `internal/adapters/inbound/kafka/product_classification_consumer.go`
and `cmd/order/product_classification.go`;
`wes-work-planning` `internal/adapters/inbound/kafka/product_classification_consumer.go`
and `cmd/wes/classification.go`;
`fulfillment-execution` `internal/adapters/inbound/kafka/product_classified_consumer.go`
and `cmd/execution/classification.go`;
`slotting-optimization` `internal/adapters/inbound/kafka/product_consumer.go`
and `cmd/api/main.go` (`consumerSpecs`);
`inbound-receiving` `internal/adapters/inbound/kafka/product_consumer.go` and
`cmd/api/main.go`.

## Events with no consumer, and unbuilt uses

`ProductDescriptionChanged` is published but no sibling consumes it; every
consumer above ignores it by type. The same holds for the three pack level
types of ADR 0011 (`ProductPackLevelDeclared`, `ProductPackLevelRetired`,
`ProductPackLevelMeasured`): published on the integration topic, no consumer yet.
ADR 0011 names the ones that will build a projection; each must ignore them by
type until then.

The physical profile (`ProductDimensionsDeclared`, `ProductMeasured`) has one
consumer, slotting-optimization. The other uses ADR 0002 names are still not
built: `fulfillment-execution` (expected package weight = effective unit
weight x scanned quantity), `inventory-storage` (unit volume against slot
capacity at stow) and `warehouse-planning` (cube-based storage capacity); none
of those repositories consumes either type on `develop`.

## Operators, console and agents

ADR 0001 lists `warehouse-ops-agent` and `warehouse-console` as Open Host
Service customers. Both are built and deployed in the kind cluster:

| Customer | Surface it uses | Evidence |
| --- | --- | --- |
| `warehouse-ops-agent` | The MCP server (`cmd/mcp`, ADR 0005; read-only but for two pack level tools, ADR 0011) at `http://product-master-mcp.warehouse-systems.svc.cluster.local:8090/mcp`, configured as `PRODUCT_MASTER_MCP_ENDPOINT`. Its client pins the four tools' schemas; its `find_master_data_gaps` MCP tool and `GET /master-data-gaps` page through `list_products` to report unclassified products and dimension discrepancies. | warehouse-ops-agent ADR 0020, `internal/adapters/outbound/mcpclient/product_master.go` |
| `warehouse-console` | Hosts this context's own remote, `productmaster_mfe` (`web/`), served at `/mfes/product-master/` and mounted on `/product-master/*` behind the Product Master tile. Its Packaging screen (`/product-master/products/{sku}/packaging`) reads and writes pack levels (`GET`/`PUT` pack-levels, `POST .../retirements`, `GET /gtins/{gtin}`). The remote calls the REST API through Kong at `/api/product-master`. | warehouse-console #66 (`src/App.tsx`, `vite.config.ts`), `web/src/config.ts` |

Neither is a domain context and neither keeps a copy: both read at request
time, which ADR 0001 allows for operators, the console and agents.

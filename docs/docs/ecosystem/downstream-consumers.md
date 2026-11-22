---
id: downstream-consumers
title: Downstream consumers
sidebar_position: 1
---

# Downstream consumers

`product-master` is upstream of nine contexts through its Published Language,
`warehouse.product-master.events`. None of them calls this service at request
time: each keeps a **local copy** keyed by SKU, fed by a Kafka consumer and
guarded by `version` (ADR 0001, ADR 0003 stage D). The table reflects each
consumer's code on its `develop` branch and its wiring in warehouse-infra
`develop` (`terraform/apps.yaml` deploys all nine; the values file named in the
last column sets the switch).

| Consumer | Types consumed | Consumer group env | Mode switch (binary) | Wired in warehouse-infra | What the copy is for | Its ADR |
| --- | --- | --- | --- | --- | --- | --- |
| `inventory-storage` (Core) | `com.warehouse.wms.product-master.product.ProductClassified`, `...product.ProductPackLevelDeclared`, `...product.ProductPackLevelRetired` | `PRODUCT_MASTER_CONSUMER_GROUP` (one consumer for all three types; unset = not started; needs `DATABASE_URL` and `KAFKA_BROKERS`) | none | yes: `productMasterConsumerGroup: inventory-storage-product-master` (`helm-values/inventory-storage.yaml`) | its existing `product_classifications` table, read by `StowStock` for placement (hazmat zone, temperature class) and same-bin DOT segregation; the pack level types feed its local `PackagingView` | 0034, 0046 |
| `order-management` (Generic/Supporting) | `...product.ProductClassified` | `PRODUCT_CLASSIFICATION_CONSUMER_GROUP` | `PRODUCT_CLASSIFICATION_MODE=kafka` or `permissive` (default); `http` is rejected at boot | yes: `productClassification.mode: kafka` (`helm-values/order-management.yaml`) | intake enrichment: derived product attributes for path eligibility routing | 0036 |
| `wes-work-planning` (Core) | `...product.ProductClassified`, `...product.ProductPackLevelDeclared`, `...product.ProductPackLevelRetired`, `...product.ProductPackLevelMeasured` | `PRODUCT_CLASSIFICATION_CONSUMER_GROUP` (`ProductClassified`); `PACK_VIEW_CONSUMER_GROUP` (pack level types, mandatory, must differ from the first) | `PRODUCT_CLASSIFICATION_MODE=kafka` or `permissive` (default) | yes: `PRODUCT_CLASSIFICATION_MODE=kafka` for `cmd/wes` and `cmd/mcp` (`helm-values/wes-work-planning.yaml`) | release-time capabilities and the fragile flag; the pack level types feed its local `PackView` | 0035, 0045 |
| `fulfillment-execution` (Core) | `...product.ProductClassified`, `...product.ProductPackLevelDeclared`, `...product.ProductPackLevelRetired` | `PRODUCT_CLASSIFICATION_CONSUMER_GROUP` (required; `ProductClassified`); `PACK_VIEW_CONSUMER_GROUP` (required; pack level types, always on) | `PRODUCT_CLASSIFICATION_MODE`: `kafka` (default) is the only mode; `permissive`, `off` and `http` are boot errors | yes: `PRODUCT_CLASSIFICATION_MODE=kafka` (`helm-values/fulfillment-execution.yaml`) | seal-time package segregation; the pack level types feed its local `PackView` | 0039, 0054 |
| `slotting-optimization` (Supporting) | `...product.ProductClassified`, `...product.ProductDimensionsDeclared`, `...product.ProductMeasured`, `...product.ProductPackLevelDeclared`, `...product.ProductPackLevelRetired`, `...product.ProductPackLevelMeasured` | `PRODUCT_CONSUMER_GROUP` (required; the first three types); `PACK_LEVEL_CONSUMER_GROUP` (required; pack level types) | `PRODUCT_MODE` and `PACK_LEVEL_MODE`: `kafka` (default) is the only mode; `permissive` and `off` are boot errors | yes: `productMode: kafka`, `productConsumerGroup: slotting-optimization-product`, `packLevelMode: kafka`, `packLevelConsumerGroup: slotting-optimization-pack-level` (`helm-values/slotting-optimization.yaml`) | handling class and effective unit size (the physical profile) of each SKU for slot plans; the pack level types feed its local `PackLevelView` | 0003, 0012 |
| `inbound-receiving` (Supporting) | `...product.ProductRegistered`, `...product.ProductPackLevelDeclared`, `...product.ProductPackLevelRetired` | `PRODUCT_CONSUMER_GROUP` (required; `ProductRegistered`); `PACKAGING_CONSUMER_GROUP` (required; pack level types) | `PRODUCT_MODE`: `kafka` (default) is the only mode; `permissive` and `off` are boot errors | yes: `productMode: kafka`, `productConsumerGroup: inbound-receiving-product`, `packagingConsumerGroup: inbound-receiving-packaging` (`helm-values/inbound-receiving.yaml`) | `known_skus`: an ASN line for an unknown SKU is rejected at registration; the pack level types feed its packaging view | 0003, 0013 |
| `pick-face-replenishment` (Supporting) | `...product.ProductPackLevelDeclared`, `...product.ProductPackLevelRetired` | `PACK_LEVEL_CONSUMER_GROUP` | `PACK_LEVEL_MODE`: `kafka` (default) is the only mode; `permissive` is rejected at boot | yes: `packLevelConsumerGroup: pick-face-replenishment-pack-levels` (`helm-values/pick-face-replenishment.yaml`) | its packaging view: the eaches factor per pack level that min/max and request quantities are converted with | 0009 |
| `network-inventory-planning` (Core) | `...product.ProductPackLevelDeclared`, `...product.ProductPackLevelRetired` | `PACK_LEVEL_CONSUMER_GROUP` | none | yes: `packLevelConsumerGroup: network-inventory-planning-pack-level` (`helm-values/network-inventory-planning.yaml`) | its local `pack_levels` view (case-multiple transfers) | 0017 |
| `returns-processing` (Supporting) | `...product.ProductClassified` (the only type it consumes) | `PRODUCT_CLASSIFICATION_CONSUMER_GROUP` (required) | none | yes: `productClassificationConsumerGroup: returns-processing-product-classification` (`helm-values/returns-processing.yaml`) | its `product_classifications` local copy: the hazmat flag, DOT hazard class and temperature class the disposition policy keys on | 0003 |

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
`cmd/api/main.go`; the pack level types are handled in
`internal/adapters/inbound/kafka/packaging_consumer.go` (inbound-receiving),
`product_master_consumer.go` (inventory-storage), `packlevel_consumer.go`
(slotting-optimization), `productmaster_consumer.go` (pick-face-replenishment),
`pack_level_consumer.go` (fulfillment-execution, network-inventory-planning and
wes-work-planning, each in its own repository);
`returns-processing` `internal/adapters/inbound/kafka/product_classes.go`.

## Events with no consumer, and unbuilt uses

`ProductDescriptionChanged` is published but no sibling consumes it; every
consumer above ignores it by type.

The three pack level types of ADR 0011 are consumed. `ProductPackLevelDeclared`
and `ProductPackLevelRetired` are consumed by seven contexts: inbound-receiving,
inventory-storage, slotting-optimization, pick-face-replenishment,
fulfillment-execution, wes-work-planning and network-inventory-planning.
`ProductPackLevelMeasured` is consumed by slotting-optimization and
wes-work-planning; the other five ignore it by type. ADR 0011 also names
`process-path-management`, `labor-performance` and `network-fulfillment` as
consumers: none of them consumes a pack level type on `develop`
(labor-performance ADR 0035 says so explicitly).

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

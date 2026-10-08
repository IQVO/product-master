# product-master

Product Master is the WMS-tier bounded context of the warehouse-systems fleet
that owns SKU-level product master data: the handling classification of a SKU
(hazmat, fragile, temperature-sensitive, oversized, high value, DOT hazard
class) and its physical profile (declared and measured unit dimensions and
weight). Hexagonal Go, Postgres, REST, Kafka (CloudEvents 1.0).

- Why it exists and how it relates to the other contexts:
  [ADR 0001](docs/adr/0001-product-master-bounded-context.md)
- Physical profile rules: [ADR 0002](docs/adr/0002-physical-profile-declared-vs-measured.md)
- Migration from inventory-storage: [ADR 0003](docs/adr/0003-migration-from-inventory-storage.md)
- Event catalogue: [ADR 0004](docs/adr/0004-cloudevents-envelope-and-type-catalogue.md)
- Read-only MCP server: [ADR 0005](docs/adr/0005-mcp-server-adoption.md)
- Analytics read side: [ADR 0006](docs/adr/0006-analytics-read-side.md)
- Contracts: [`apis/openapi.yaml`](apis/openapi.yaml) (REST),
  [`apis/asyncapi.yaml`](apis/asyncapi.yaml) (events on
  `warehouse.product-master.events`)
- Agent guides: `.claude/rules/` ; harness: [`HARNESS.md`](HARNESS.md)

## Endpoints (summary)

| Method | Path | Use case |
|---|---|---|
| GET | `/products` | ListProducts |
| PUT | `/products/{sku}` | RegisterProduct |
| GET | `/products/{sku}` | GetProduct |
| PUT | `/products/{sku}/classification` | ClassifyProduct |
| GET | `/products/{sku}/classification` | GetProduct (classification) |
| PUT | `/products/{sku}/dimensions/declared` | DeclareDimensions |
| PUT | `/products/{sku}/dimensions/measured` | RecordMeasurement |
| GET | `/products/{sku}/physical-profile` | GetProduct (physical profile) |

`cmd/product-reports` (a separate binary, see below) serves
`GET /reports/master-data-quality` and `GET /reports/freshness`.

## Where it runs (kind cluster, warehouse-infra)

Deployed in the `warehouse-systems` namespace by warehouse-infra
(`terraform/locals.tf`, `helm-values/product-master.yaml`), all from the one
image:

| Surface | How to reach it | Notes |
|---|---|---|
| REST API (`cmd/api`) | `http://localhost:8000/api/product-master/*` through Kong (strips the prefix) | `EVENT_PUBLISHER=kafka`; the legacy importer runs with group `product-master-legacy-import` |
| Console remote (`web/`, `productmaster_mfe`) | `http://localhost/mfes/product-master/` (web gateway); the console shell mounts it at `/product-master/*` (warehouse-console #66) | calls the REST API above |
| MCP server (`cmd/mcp`) | in-cluster only: `http://product-master-mcp.warehouse-systems.svc.cluster.local:8090/mcp` | read by `warehouse-ops-agent` (its ADR 0020, `PRODUCT_MASTER_MCP_ENDPOINT`) |
| Analytics projector (`cmd/product-projector`) | in-cluster only | group `product-master-analytics`, database `product_master_analytics` |
| Reports (`cmd/product-reports`) | in-cluster Service `product-master-reports` (port 80 to 8092) | see the known issue below |

Known issue: warehouse-infra also declares a Kong route
`/api/product-master/reports/*`, but requests to it are answered by the
`/api/product-master` route instead (the OLTP API has no `/reports/*` path),
so through Kong the reports return `404`. The reports service itself works
in-cluster; until the route is fixed in warehouse-infra, use
`kubectl -n warehouse-systems port-forward svc/product-master-reports 8092:80`
and call `http://localhost:8092/reports/master-data-quality`.

Downstream, inventory-storage, order-management, wes-work-planning and
fulfillment-execution keep local copies of `ProductClassified` (all deployed
in Kafka mode); see `docs/docs/ecosystem/downstream-consumers.md`.

## Running locally

```bash
go run ./cmd/api          # :8080, in-memory without DATABASE_URL, events logged without a broker
make check-fast           # quick gate
make check-all            # full local gate
make integration          # testcontainers Postgres + Kafka (Docker required)
go run ./cmd/mcp          # :8090, read-only MCP server (Streamable HTTP) over the same database
go run ./cmd/product-projector  # :8091 admin; needs ANALYTICS_DATABASE_URL + KAFKA_BROKERS (ADR 0006)
go run ./cmd/product-reports    # :8092, read-only reports; needs ANALYTICS_DATABASE_URL
```

## MCP server (ADR 0005)

`cmd/mcp` exposes four read-only tools: `get_product`, `list_products`,
`get_product_classification`, `get_physical_profile`. It never writes, never
dials Kafka and never runs the outbox relay. A governance test fails the build
on any write-verb tool name. Details: [ADR 0005](docs/adr/0005-mcp-server-adoption.md),
`.claude/rules/mcp.md`.

## Analytics read side (ADR 0006)

Every event also goes, in the same outbox transaction and under the same
CloudEvents id, to `warehouse.product-master.analytics`. `cmd/product-projector`
consumes it (fixed group `ANALYTICS_CONSUMER_GROUP`, poison to
`warehouse.product-master.analytics.dlq`) into a separate analytical database
(`analytics/migrations`, embedded), and `cmd/product-reports` serves
`GET /reports/master-data-quality` (per day: registered, classified, declared,
measured, open declared-vs-measured discrepancies; current coverage ratios)
and `GET /reports/freshness` read-only. Details:
[ADR 0006](docs/adr/0006-analytics-read-side.md).

## Packaging

- `Dockerfile` builds every `cmd/*` binary into one image (`api`, `mcp`,
  `product-projector`, `product-reports`).
- `web/`: the `productmaster_mfe` Module Federation remote (Vite base
  `/mfes/product-master/`, dev port 5191), packaged by `web/Dockerfile` (nginx).
- `charts/product-master`: the `api` Deployment and Service, plus an `mcp`
  component, the `analytics-projector` / `analytics-reports` components
  (`analytics.enabled`) and an optional `frontend` component, all off by
  default. Each Service selects exactly one
  Deployment (`charts/product-master/tests/test_service_selectors.py`).
  Chart values cover `EVENT_PUBLISHER`, `KAFKA_BROKERS`,
  `OUTBOX_RELAY_INTERVAL` and `LEGACY_IMPORT_CONSUMER_GROUP`.
- Docs site: `cd docs && npm ci && npm run build`.

Scaffolded from [warehouse-harness-template](https://github.com/IQVO/warehouse-harness-template).
Study project: not production software.

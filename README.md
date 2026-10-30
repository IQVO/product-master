# product-master

Product Master is the WMS-tier bounded context of the warehouse-systems fleet
that owns SKU-level product master data: the handling classification of a SKU
(hazmat, fragile, temperature-sensitive, oversized, high value, DOT hazard
class) and its physical profile (declared and measured unit dimensions and
weight). Hexagonal Go, Postgres, REST, Kafka (CloudEvents 1.0).

- Why it exists and how it relates to the other contexts:
  [ADR 0001](docs/adr/0001-product-master-bounded-context.md)
- Physical profile rules: [ADR 0002](docs/adr/0002-physical-profile-declared-vs-measured.md)
- Migration from inventory-storage (finished: stage E, 2026-10-17, removed the legacy importer): [ADR 0003](docs/adr/0003-migration-from-inventory-storage.md)
- Event catalogue: [ADR 0004](docs/adr/0004-cloudevents-envelope-and-type-catalogue.md)
- MCP server (read-only except two pack level tools): [ADR 0005](docs/adr/0005-mcp-server-adoption.md), amended by [ADR 0011](docs/adr/0011-packaging-hierarchy-with-gs1-gtin.md)
- Packaging hierarchy with GS1 GTINs (pack levels, `GET /gtins/{gtin}`): [ADR 0011](docs/adr/0011-packaging-hierarchy-with-gs1-gtin.md)
- A2A agent (`product_profile`, `declare_pack_level`; delegation only): [ADR 0012](docs/adr/0012-a2a-inbound-adapter.md)
- Analytics read side: [ADR 0006](docs/adr/0006-analytics-read-side.md)
- Outbox relay attempt cap: [ADR 0007](docs/adr/0007-outbox-relay-attempt-cap.md);
  projector telemetry: [ADR 0008](docs/adr/0008-projector-telemetry-and-environment.md);
  trace context across the outbox: [ADR 0009](docs/adr/0009-trace-context-crosses-the-outbox.md)
- Postgres and Kafka are the only runtime: [ADR 0010](docs/adr/0010-postgres-and-kafka-are-the-only-runtime.md)
- Contracts: [`apis/openapi.yaml`](apis/openapi.yaml) (REST),
  [`apis/asyncapi.yaml`](apis/asyncapi.yaml) (events on
  `warehouse.product-master.events`)
- Agent guides: `.claude/rules/` ; harness: [`HARNESS.md`](HARNESS.md)

## Documentation

Published site: <https://iqvo.github.io/product-master/> (sources in `docs/`).

| Page | What it covers |
|---|---|
| [Introduction](docs/docs/intro.md) | What the context owns, tier, classification, where to go next |
| [Quickstart](docs/docs/overview/quickstart.md) | Prerequisites, `make` targets, running locally, seeding, first calls |
| [Architecture and runtime](docs/docs/overview/runtime.md) | Hexagonal layers, the four binaries and ports, container diagram, data stores |
| [Configuration](docs/docs/operations/configuration.md) | Every environment variable of every binary |
| [Runbook](docs/docs/operations/runbook.md) | Deployment, migrations, Kafka topics and groups, outbox relay, DLQ, scaling, procedures |
| [Observability](docs/docs/operations/observability.md) | Metrics, traces, logs, health endpoints, suggested alerts |
| [Troubleshooting](docs/docs/operations/troubleshooting.md) | Symptom, cause, check, fix |
| [Testing](docs/docs/development/testing.md) | Test pyramid, the 16 testcontainers integration suites, `make` targets, CI jobs |
| [Integration: downstream](docs/docs/ecosystem/downstream-consumers.md), [upstream](docs/docs/ecosystem/upstream-contracts.md) | Every consumer and producer edge |
| [Use cases](docs/docs/ddd/use-cases.md) | Triggers, inputs, invariants, events |
| [Subdomain classification](docs/docs/ddd/subdomain-classification.md) | Supporting, `wms` tier, and the neighbours |
| [MCP tools](docs/docs/mcp/tools.md) | The six read-only tools and the two pack level write tools of `cmd/mcp` |
| [A2A agent](docs/adr/0012-a2a-inbound-adapter.md) | The two skills of `cmd/a2a`, their contract, error classification and boundaries |
| [ADR index](docs/adr/index.md) | Every ADR with its status |
| [DDD artifacts](docs/docs/ddd/ddd-artifacts.md) | The ddd-crew pack |

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
| GET | `/gtins/{gtin}` | ResolveGTIN (the SKU and pack level a GTIN identifies; retired ones too) |
| GET | `/products/{sku}/pack-levels` | GetProduct (pack levels, retired ones included) |
| PUT | `/products/{sku}/pack-levels/{level}` | DeclarePackLevel (`EACH`, `INNER`, `CASE`, `PALLET`) |
| POST | `/products/{sku}/pack-levels/{level}:retire` | RetirePackLevel (idempotent) |
| PUT | `/products/{sku}/pack-levels/{level}/measured` | RecordPackLevelMeasurement |

`cmd/product-reports` (a separate binary, see below) serves
`GET /reports/master-data-quality` and `GET /reports/freshness`.

## Where it runs (kind cluster, warehouse-infra)

Deployed in the `warehouse-systems` namespace by warehouse-infra
(`terraform/apps.yaml`, `helm-values/product-master.yaml`), all from the one
image:

| Surface | How to reach it | Notes |
|---|---|---|
| REST API (`cmd/api`) | `http://localhost:8000/api/product-master/*` through Kong (strips the prefix) | runs on real Postgres and Kafka only (ADR 0010); `/readyz` checks both |
| Console remote (`web/`, `productmaster_mfe`) | `http://localhost/mfes/product-master/` (web gateway); the console shell mounts it at `/product-master/*` (warehouse-console #66) | calls the REST API above. Screens: product list, register, product detail, and **Packaging** at `/product-master/products/{sku}/packaging` (pack-level hierarchy EACH → INNER → CASE → PALLET with GTIN, eaches per pack, dimensions and active/retired state; declare and retire a level; GTIN lookup via `GET /gtins/{gtin}`). Quantities are always eaches, a case reads "Case of N eaches". Retire is a `POST` (the remote's first non-`PUT` write); the chart's Kong CORS plugin already allows `POST` |
| MCP server (`cmd/mcp`) | in-cluster `http://product-master-mcp.warehouse-systems.svc.cluster.local:8090/mcp`; through Kong at `http://localhost:8000/api/mcp/product-master` (chart `mcp-route.yaml`) | read by `warehouse-ops-agent` in-cluster (its ADR 0020, `PRODUCT_MASTER_MCP_ENDPOINT`) |
| A2A agent (`cmd/a2a`) | in-cluster `http://product-master-a2a.warehouse-systems.svc.cluster.local:8093/` (Agent Card at `/.well-known/agent-card.json`); through Kong at `http://localhost:8000/api/a2a/product-master` (chart `a2a-route.yaml`, rewritten to `/`) | off by default (`a2a.enabled`); delegation only (ADR 0012) |
| Analytics projector (`cmd/product-projector`) | in-cluster only | group `product-master-analytics`, database `product_master_analytics` |
| Reports (`cmd/product-reports`) | `http://localhost:8000/api/product-master/reports/*` through Kong (chart `reports-route.yaml`, rewritten to `/reports`); in-cluster Service `product-master-reports` (port 80 to 8092) | see the note below |

Reports through Kong: warehouse-infra recorded on 2026-10-07 that
`/api/product-master/reports/*` was answered by the `/api/product-master`
route and returned `404`. The reports route has since moved into this chart
(`charts/product-master/templates/reports-route.yaml`, render condition fixed
in `8c22a05` on 2026-10-13) and has not been re-tested on a running cluster.
If it still returns `404`, use
`kubectl -n warehouse-systems port-forward svc/product-master-reports 8092:80`
and call `http://localhost:8092/reports/master-data-quality`.

Downstream, six contexts keep local copies from `warehouse.product-master.events`,
all built on their `develop` and wired in Kafka mode in warehouse-infra:
inventory-storage, order-management, wes-work-planning, fulfillment-execution
and slotting-optimization consume `ProductClassified` (slotting-optimization
also `ProductDimensionsDeclared` and `ProductMeasured`), and inbound-receiving
consumes `ProductRegistered`. `ProductDescriptionChanged` has no consumer.
Nothing flows in: the legacy classification importer from inventory-storage
was removed at stage E. See `docs/docs/ecosystem/downstream-consumers.md`.

## Running locally

```bash
DATABASE_URL=... KAFKA_BROKERS=... go run ./cmd/api   # :8080; both are mandatory (ADR 0010), the process exits at boot without them
make check-fast           # quick gate
make check-all            # full local gate
make integration          # testcontainers Postgres + Kafka (Docker only; also the CI `integration` job)
DATABASE_URL=... go run ./cmd/mcp   # :8090, MCP server (Streamable HTTP; read-only but for two pack level tools) over the same database; DATABASE_URL is mandatory
DATABASE_URL=... A2A_AGENT_URL=http://localhost:8093/ go run ./cmd/a2a   # :8093, A2A agent (JSON-RPC) over the same database; both variables are mandatory
go run ./cmd/product-projector  # :8091 admin; needs ANALYTICS_DATABASE_URL + KAFKA_BROKERS + ANALYTICS_CONSUMER_GROUP (ADR 0006)
go run ./cmd/product-reports    # :8092, read-only reports; needs ANALYTICS_DATABASE_URL
```

## MCP server (ADR 0005)

`cmd/mcp` exposes six read-only tools (`get_product`, `list_products`,
`get_product_classification`, `get_physical_profile`, `resolve_gtin`,
`get_pack_levels`) and, since [ADR 0011](docs/adr/0011-packaging-hierarchy-with-gs1-gtin.md),
two write tools: `declare_pack_level` and `retire_pack_level`. The writes run
the same use cases as REST, so they enqueue their events in the transactional
outbox; the binary still never dials Kafka and never runs the relay (the relay
in `cmd/api` publishes them). A governance test fails the build on any other
write-verb tool name. Details: [ADR 0005](docs/adr/0005-mcp-server-adoption.md),
`.claude/rules/mcp.md`.

## A2A agent (ADR 0012)

`cmd/a2a` is the third driving adapter over the same use cases as REST and
MCP: an Agent2Agent agent (JSON-RPC 2.0 over HTTP, message-only) with two
curated skills derived from the MCP surface. `product_profile` (read) answers
"what is this item and how do I handle it" for one `sku` or one scanned
`gtin`: classification, physical profile and the packaging hierarchy.
`declare_pack_level` (write, idempotent) declares one packaging level with its
GTIN, the same use case as REST and MCP, so the product row and its
`ProductPackLevelDeclared` event commit in one transaction through the
transactional outbox; the binary never runs the relay and never dials Kafka
(the relay in `cmd/api` publishes). The agent does not widen the context:
`retire_pack_level`, registration, classification and measurements stay off
A2A. A2A carries delegation only, never data sync: facts still leave as
CloudEvents, and downstream contexts keep local copies. Requests are
`{"skill":"product_profile","arguments":{"sku":"SKU-1"}}` as a data part (or a
text part holding the same JSON); unknown arguments are refused; domain
rejections come back in-band as RFC 7807-style problems with REST's status
classification. No auth (fleet-wide). The Agent Card is at
`/.well-known/agent-card.json`, the endpoint at `/` and `/a2a` (`:8093`,
`A2A_ADDR`); `A2A_AGENT_URL`, the absolute URL the card advertises, is
mandatory. Details: [ADR 0012](docs/adr/0012-a2a-inbound-adapter.md).

## Analytics read side (ADR 0006)

Every unit event (not the three pack level events of ADR 0011) also goes, in
the same outbox transaction and under the same CloudEvents id, to
`warehouse.product-master.analytics`. `cmd/product-projector`
consumes it (fixed group `ANALYTICS_CONSUMER_GROUP`, poison to
`warehouse.product-master.analytics.dlq`) into a separate analytical database
(`analytics/migrations`, embedded), and `cmd/product-reports` serves
`GET /reports/master-data-quality` (per day: registered, classified, declared,
measured, open declared-vs-measured discrepancies; current coverage ratios)
and `GET /reports/freshness` read-only. Details:
[ADR 0006](docs/adr/0006-analytics-read-side.md).

## Packaging

- `Dockerfile` builds every `cmd/*` binary into one image (`api`, `mcp`, `a2a`,
  `product-projector`, `product-reports`).
- `web/`: the `productmaster_mfe` Module Federation remote (Vite base
  `/mfes/product-master/`, dev port 5191), packaged by `web/Dockerfile` (nginx).
- `charts/product-master`: the `api` Deployment and Service, plus an `mcp`
  and an `a2a` component (`<release>-a2a`, port 8093), the `analytics-projector` / `analytics-reports` components
  (`analytics.enabled`) and an optional `frontend` component, all off by
  default. Each Service selects exactly one
  Deployment (`charts/product-master/tests/test_service_selectors.py`).
  `database.*` and `kafka.brokers` are required (the render fails naming
  the value, ADR 0010); chart values also cover `EVENT_PUBLISHER` (only `kafka`),
  `OUTBOX_RELAY_INTERVAL` and `OUTBOX_MAX_ATTEMPTS` (relay attempt cap, ADR 0007);
  the api Deployment mounts the chart ConfigMap via `envFrom`
  (`charts/product-master/tests/render_test.sh`).
- Docs site: `cd docs && npm ci && npm run build`.

Scaffolded from [warehouse-harness-template](https://github.com/IQVO/warehouse-harness-template).
Study project: not production software.

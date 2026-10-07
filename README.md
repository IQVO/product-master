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

## Running locally

```bash
go run ./cmd/api          # :8080, in-memory without DATABASE_URL, events logged without a broker
make check-fast           # quick gate
make check-all            # full local gate
make integration          # testcontainers Postgres + Kafka (Docker required)
go run ./cmd/mcp          # :8090, read-only MCP server (Streamable HTTP) over the same database
```

## MCP server (ADR 0005)

`cmd/mcp` exposes four read-only tools: `get_product`, `list_products`,
`get_product_classification`, `get_physical_profile`. It never writes, never
dials Kafka and never runs the outbox relay. A governance test fails the build
on any write-verb tool name. Details: [ADR 0005](docs/adr/0005-mcp-server-adoption.md),
`.claude/rules/mcp.md`.

## Packaging

- `Dockerfile` builds every `cmd/*` binary into one image (`api`, `mcp`).
- `charts/product-master`: the `api` Deployment and Service, plus an `mcp`
  component that is off by default. Each Service selects exactly one
  Deployment (`charts/product-master/tests/test_service_selectors.py`).
  Chart values cover `EVENT_PUBLISHER`, `KAFKA_BROKERS`,
  `OUTBOX_RELAY_INTERVAL` and `LEGACY_IMPORT_CONSUMER_GROUP`.
- Docs site: `cd docs && npm ci && npm run build`.

Scaffolded from [warehouse-harness-template](https://github.com/IQVO/warehouse-harness-template).
Study project: not production software.

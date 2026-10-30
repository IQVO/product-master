# product-master

Product Master is the WMS-tier bounded context of the warehouse-systems fleet
that owns SKU-level product master data: the handling classification of a SKU
(hazmat, fragile, temperature-sensitive, oversized, high value, DOT hazard
class) and its physical profile (declared and measured unit dimensions and
weight). It does not own stock levels (`inventory-storage`), slotting
(`slotting-optimization`) or receipts (`inbound-receiving`) — those contexts
keep local copies of its published events. Hexagonal Go, Postgres, REST,
Kafka (CloudEvents 1.0). The repository holds the contracts, the domain
model, the REST service, a read-mostly MCP server, an A2A agent, the
analytics read side and the `productmaster_mfe` console remote. Agent
guides: `.claude/rules/`; harness: [`HARNESS.md`](HARNESS.md); scaffolded
from [warehouse-harness-template](https://github.com/IQVO/warehouse-harness-template).

## Disclaimer

This project is a domain-driven design study. It is not associated with,
endorsed by or affiliated with any company, and it uses no company's code,
data, logos or trademarks. All names, figures and examples are illustrative
or synthetic.

---

## Documentation

Published site: **<https://iqvo.github.io/product-master/>** (sources in
`docs/`; build locally with `cd docs && npm ci && npm run build`).

| Area | Pages |
|---|---|
| Overview | [Introduction](docs/docs/intro.md), [Quickstart](docs/docs/overview/quickstart.md), [Architecture and runtime](docs/docs/overview/runtime.md) |
| Operations | [Configuration](docs/docs/operations/configuration.md) (every env var of every binary), [Runbook](docs/docs/operations/runbook.md), [Observability](docs/docs/operations/observability.md), [Troubleshooting](docs/docs/operations/troubleshooting.md) |
| Development | [Testing](docs/docs/development/testing.md) (test pyramid, the testcontainers integration suites, `make` targets, CI jobs) |
| MCP & A2A | [MCP tools](docs/docs/mcp/tools.md), [A2A agent (ADR 0012)](docs/adr/0012-a2a-inbound-adapter.md) |
| Ecosystem | [Downstream consumers](docs/docs/ecosystem/downstream-consumers.md), [Upstream contracts](docs/docs/ecosystem/upstream-contracts.md) |
| DDD | [Use cases](docs/docs/ddd/use-cases.md), [Subdomain classification](docs/docs/ddd/subdomain-classification.md), [DDD artifacts](docs/docs/ddd/ddd-artifacts.md) |
| Decisions | [ADR index](docs/adr/index.md) — 12 decisions with status |

---

## Architecture

Hexagonal / ports & adapters; dependencies point inward only.

```text
apis/                    openapi.yaml (REST), asyncapi.yaml (events)
cmd/api/                 the REST service — composition root (config, adapters, relay)
cmd/mcp/                 MCP server (ADR 0005: six read tools + two pack-level write tools)
cmd/a2a/                 A2A agent (ADR 0012)
cmd/product-projector/   analytics projector — the only analytical-DB writer (ADR 0006)
cmd/product-reports/     analytics reader (:8092)
internal/domain/         product (the Product aggregate: classification, physical profile, pack levels), shared value types
internal/application/    usecases, ports, repository, outbox, idempotency
internal/adapters/
  inbound/http/          chi handlers, RFC 7807 errors, /metrics
  inbound/kafka/         the projector's analytics-topic consumer
  inbound/mcp/           MCP tools (governance-tested write verbs)
  inbound/a2a/           A2A skills
  outbound/postgres/     pgxpool repos, UnitOfWork, outbox + relay
  outbound/kafka/        CloudEvents publisher
  outbound/outbox/       the transactional outbox relay (attempt cap, ADR 0007)
  outbound/analyticsstore/  analytical-DB projection and report queries (read-only)
  outbound/telemetry/    OpenTelemetry wiring (ADR 0008)
  outbound/memory/, clock/  test doubles and system ports
internal/architecture/   hexagonal and fleet fitness tests
analytics/migrations/    analytical-DB SQL (embedded)
migrations/              golang-migrate SQL
features/                godog scenarios
web/                     the productmaster_mfe remote
charts/product-master/   Helm chart (values, templates, tests/)
docs/                    Docusaurus site (docs/docs) and the ADRs (docs/adr)
```

The layering is enforced by the executable fitness tests in
`internal/architecture` (`make arch-test`, CI job `arch-test`).

---

## Domain events

All messages are **CloudEvents 1.0** in structured content mode; the type
convention is `com.warehouse.wms.product-master.product.<EventName>` (the
`wms` subdomain segment, single `product` entity).

**Publishes** — 8 events on `warehouse.product-master.events`, through the
transactional outbox:

| Entity | Events |
|---|---|
| `product` (unit) | `ProductRegistered`, `ProductDescriptionChanged`, `ProductClassified`, `ProductDimensionsDeclared`, `ProductMeasured` |
| `product` (pack levels, ADR 0011) | `ProductPackLevelDeclared`, `ProductPackLevelMeasured`, `ProductPackLevelRetired` |

Every **unit** event (not the three pack-level events) also goes, in the same
outbox transaction and under the same CloudEvents id, to
`warehouse.product-master.analytics` (ADR 0006).

**Downstream consumers** — six contexts keep local copies from
`warehouse.product-master.events`, all built on their `develop` and wired in
Kafka mode in warehouse-infra: inventory-storage, order-management,
wes-work-planning, fulfillment-execution and slotting-optimization consume
`ProductClassified` (slotting-optimization also `ProductDimensionsDeclared`
and `ProductMeasured`), and inbound-receiving consumes `ProductRegistered`.
`ProductDescriptionChanged` has no consumer. **Nothing flows in**: the legacy
classification importer from inventory-storage was removed at stage E of
[ADR 0003](docs/adr/0003-migration-from-inventory-storage.md) (finished
2026-10-17). See
[downstream consumers](docs/docs/ecosystem/downstream-consumers.md).

---

## Running it

Postgres and Kafka are the only runtime (ADR 0010): there is no in-memory
mode, no log sink and no optional relay. A missing mandatory variable is a
boot error naming it, raised before the listener binds. The cluster broker
auto-creates topics on first write; the service never calls an admin API.

```bash
DATABASE_URL=... KAFKA_BROKERS=... go run ./cmd/api   # :8080; both are mandatory, the process exits at boot without them
DATABASE_URL=... go run ./cmd/mcp                     # :8090, MCP server (Streamable HTTP) over the same database
DATABASE_URL=... A2A_AGENT_URL=http://localhost:8093/ go run ./cmd/a2a   # :8093, A2A agent (JSON-RPC); both variables are mandatory
go run ./cmd/product-projector  # :8091 admin; needs ANALYTICS_DATABASE_URL + KAFKA_BROKERS + ANALYTICS_CONSUMER_GROUP (ADR 0006)
go run ./cmd/product-reports    # :8092, read-only reports; needs ANALYTICS_DATABASE_URL
```

`/readyz` is the drain gate AND a Postgres ping AND a Kafka metadata check
(reachability only, never topics), cached for a few seconds; `/healthz` is
liveness only.

### Configuration

`cmd/api`:

| Env var | Default | Meaning |
|---|---|---|
| `HTTP_ADDR` | `:8080` | REST listen address |
| `DATABASE_URL` | **required** | Postgres DSN; boot error when unset |
| `KAFKA_BROKERS` | **required** | Comma-separated brokers |
| `EVENT_PUBLISHER` | `kafka` | Only `kafka` is accepted; `log` is rejected at boot |
| `OUTBOX_RELAY_INTERVAL` | `1s` | Relay poll interval (Go duration or seconds) |
| `OUTBOX_MAX_ATTEMPTS` | `20` | Failures before the relay excludes a row (`0` = unlimited, ADR 0007) |
| `MIGRATIONS_DATABASE_URL` | *(unset = `DATABASE_URL`)* | Direct DSN for the boot migrations |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | OTLP/gRPC Collector endpoint |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | Wait after flipping `/readyz` before closing |
| `SERVICE_VERSION` | `dev` | `service.version` resource attribute |
| `ENVIRONMENT` | `local` | `deployment.environment.name` |
| `LOG_LEVEL` | `info` | `debug`/`info`/`warn`/`error` |
| `CORS_ALLOWED_ORIGINS` | *(unset)* | Comma-separated browser origins allowed to call the API (the `web/` remote) |

The other binaries:

| Binary | Env vars |
|---|---|
| `cmd/mcp` | `MCP_ADDR` (`:8090`), `DATABASE_URL` (**required**), `LOG_LEVEL`, `SERVICE_VERSION` |
| `cmd/a2a` | `A2A_ADDR` (`:8093`), `A2A_AGENT_URL` (**required**, absolute URL advertised on the Agent Card), `DATABASE_URL` (**required**), `LOG_LEVEL`, `SERVICE_VERSION` |
| `cmd/product-projector` | `ANALYTICS_DATABASE_URL` (**required**), `KAFKA_BROKERS` (**required**), `ANALYTICS_CONSUMER_GROUP` (deployed value `product-master-analytics`), `ADMIN_ADDR` (`:8091`), `LOG_LEVEL`, `SERVICE_VERSION` |
| `cmd/product-reports` | `ANALYTICS_DATABASE_URL` (**required**), `HTTP_ADDR` (`:8092`), `LOG_LEVEL`, `SERVICE_VERSION` |

Every variable is tabulated per binary in
[Configuration](docs/docs/operations/configuration.md).

---

## Observability

OpenTelemetry is wired in `internal/adapters/outbound/telemetry` (ADR 0008)
— an outbound adapter; the domain and application layers never import it.

- **Traces**: a `TracerProvider` exporting over OTLP/gRPC; W3C trace-context
  propagator installed, and trace context crosses the outbox onto the Kafka
  message (ADR 0009).
- **Metrics**: a `MeterProvider` over OTLP/gRPC plus a Prometheus scrape
  handler at **`GET /metrics`**: HTTP server metrics, Go runtime metrics,
  and the business counter **`product_master.products.registered`**.
- **Logs**: structured `log/slog` JSON on stdout; level from `LOG_LEVEL`.
- Export is non-blocking: with no Collector at
  `OTEL_EXPORTER_OTLP_ENDPOINT`, telemetry is dropped silently and the
  service runs normally.

Details and suggested alerts:
[Observability](docs/docs/operations/observability.md).

---

## API

REST on `HTTP_ADDR` (default `:8080`), specified in `apis/openapi.yaml`.
Errors are **RFC 7807** `application/problem+json`. 18 operations over 15
paths (`cmd/api` serves all but the two `/reports/*` routes, which belong to
`cmd/product-reports`):

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
| GET | `/healthz`, `/readyz` | liveness; readiness (drain gate + Postgres + Kafka) |
| GET | `/metrics` | Prometheus scrape |
| GET | `/reports/master-data-quality`, `/reports/freshness` | analytics — served by `cmd/product-reports`, not `cmd/api` |

Writes are PUT-shaped against idempotent resource paths; the domain's
idempotent semantics (register, declare, retire) make retries safe without an
`Idempotency-Key` header. Problem types are catalogued in
[Troubleshooting](docs/docs/operations/troubleshooting.md).

### Analytics data product (Master Data Quality)

ADR 0006. `cmd/product-projector` consumes
`warehouse.product-master.analytics` (fixed group
`ANALYTICS_CONSUMER_GROUP`; poison is dead-lettered byte-for-byte to
`warehouse.product-master.analytics.dlq`) into a separate analytical database
(`analytics/migrations`, embedded), and `cmd/product-reports` serves
`GET /reports/master-data-quality` (per day: registered, classified,
declared, measured, open declared-vs-measured discrepancies; current coverage
ratios) and `GET /reports/freshness` read-only. The projector is the only
writer; the reports binary reads through a read-only pool.

---

## Helm chart

`charts/product-master` renders the `api` Deployment and Service, plus an
`mcp` and an `a2a` component (`<release>-a2a`, port 8093), the
`analytics-projector` / `analytics-reports` components (`analytics.enabled`)
and an optional `frontend` component — **all off by default** except `api`.
The `Dockerfile` builds every `cmd/*` binary into one image (`api`, `mcp`,
`a2a`, `product-projector`, `product-reports`).

- `database.*` and `kafka.brokers` are required (the render fails naming the
  value, ADR 0010); chart values also cover `EVENT_PUBLISHER` (only
  `kafka`), `OUTBOX_RELAY_INTERVAL` and `OUTBOX_MAX_ATTEMPTS` (ADR 0007).
- The api Deployment mounts the chart ConfigMap via `envFrom`, asserted by
  `charts/product-master/tests/render_test.sh`; each Service selects exactly
  one Deployment (`charts/product-master/tests/test_service_selectors.py`).
  CI runs them in the `chart-tests` job; `helm-lint` lints the chart.

### Where it runs (kind cluster, warehouse-infra)

Deployed in the `warehouse-systems` namespace by warehouse-infra
(`terraform/apps.yaml`, `helm-values/product-master.yaml`), all from the one
image:

| Surface | How to reach it | Notes |
|---|---|---|
| REST API (`cmd/api`) | `http://localhost:8000/api/product-master/*` through Kong (strips the prefix) | runs on real Postgres and Kafka only (ADR 0010); `/readyz` checks both |
| Console remote (`web/`, `productmaster_mfe`) | `http://localhost/mfes/product-master/` (web gateway); the console shell mounts it at `/product-master/*` (warehouse-console #66) | calls the REST API above |
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

### Running the MCP server in Kubernetes

`mcp.enabled=true` renders the MCP Deployment + Service on port `8090`
(ADR 0005). The server exposes six read-only tools (`get_product`,
`list_products`, `get_product_classification`, `get_physical_profile`,
`resolve_gtin`, `get_pack_levels`) and, since
[ADR 0011](docs/adr/0011-packaging-hierarchy-with-gs1-gtin.md), two write
tools: `declare_pack_level` and `retire_pack_level`. The writes run the same
use cases as REST, so they enqueue their events in the transactional outbox;
the binary still never dials Kafka and never runs the relay (the relay in
`cmd/api` publishes them). A governance test fails the build on any other
write-verb tool name. `GET /healthz` backs the probes; no auth (fleet-wide).
Details: [ADR 0005](docs/adr/0005-mcp-server-adoption.md),
[ADR 0011](docs/adr/0011-packaging-hierarchy-with-gs1-gtin.md),
`.claude/rules/mcp.md`.

### Running the A2A agent in Kubernetes

`a2a.enabled=true` renders the A2A Deployment + Service on port `8093`
(ADR 0012). `cmd/a2a` is the third driving adapter over the same use cases
as REST and MCP: an Agent2Agent agent (JSON-RPC 2.0 over HTTP,
message-only) with two curated skills derived from the MCP surface.
`product_profile` (read) answers "what is this item and how do I handle it"
for one `sku` or one scanned `gtin`: classification, physical profile and
the packaging hierarchy. `declare_pack_level` (write, idempotent) declares
one packaging level with its GTIN, the same use case as REST and MCP, so the
product row and its `ProductPackLevelDeclared` event commit in one
transaction through the transactional outbox; the binary never runs the
relay and never dials Kafka. The agent does not widen the context:
`retire_pack_level`, registration, classification and measurements stay off
A2A. A2A carries delegation only, never data sync. Requests are
`{"skill":"product_profile","arguments":{"sku":"SKU-1"}}` as a data part (or
a text part holding the same JSON); unknown arguments are refused; domain
rejections come back in-band as RFC 7807-style problems with REST's status
classification. No auth (fleet-wide). The Agent Card is at
`/.well-known/agent-card.json`, the endpoint at `/` and `/a2a` (`:8093`,
`A2A_ADDR`); `A2A_AGENT_URL`, the absolute URL the card advertises, is
mandatory.

---

## Operator micro-frontend (`web/`)

`web/` is `productmaster_mfe`, the console's Module Federation remote for
this context (Vite base `/mfes/product-master/`), packaged by
`web/Dockerfile` (nginx) and enabled with `frontend.enabled`.

```bash
cd web && npm ci && npm run dev   # http://localhost:5191
```

Screens: product list, register, product detail, and **Packaging** at
`/product-master/products/{sku}/packaging` (pack-level hierarchy EACH →
INNER → CASE → PALLET with GTIN, eaches per pack, dimensions and
active/retired state; declare and retire a level; GTIN lookup via
`GET /gtins/{gtin}`). Quantities are always eaches, a case reads "Case of N
eaches". Retire is a `POST` (the remote's first non-`PUT` write); the
chart's Kong CORS plugin already allows `POST`. CI job `web` lints,
typechecks, tests and builds it.

---

## Local development / quality gate

```bash
make check-fast           # quick gate
make check                # fmt-check, vet, build, lint, test
make check-all            # check + coverage (>= 90%) + arch-test + bdd — run this before pushing
make integration          # testcontainers Postgres + Kafka (Docker only)
make mutation             # gremlins on internal/domain
make vuln                 # govulncheck ./...
```

lefthook runs `fmt-check`, `vet` and `lint` on pre-commit and `make check`
on pre-push (`lefthook install` once per clone). `make help` lists every
target.

## Tests

- **Unit, handler and BDD** (`make test`, `make bdd`): no database; godog
  features in `features/`.
- **Integration** (`make integration`, CI job `integration`): testcontainers
  Postgres + Kafka — 18 `*_integration_test.go` suites covering the
  repository contract, use-case wiring, the outbox relay, the CloudEvents
  publisher, the projector's consumer (poison dead-lettering, redelivery
  dedup), the analytical store, the REST router, MCP and A2A wiring, and the
  end-to-end analytics path (ADR 0006).
- **Architecture fitness**: `internal/architecture` (`make arch-test`, CI
  `arch-test`).
- **Mutation**: gremlins over `internal/domain` — `mutation-fast` on every
  push/PR, the exhaustive `mutation` run on schedule.
- **Web**: the remote has its own vitest suite (CI job `web`).

CI (`.github/workflows/ci.yml`) runs on every push and PR: `lint`,
`guide-lint`, `complexity`, `test` (with the coverage gate), `bdd`,
`integration`, `mutation-fast`, `api-lint`, `vuln` (govulncheck),
`docs-api-drift` (the docs REST reference is regenerated from
`apis/openapi.yaml` and must match what is committed), `helm-lint`,
`chart-tests`, `web`, `arch-test`, plus the scheduled `mutation` and
advisory `drift`; `trivy-scan` builds the image and blocks on CRITICAL/HIGH
CVEs with a fix; `docker-publish` pushes the image and `release` tags the
next version and publishes the chart.

## Architecture Decision Records

The ADR index lives at [docs/adr/index.md](docs/adr/index.md) — 12 decisions
(0001–0012): the
[bounded context](docs/adr/0001-product-master-bounded-context.md),
[declared-vs-measured physical profile](docs/adr/0002-physical-profile-declared-vs-measured.md),
[migration from inventory-storage](docs/adr/0003-migration-from-inventory-storage.md),
[event catalogue](docs/adr/0004-cloudevents-envelope-and-type-catalogue.md),
[MCP server](docs/adr/0005-mcp-server-adoption.md),
[analytics read side](docs/adr/0006-analytics-read-side.md),
[packaging hierarchy with GS1 GTINs](docs/adr/0011-packaging-hierarchy-with-gs1-gtin.md)
and the [A2A inbound adapter](docs/adr/0012-a2a-inbound-adapter.md), among
others.

## License

MIT — see [LICENSE](LICENSE).

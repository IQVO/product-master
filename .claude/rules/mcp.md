---
paths:
  - "internal/adapters/inbound/mcp/**"
  - "cmd/mcp/**"
  - "charts/product-master/templates/mcp-*.yaml"
---

# MCP server (inbound adapter, read-only)

One MCP server for this bounded context, an additive inbound adapter over the
SAME read use cases the REST adapter calls. Decision record:
`docs/adr/0005-mcp-server-adoption.md`; the short fleet rule is
`.claude/rules/fleet/no-auth-and-mcp.md`.

- Code: `internal/adapters/inbound/mcp/` (tools, error mapping) and the
  composition root `cmd/mcp/` (env, repository, router, graceful shutdown).
  Built on the official SDK `github.com/modelcontextprotocol/go-sdk` (v1.8.0).
- Transport: **Streamable HTTP only** (no stdio, no SSE). Listens on
  `MCP_ADDR` (default `:8090`), served at **`/` and `/mcp`**; `GET /healthz`
  -> `200 {"status":"ok"}`.
- **Read-only.** `Deps` holds only `GetProduct` and `ListProducts`; never add
  a write use case, a repository `Save`, the outbox or an encoder to it.
  Writes (register, classify, declare dimensions, record measurement) are
  REST-only because their events feed other contexts' local copies.
- **No auth of any kind** (fleet-wide revert 2026-09-11).
  `TestNoAuthMiddlewareReintroduced` fails CI if it is reintroduced.
- Architecture: the adapter depends ONLY on `internal/application` and
  `internal/domain`; nothing depends on it (`TestMCPAdapterDependencyRule`).
- Env (`cmd/mcp`): `MCP_ADDR`, `DATABASE_URL` (unset -> in-memory repository),
  `MIGRATIONS_DATABASE_URL` (direct DSN for the migration step only),
  `OTEL_EXPORTER_OTLP_ENDPOINT`, `LOG_LEVEL`, `SERVICE_VERSION`,
  `ENVIRONMENT`. It runs the idempotent embedded migrations on start and uses
  `internal/bootretry` for the Postgres dial. It does NOT start the outbox
  relay and does NOT dial Kafka.
- Chart: `mcp.enabled` (default false) renders
  `charts/product-master/templates/mcp-deployment.yaml` and
  `charts/product-master/templates/mcp-service.yaml` (component `mcp`,
  command `/app/mcp`, probes on `/healthz`). Keep the env there in sync with
  `cmd/mcp/main.go`'s header.

## Tools (4; budget is 4)

Arguments are snake_case. Results are the REST bodies with snake_case names.
Failures are tool errors (`isError: true`) whose text is `<slug>: <message>`
with the REST problem slugs; unexpected infrastructure errors are logged and
reported as a generic `internal-error`.

| Tool | Backed by | Arguments | Result | Errors |
|---|---|---|---|---|
| `get_product` | `GetProduct` | `sku` | `sku`, `description`, `version`, `classification` (omitted when unclassified), `physical_profile` | `invalid-sku`, `product-not-found` |
| `list_products` | `ListProducts` | optional `limit` (1..500, default 100), `cursor`, `handling_tag`, `classified` | `items[]` (never null), `next_cursor` (absent on the last page) | `malformed-request` |
| `get_product_classification` | `GetProduct` | `sku` | `sku`, `handling_tags` (stable order), `temperature_class`, `dot_hazard_class` (omitted when unset), `classification_source`, `version` | `product-not-found`, `product-classification-not-found` |
| `get_physical_profile` | `GetProduct` | `sku` | `sku`, `declared`, `measured` (+ `measured_at`, `device_id`), `effective`, `effective_source`, `discrepancy`, `version` | `product-not-found` |

## Tests

- `internal/adapters/inbound/mcp/governance_test.go`: `TestToolSurface` pins
  the exact set, the budget, read-only annotations, descriptions and
  documented snake_case arguments, and fails on any write-verb tool name;
  `TestWriteVerbSensorFailsOnWriteNames` proves that check can fail;
  `TestToolRegistryGolden` pins the advertised registry against
  `testdata/tool_registry.golden.json`.
- `tools_test.go` drives every tool through the SDK in-memory transport over
  in-memory repos (seeded with the write use cases, as REST would) and proves
  reads add no outbox row.
- `cmd/mcp/main_test.go`: `/healthz`, both mount paths over real Streamable
  HTTP, no auth required. `cmd/mcp/main_integration_test.go`
  (`-tags=integration`, testcontainers Postgres): migrations on a fresh DB,
  reads what the api's use cases wrote, outbox unchanged by reads.

## Adding a tool

Only a read tool, only with an ADR 0005 amendment: a typed input struct
(snake_case `json` tags + `jsonschema:"..."` on every field), a `Deps` method
calling an existing read use case, registration in `registerTools` with the
read-only annotations, errors through `mapError`, `wantTools` and `maxTools`
in `governance_test.go` updated, and the golden regenerated with
`go test ./internal/adapters/inbound/mcp -run TestToolRegistryGolden -update`.

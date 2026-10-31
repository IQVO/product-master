---
id: tools
title: MCP tools
sidebar_label: MCP tools
---

# MCP tools

`cmd/mcp` is a second, independent deployable that exposes product master data
to AI agents over the Model Context Protocol
([ADR 0005](/docs/adr/0005-mcp-server-adoption)). It calls the **same** use
cases as the REST adapter: the reads (`GetProduct`, `ListProducts`,
`ResolveGTIN`) and, since
[ADR 0011](/docs/adr/0011-packaging-hierarchy-with-gs1-gtin) amended ADR 0005,
exactly two writes (`DeclarePackLevel`, `RetirePackLevel`). Those two write the
pack level rows and the encoded events into the transactional outbox in one
Postgres transaction, like `cmd/api`; the binary still never dials Kafka and
never runs the relay (the relay in `cmd/api` publishes what it enqueued).

| Property | Value | Source |
| --- | --- | --- |
| Transport | Streamable HTTP only (no stdio, no SSE), served at `/mcp` and `/` | `cmd/mcp/router.go` |
| Port | `:8090` (`MCP_ADDR`) | `cmd/mcp/main.go` |
| Server name and version | `product-master-mcp` `1.0.0` | `internal/adapters/inbound/mcp/server.go` |
| SDK | `github.com/modelcontextprotocol/go-sdk` v1.8.0 | `go.mod` |
| Auth | none (fleet-wide); access control is the in-cluster ClusterIP Service | `cmd/mcp/main.go` |
| Health | `GET /healthz` returns `{"status":"ok"}` | `cmd/mcp/router.go` |
| Data | the OLTP database (`DATABASE_URL`, mandatory: the binary exits at boot without it, [ADR 0010](/docs/adr/0010-postgres-and-kafka-are-the-only-runtime)) | `cmd/mcp/main.go` |
| In the kind cluster | `http://product-master-mcp.warehouse-systems.svc.cluster.local:8090/mcp`, read by warehouse-ops-agent (`PRODUCT_MASTER_MCP_ENDPOINT`) | README, warehouse-infra |

At `initialize` the server sends instructions telling the client that only
`declare_pack_level` and `retire_pack_level` change data and that registering,
classifying and measuring are REST operations.

## Tools

The six read tools carry the annotations `readOnlyHint: true`,
`idempotentHint: true` and `openWorldHint: false` (read-only, idempotent,
closed world). The two write tools carry `readOnlyHint: false`,
`idempotentHint: true`, `openWorldHint: false` and an explicit
`destructiveHint` (`false` for `declare_pack_level`, `true` for
`retire_pack_level`, which end-dates a GTIN for good) and their descriptions
start with `WRITE.`. The schemas below are the ones a live `tools/list`
returns; they are pinned by
`internal/adapters/inbound/mcp/testdata/tool_registry.golden.json`.
`governance_test.go` fails the build on a tool whose name contains a write verb
unless it is one of the two allow-listed pack level tools, and on a tool count
above 8.

| Tool | Annotation | Input fields | Output | Errors |
| --- | --- | --- | --- | --- |
| `get_product` | read-only | `sku` (string, **required**): 1..64 characters, no whitespace, control characters or `/` | `sku`, `description`, `version`, `classification` (omitted when unclassified: `handling_tags`, `temperature_class`, `dot_hazard_class`, `classification_source`), `physical_profile` (`declared`, `measured`, `effective`, `effective_source`, `discrepancy`) | `invalid-sku`, `product-not-found` |
| `list_products` | read-only | `limit` (integer, optional; 1..500, omitted or 0 means 100), `cursor` (string, optional; the previous page's `next_cursor`), `handling_tag` (string, optional; `Hazmat`, `Fragile`, `TemperatureSensitive`, `Oversized` or `HighValue`), `classified` (boolean or null, optional; `true` only classified, `false` only unclassified) | `items` (products as in `get_product`, ascending SKU order), `next_cursor` (absent on the last page) | `malformed-request` (bad limit, cursor or tag) |
| `get_product_classification` | read-only | `sku` (string, **required**) | `sku`, `handling_tags` (stable order Hazmat, Fragile, TemperatureSensitive, Oversized, HighValue), `temperature_class` (`Ambient`, `Chilled`, `Frozen`; only with TemperatureSensitive), `dot_hazard_class` (1..9; only with Hazmat), `classification_source` (`native` or `legacy-import`), `version` | `invalid-sku`, `product-not-found`, `product-classification-not-found` |
| `get_physical_profile` | read-only | `sku` (string, **required**) | `sku`, `declared` and `effective` (`length_mm`, `width_mm`, `height_mm`, `weight_g`, `volume_mm3`), `measured` (the same plus `measured_at`, `device_id`), `effective_source` (`measured`, `declared` or `none`), `discrepancy` (declared and measured differ by more than 10%), `version` | `invalid-sku`, `product-not-found` |
| `resolve_gtin` | read-only | `gtin` (string, **required**): 8, 12, 13 or 14 digits with a valid mod-10 check digit, normalised to 14 | `gtin` (14 digits), `sku`, `level` (`EACH`, `INNER`, `CASE`, `PALLET`), `units_per_pack`, `child_gtin`, `active`, `effective_from`, `effective_to`; a retired GTIN resolves with `active: false` | `invalid-gtin`, `variable-measure-unsupported`, `gtin-not-found` |
| `get_pack_levels` | read-only | `sku` (string, **required**) | `sku`, `version`, `items` (every level ever declared, retired ones included, lowest level first: `level`, `gtin`, `child_gtin`, `qty_of_child`, `units_per_pack`, `dims`, `measured`, `is_orderable`, `is_despatch_unit`, `active`, `effective_from`, `effective_to`) | `invalid-sku`, `product-not-found` |
| `declare_pack_level` | **write**, idempotent | `sku`, `level`, `gtin` (**required**); `child_gtin`, `qty_of_child` (required above EACH), `units_per_pack`, `dims` (`length_mm`, `width_mm`, `height_mm`, `weight_g`), `is_orderable`, `is_despatch_unit` (optional) | `sku`, `version`, `pack_level` (as an item of `get_pack_levels`). A new `gtin` at an active level end-dates the old level (`superseded`); the content quantity of an active level never changes | `invalid-sku`, `invalid-pack-level`, `invalid-gtin`, `variable-measure-unsupported`, `invalid-pack-quantity`, `invalid-dimension`, `invalid-weight`, `pack-level-order`, `pack-quantity-immutable`, `pack-dimensions-below-contents`, `gtin-already-assigned`, `product-not-found` |
| `retire_pack_level` | **write**, idempotent | `sku`, `level` (**required**); `reason` (optional, 1..200 characters; omitted means `retired`) | `sku`, `version`, `pack_level` (now `active: false`); retiring a retired level changes nothing | `invalid-sku`, `invalid-pack-level`, `invalid-reason`, `pack-level-not-found`, `product-not-found` |

Field names are snake_case (the REST bodies use camelCase for the same data).

## Errors

A failing tool returns a normal tool result with `isError: true` and the text
`<slug>: <detail>`, never a transport or JSON-RPC error. The slugs are the same
as the last segment of the REST problem `type`
(`internal/adapters/inbound/mcp/errors.go`):

| Slug | When |
| --- | --- |
| `malformed-request` | invalid `limit`, `cursor` or `handling_tag` on `list_products` |
| `invalid-sku` | malformed SKU |
| `product-not-found` | unknown SKU |
| `product-classification-not-found` | `get_product_classification` on an unclassified product |
| `invalid-gtin`, `variable-measure-unsupported`, `invalid-pack-level`, `invalid-pack-quantity`, `invalid-reason`, `invalid-dimension`, `invalid-weight` | a GTIN, level, quantity, reason or dimension the pack level tools reject (ADR 0011) |
| `gtin-not-found`, `pack-level-not-found` | `resolve_gtin` on a GTIN no level holds; `retire_pack_level` on a level the SKU never had |
| `gtin-already-assigned`, `pack-quantity-immutable`, `pack-level-order`, `pack-dimensions-below-contents`, `concurrent-modification` | conflicts of `declare_pack_level` / `retire_pack_level` (same meaning as the REST `409` slugs) |
| `internal-error` | anything else (database failure); the real error is logged as `mcp tool failed with an unexpected error` and never returned |

Example for a SKU that does not exist (captured from a test over an empty repository double):

```json
{"content":[{"type":"text","text":"product-not-found: product not found"}],"isError":true}
```

## Calling it by hand

```bash
curl -s -D - -X POST localhost:8090/mcp \
  -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"curl","version":"0"}}}'
# take the Mcp-Session-Id response header
curl -s -X POST localhost:8090/mcp \
  -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  -H 'Mcp-Session-Id: <id>' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_products","arguments":{"classified":false,"limit":20}}}'
```

Responses come back as `text/event-stream` (`event: message`, `data: {...}`).

## Operating notes

- Run exactly one replica: sessions are held in process memory and the Service
  has no session affinity (the chart has no HPA for `mcp`).
- Give it the same `DATABASE_URL` as `cmd/api`. It runs the idempotent OLTP
  migrations on start (with `MIGRATIONS_DATABASE_URL` when set) so it can boot
  before the API on a fresh database.
- It exports OTLP traces and the request-duration histogram under
  `service.name=product-master-mcp`; it has no `/metrics` and no `/readyz`.

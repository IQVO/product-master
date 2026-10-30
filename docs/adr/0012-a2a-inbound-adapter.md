# ADR 0012: A2A inbound adapter (third driving adapter)

## Status

Accepted (2026-10-29); additive. Mirrors the fleet's reference rollout
(wes-work-planning ADR 0046, workforce-management ADR 0039). Follows
[ADR 0005](./0005-mcp-server-adoption.md) (the MCP adapter this one is
modelled on, as amended by [ADR 0011](./0011-packaging-hierarchy-with-gs1-gtin.md)),
[ADR 0004](./0004-cloudevents-envelope-and-type-catalogue.md) (CloudEvents on
every Kafka message) and
[ADR 0010](./0010-postgres-and-kafka-are-the-only-runtime.md) (Postgres and
Kafka are the only runtime). No contract change: REST, MCP and the CloudEvents
envelope are unchanged.

## Context

The fleet is adopting the Agent2Agent (A2A) protocol so that a bounded context
can **delegate** a goal to another context's agent. This context already
exposes its use cases over REST and MCP. The two things a delegating agent most
wants from the owner of SKU master data are: *what is this item and how do I
handle it?* (including: *what SKU does this scanned barcode belong to?*) and,
for packaging, *declare this level with its GTIN*.

Three transports now carry three different things and must not overlap:

| Transport | Carries |
| --- | --- |
| Kafka + CloudEvents | **facts** between contexts (ADR 0004). Downstream contexts keep LOCAL COPIES fed by `warehouse.product-master.events` (ADR 0001) |
| MCP | an agent's **tools** (reads and single actions) |
| A2A | **delegation** of a goal to this context's agent |

A2A is never a way for another service to look a product up on its hot path:
the "no outbound calls to sibling contexts" rule (ADR 0001) is about what
product-master calls; the mirror rule for consumers is "local copy, not live
lookup", and this ADR does not weaken it.

## Decision

Add `internal/adapters/inbound/a2a`, a third driving adapter over the **same**
use cases (`GetProduct`, `ResolveGTIN`, `DeclarePackLevel`) as the REST and MCP
adapters, built on the official `a2aproject/a2a-go/v2` SDK (pinned v2.7.0) and
served by a new `cmd/a2a` binary: JSON-RPC 2.0 over HTTP on port **8093**
(`A2A_ADDR`), the Agent Card at `/.well-known/agent-card.json`, the endpoint
mounted at `/` and `/a2a`, `GET /healthz`. The mandatory `A2A_AGENT_URL` (an
absolute http(s) URL advertised on the card) is refused at boot when missing or
malformed. The adapter depends only on `internal/application` and
`internal/domain`; nothing depends on it
(`TestA2AAdapterDependencyRule`).

### Two curated skills, derived from the MCP surface

| Skill | Kind | What it does | Derived from |
| --- | --- | --- | --- |
| `product_profile` | read | one product by exactly one of `sku` or `gtin`: description and version, handling classification (absent when unclassified), physical profile (declared, measured, effective, discrepancy) and the whole packaging hierarchy; a GTIN lookup also returns `identifiedBy`, the pack level that GTIN identifies. Writes nothing. | MCP `get_product` + `get_physical_profile` + `get_product_classification` + `get_pack_levels` + `resolve_gtin` |
| `declare_pack_level` | write | declares one packaging level of a registered SKU with its GTIN (`sku`, `level`, `gtin`; `childGtin` and `qtyOfChild` above EACH; optional `unitsPerPack`, `dims`, `isOrderable`, `isDespatchUnit`). Idempotent. Publishes `ProductPackLevelDeclared` (and `ProductPackLevelRetired` when it supersedes a level) through the transactional outbox. | MCP `declare_pack_level` |

Goal-shaped, never one skill per endpoint (the discipline of ADR 0005): the
five MCP reads collapse into one because a delegating agent asks "what is this
item" once, not field by field.

### The write gate: the A2A agent never widens the context

A skill may change state only because the MCP surface already offers that
write. ADR 0011 amended ADR 0005 for exactly two MCP write tools; the registry
golden and `TestToolSurface` pin them. `declare_pack_level` is additive and
idempotent, so it is offered. **Not offered over A2A**, by decision:

- `retire_pack_level` (destructive: it end-dates a GTIN for good). It stays
  MCP/REST; a test pins that no skill id names it and that asking for it is
  answered as `unknown-skill`.
- registering, classifying, declaring dimensions and recording measurements:
  REST-only (their events feed other contexts' local copies; ADR 0005).
- `list_products`: a paged catalogue scan is an operator/console read, not a
  delegated goal.

Adding any further skill, read or write, is a new ADR.

### Message-only, answered in-band

Both skills are transactional request/response work, so the agent is
message-only (no Task lifecycle). Domain rejections and malformed requests are
**answered** as RFC 7807-shaped `problem`s inside the message envelope, never
raised as JSON-RPC transport errors, with the **same status and slug
classification as the REST adapter** (`internal/adapters/inbound/http/errors.go`):
`400` (`invalid-sku`, `invalid-gtin`, `invalid-pack-level`,
`invalid-pack-quantity`, `invalid-dimension`, `invalid-weight`,
`variable-measure-unsupported`, ...), `404` (`product-not-found`,
`gtin-not-found`, `pack-level-not-found`), `409` (`gtin-already-assigned`,
`pack-quantity-immutable`, `pack-level-order`, `pack-dimensions-below-contents`,
`concurrent-modification`) and `500` (`internal-error`; the cause is logged,
never shown). The REST adapter has no `503` class, so neither does this one.
Problem `type` URIs share REST's base
(`https://errors.product-master.warehouse-systems.dev/<slug>`); the adapter adds
`malformed-skill-request`, `unknown-skill` and `invalid-argument` (all `400`).
Inbound adapters do not import each other, so the catalogue is copied and
`TestProblemCatalogue` pins every row.

### Strict input, camelCase names

- Unknown argument names are **rejected**, not ignored, and trailing data after
  the JSON document is rejected: a misspelled `qtyOfChld` must not silently
  drop a required quantity. Go's `encoding/json` matches names
  case-insensitively, so `qtyofchild` still reaches `qtyOfChild`.
- Argument names are camelCase (`childGtin`, `qtyOfChild`, `unitsPerPack`,
  `lengthMm`, ...), unlike MCP's snake_case. That is deliberate and checked:
  the shared domain error texts already spell their arguments in camelCase
  (`unitsPerPack must be 1..N`, `qtyOfChild must be 2..N`), so on this surface
  an error that names an argument names one the strict decoder accepts.
  `TestWireAnswersOnlyNameArgumentsTheSurfaceOffers` asserts every camelCase
  token in a problem answer is an accepted argument and that no answer carries
  a snake_case identifier. There is no deprecated alias on this surface.

### Facts still travel on Kafka

A2A never becomes a data-sync channel. `declare_pack_level` runs
`DeclarePackLevel`, so the product row, the pack level rows and the encoded
CloudEvents commit in one `ports.UnitOfWork` exactly as for REST and MCP;
`cmd/a2a` never runs the relay and never dials Kafka (no `KAFKA_BROKERS`, no
`EVENT_PUBLISHER`), the relay in `cmd/api` publishes what it enqueued. Proven on
real Postgres by `TestA2ADeclarePackLevel_WritesUnpublishedOutboxRows` (build tag
`integration`): the delegated write leaves UNPUBLISHED
`com.warehouse.wms.product-master.product.ProductPackLevelDeclared` rows on the
integration topic, and a rejected declaration stores nothing.

### Unauthenticated, like REST and MCP

The card declares no security schemes: this surface is unauthenticated by the
same fleet-wide decision (2026-09-11) as REST and MCP, and
`TestNoAuthMiddlewareReintroduced` scans this adapter's sources too. Schemes are
added to the card, not a redesign, if that decision is ever reversed.

### Honouring the context's rules

- No outbound calls to sibling contexts (AGENTS.md rule 5, ADR 0001): this
  change adds an inbound server only. There is no A2A client in this repo.
- Real Postgres only (ADR 0010): `cmd/a2a` refuses to boot without
  `DATABASE_URL`; there is no in-memory path (the unit tests use in-memory
  doubles through `depsFor`, as `cmd/mcp`'s do).
- Every write is an idempotent PUT and no resource-creating POST is added
  (rule 10): `declare_pack_level` is the same idempotent declaration as
  `PUT /products/{sku}/pack-levels/{level}`.

## Consequences

- A fifth binary (`/app/a2a`) in the same image (the Dockerfile builds every
  `cmd/*`) and an `a2a.*` chart block (`<release>-a2a`, default **off**),
  exposed through Kong at `/api/a2a/product-master` rewritten to `/`, so the
  card stays reachable under the prefix.
- Port 8093 is the fleet's A2A port (8080 api, 8090 mcp, 8091 projector admin,
  8092 reports).
- `declare_pack_level` is idempotent (an identical declaration changes nothing
  and enqueues nothing), so a delegating agent may retry an ambiguous failure,
  unlike a non-idempotent delegation elsewhere in the fleet. Using the A2A
  `messageId` as an idempotency key is therefore not needed.
- Delegating to this agent does not change who decides: whether a caller may
  *ask* for a declaration is that caller's governance.
- Like MCP, the agent offers no pagination and no listing; consumers that need
  the catalogue keep a local copy from the event topic.

## Not in this ADR

An outbound A2A client; Task lifecycle or streaming; authentication; skills
beyond the two above (a new ADR); retiring a pack level over A2A.

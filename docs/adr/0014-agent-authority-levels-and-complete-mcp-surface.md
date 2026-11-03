# ADR 0014: Agent authority levels and complete MCP surface

## Status

Accepted (2026-11-02). Applies the fleet decision "Agentic Standard v2"
(`docs/architecture/agentic-standard.md` and the fleet rule
`agents/fleet/agent-authority.md` in IQVO/warehouse-docs) to this context.
Extends [ADR 0005](./0005-mcp-server-adoption.md) (MCP) and
[ADR 0012](./0012-a2a-inbound-adapter.md) (A2A); additive only — every tool
and skill keeps its name, arguments and results, CloudEvents are untouched
and there is still no auth of any kind.

## Context

This context already lets an agent act: `declare_pack_level` and
`retire_pack_level` are write tools over MCP and `declare_pack_level` is a
write skill over A2A. Nothing declared how much an agent may do with a tool:
the MCP `ReadOnlyHint` / `DestructiveHint` annotations and the A2A `write`
tag say *whether* a call changes state, not *what level of authority* it
represents. A delegating agent (and its reviewer) could not read a tool's
reach off the registry.

Separately, the fleet's MCP servers used the Streamable HTTP handler with its
default (stateful) options, which advertises only the 2025-11-25 revision and
older, so a client speaking the 2026-07-28 per-request handshake
(`server/discover`) was refused with `-32022`. And the server offered tools
only — no resource and no prompt — so a host could not read this context's
state or its operating procedure through the protocol's discovery surface.

## Decision

### Authority levels, declared where the tool is registered

Authority levels are the strings `L1`..`L4`. In this context:

| Surface | Name | Level | Why |
|---|---|---|---|
| MCP | `get_product` | `L1` | reads facts |
| MCP | `list_products` | `L1` | reads facts |
| MCP | `get_product_classification` | `L1` | reads facts |
| MCP | `get_physical_profile` | `L1` | reads facts |
| MCP | `resolve_gtin` | `L1` | reads facts |
| MCP | `get_pack_levels` | `L1` | reads facts |
| MCP | `declare_pack_level` | `L3` | changes state |
| MCP | `retire_pack_level` | `L3` | changes state |
| A2A | `product_profile` | `L1` | composed read, writes nothing |
| A2A | `declare_pack_level` | `L3` | changes state; keeps its `write` tag |

- **MCP** declares it in the tool's `Meta` under `com.warehouse/authority`
  (`authority.go`: `authorityMetaKey`, `authorityL1`, `authorityL3` and
  `func authorityMeta(level string) mcp.Meta`); it is returned verbatim in
  `tools/list` `_meta`.
- **A2A** declares it as the skill tag `authority:L<n>`; a write skill keeps
  its `write` tag.
- **L4 is not granted here.** No ADR in this context lets an agent write
  without a human-gated step, so `L4` is forbidden. There is no L2 either:
  nothing in this context computes a recommendation.

Two sensors enforce it. `TestEveryAgentToolDeclaresAuthority` exists in both
adapters (`internal/adapters/inbound/mcp` and `internal/adapters/inbound/a2a`):
every registered tool or skill has exactly one valid level, every tool with
`ReadOnlyHint=false` (or tagged `write`) is `L3`, and every read tool is `L1`
or `L2`. The fleet's static sensor
`internal/architecture/mcp_authority_fitness_test.go` (copied verbatim from
warehouse-harness-template) additionally reads the adapter sources with go/ast
and fails the build on any tool without a declaration. The MCP golden
`testdata/tool_registry.golden.json` gained `_meta.com.warehouse/authority` on
every tool, so a level can only change through a reviewed golden edit. The
level is a label for callers and reviewers; **it is not access control** and
nothing in this context decides anything from it (the surface stays
unauthenticated, like REST, per the fleet decision).

### Stateless MCP

`mcp.NewStreamableHTTPHandler` now receives
`&mcp.StreamableHTTPOptions{Stateless: true}`. No `Mcp-Session-Id` is issued
and the server answers `server/discover` for the 2026-07-28 revision, while a
legacy `initialize` (for example `2025-06-18`) still answers 200, so existing
session clients (the fleet's SDK clients negotiate the older revision) keep
working. Nothing in this adapter relied on cross-call session state; a tool
that ever needs it takes an explicit, server-minted handle as an argument.
`stateless_test.go` pins all three wire facts with raw POSTs, and reverting
the options to `nil` makes the `server/discover` and `tools/list` probes fail
with `-32022` (proved).

### A resource and a prompt

The server now also registers exactly one resource and one prompt (the
protocol's discovery surface, not new behaviour):

- **Resource** `warehouse://product-master/skus/{sku}/pack-levels` — the
  packaging hierarchy of one SKU (every level it ever declared, retired ones
  included), the same bounded view `get_pack_levels` returns, built from the
  same `GetProduct` read use case. JSON text; never a dump of a table.
- **Prompt** `declare_pack_levels` — the operator SOP for declaring or
  retiring packaging levels: read the hierarchy first, declare bottom-up with
  real GTINs, respect quantity immutability, retire explicitly, escalate label
  mismatches. It takes one optional typed argument (`sku`) to scope the SOP,
  names tools but never calls them, and writes nothing.

Resources and prompts are L1 by nature (facts and procedure text); they carry
no tool Meta. Per ADR 0005's charter as amended by ADR 0011, the TOOL surface
stays exactly the eight tools above: registering, classifying and measuring
products remain REST operations.

## Consequences

- A reviewer can read an agent's reach off the registry, and a new tool cannot
  ship without declaring a level (the static sensor fails the build).
- The in-process SDK client negotiates the older revision against a
  default-options server, so serving 2026-07-28 does not mean the fleet's
  clients speak it yet.
- Facts still travel as CloudEvents on Kafka; MCP still carries tools and A2A
  still carries delegation. Nothing here adds a producer or a transport.
- The golden file changes whenever a level changes, so a silent re-levelling
  is a reviewed diff.

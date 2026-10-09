---
id: subdomain-classification
title: Subdomain classification
sidebar_label: Subdomain classification
---

# Subdomain classification

## Verdict: Supporting

`product-master` is a **Supporting** subdomain in the **`wms`** tier.

| Axis | Verdict | Source |
| --- | --- | --- |
| Subdomain | Supporting | [ADR 0001](/docs/adr/0001-product-master-bounded-context) "Classification"; the warehouse-docs fleet table |
| Tier (CloudEvents subdomain segment) | `wms`: every type is `com.warehouse.wms.product-master.<entity>.<EventName>` | ADR 0001; `Subdomain = "wms"` in `internal/adapters/kafka/cloudevents/cloudevents.go` |
| Business model | Compliance and risk reduction | [Bounded context canvas](/docs/ddd/bounded-context-canvas) |
| Evolution | Custom-built, in genesis (created 2026-10-06) | [Core domain chart](/docs/ddd/core-domain-chart) |

The tier and the classification are different axes: `wms` says this context
answers "what and where" questions in the warehouse-management tier (alongside
`facility-layout`, `inventory-storage`, `inbound-receiving` and
`slotting-optimization`); Supporting says how much competitive advantage it
gives.

## Why Supporting

ADR 0001 states it directly: product master data is "necessary for every
warehouse flow and specific to this warehouse's handling rules, but it is not
where the business differentiates."

- **Not Core.** Nobody chooses this warehouse for how it records SKU master
  data. Its value is being correct and being the single source: every
  downstream flow needs a SKU's handling tags (and later its size and weight),
  but the differentiating decisions (stow placement, release, dispatch,
  sealing) are made by the contexts that consume these facts.
- **Not Generic.** The taxonomy is this warehouse's own: the closed handling
  tag set (`Hazmat`, `Fragile`, `TemperatureSensitive`, `Oversized`,
  `HighValue`), the temperature-class and DOT-hazard rules inherited from
  inventory-storage's ADRs 0009 and 0010, and the declared-versus-measured
  physical profile with its 10% discrepancy rule (ADR 0002). A bought-in
  generic product catalogue would not carry these rules as they are.
- **Small model.** One aggregate (`Product`) with value objects
  `Classification`, `UnitDimensions`, `Measurement` and `PhysicalProfile`; no
  invariant spans two SKUs; the rules are local validations plus
  latest-measurement-wins and "a legacy import never overwrites a native
  classification" (`internal/domain/product`).

Where it could move: ADR 0001's out-of-scope list (pack hierarchy, units of
measure, lot/serial/expiry policy, lifecycle states, barcodes) is the part a
commodity product-information product could one day supply; the handling
taxonomy should stay custom.

## Neighbours

Classifications copied from warehouse-docs
`docs/strategic-design/subdomain-classification.md` (`origin/main`), not
restated from memory:

| Context | Classification | Tier | Relationship to product-master |
| --- | --- | --- | --- |
| `inventory-storage` | Core | `wms` | Upstream during the migration only (legacy `ProductClassified` on `warehouse.inventory.events`, ADR 0003); downstream consumer of `ProductClassified` for stow placement and DOT segregation |
| `order-management` | Generic/Supporting | `wes` | Downstream consumer of `ProductClassified` (intake enrichment) |
| `wes-work-planning` | Core | `wes` | Downstream consumer of `ProductClassified` (release capabilities, fragile flag) |
| `fulfillment-execution` | Core | `wes` | Downstream consumer of `ProductClassified` (seal-time segregation) |
| `warehouse-ops-agent` | Supporting | `wes` (publishes no events) | Reads the four MCP tools |
| `warehouse-planning` | Core | `wes` | Named by ADR 0002 as a future user of the physical profile; no consumer is built |

The pattern is the usual one for a Supporting context: it is upstream of three
Core contexts, which keep their own copy of the facts from the Published
Language (`warehouse.product-master.events`) instead of calling it at request
time. See [Downstream consumers](/docs/ecosystem/downstream-consumers) and the
[context map](/docs/ddd/context-map).

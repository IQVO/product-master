---
id: intro
title: Introduction
slug: /intro
sidebar_position: 1
---

# Product Master

`product-master` is a **Supporting** bounded context in the `warehouse-systems`
fleet (GitHub org `IQVO`). It answers one question:

> *What is this SKU, for handling purposes and physically?*

It is the single source of truth for SKU-level product master data: the
handling **classification** of a SKU (hazmat, fragile, temperature-sensitive,
oversized, high value, DOT hazard class) and its **physical profile** (declared
and measured unit dimensions and weight). It answers no "where" or "how many"
question: stock lives in `inventory-storage`.

## Where it sits

| Property | Value |
| --- | --- |
| Subdomain classification | Supporting ([ADR 0001](/docs/adr/0001-product-master-bounded-context)) |
| Tier | `wms` (CloudEvents type prefix `com.warehouse.wms.product-master.*`; the third `wms` context after `facility-layout` and `inventory-storage`) |
| Language / style | Go backend, hexagonal architecture (ports and adapters) |
| Inbound adapters | REST (`cmd/api`, `:8080`) and, during the migration only, the legacy classification importer (Kafka) |
| Integration | Kafka CloudEvents 1.0 (structured mode), transactional outbox for publishing |
| Auth | None on REST (fleet-wide revert of 2026-09-11) |

:::note Study project
Like the rest of the `warehouse-systems` fleet, this is a personal study
project exploring Domain-Driven Design, hexagonal architecture and AI-agent
harness engineering. It is not production software and carries no support
guarantee.
:::

## What this context owns

One aggregate, **Product**, identified by **SKU**:

- **Description**: optional operator text (0..200 characters).
- **Classification** (optional value object): a non-empty set of handling
  tags from a closed list, a `TemperatureClass` required if and only if the
  product is `TemperatureSensitive`, and an optional `DOTHazardClass` (1..9)
  allowed only with `Hazmat`. Each classification records its
  **ClassificationSource**: `native` (authored here) or `legacy-import`
  (imported from `inventory-storage` during the migration).
- **PhysicalProfile**: declared unit dimensions, the latest measurement, and
  the derived *effective* values and *discrepancy* flag
  ([ADR 0002](/docs/adr/0002-physical-profile-declared-vs-measured)).
- **Version**: starts at 1 and increases by one on every accepted change; it
  guards the repository write and rides on every published event.

A product must be **registered** (`PUT /products/{sku}`) before it can be
classified or dimensioned; attributes of an unknown SKU are a `404`, never an
implicit registration.

It does **not** own stock or placement (`inventory-storage`), segregation
rules (`inventory-storage` per bin, `fulfillment-execution` per package),
pack hierarchy, units of measure, lot/serial/expiry policy, lifecycle states,
barcodes, or any commercial attribute (ADR 0001, "Explicitly out of scope").

## Where to go next

- [Bounded context](/docs/overview/context): purpose, context map, the no-live-lookup rule.
- [Aggregates](/docs/overview/aggregates): the `Product` aggregate and its invariants.
- [Physical profile](/docs/overview/physical-profile): declared versus measured, effective values and discrepancy.
- [DDD artifacts](/docs/ddd/ddd-artifacts): the ddd-crew pack (core domain chart, canvases, context map, EventStorming, class, ER and sequence diagrams).
- [Downstream consumers](/docs/ecosystem/downstream-consumers) and the [legacy upstream contract](/docs/ecosystem/upstream-contracts).
- [API reference](/docs/api-reference): REST (generated from `apis/openapi.yaml`) and the event catalogue.
- [Architecture decision records](/docs/adr/0001-product-master-bounded-context).

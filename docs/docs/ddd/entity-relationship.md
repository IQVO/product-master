---
id: entity-relationship
title: Entity-relationship diagram
sidebar_label: Entity-relationship
sidebar_position: 19
---

# Entity-relationship diagram

The final OLTP schema after applying every migration in order. This service has
**one** Postgres database (`DATABASE_URL`), migrated at boot by `cmd/api` with
golang-migrate (`postgres.RunMigrations`, over `MIGRATIONS_DATABASE_URL` when
set) from `internal/adapters/outbound/postgres/migrations/`. There is a single
migration so far, `0001_products_outbox`. golang-migrate keeps its own
`schema_migrations` table. Without `DATABASE_URL` the service runs on the
in-memory adapters and persists nothing.

## OLTP database

```mermaid
erDiagram
  products {
    text sku PK "COLLATE C, byte order for the list cursor"
    text description "default empty"
    bigint version "CHECK at least 1"
    text_array handling_tags "NULL when unclassified, stable tag order"
    text temperature_class "nullable"
    smallint dot_hazard_class "nullable, CHECK 1 to 9"
    text classification_source "native or legacy-import, NULL iff handling_tags NULL"
    bigint declared_length_mm "nullable"
    bigint declared_width_mm "nullable"
    bigint declared_height_mm "nullable"
    bigint declared_weight_g "nullable"
    bigint measured_length_mm "nullable"
    bigint measured_width_mm "nullable"
    bigint measured_height_mm "nullable"
    bigint measured_weight_g "nullable"
    timestamptz measured_at "NULL iff measured_length_mm NULL"
    text measured_device_id "nullable"
    timestamptz created_at
    timestamptz updated_at
  }
  outbox_events {
    bigserial id PK
    text event_id UK "UNIQUE with topic"
    text topic UK
    text event_type "full CloudEvents type"
    text subject "the SKU"
    bytea key "the SKU"
    text dataschema
    bytea value "structured CloudEvent"
    jsonb headers "content-type"
    timestamptz created_at
    timestamptz published_at "null until relayed"
    integer attempts
    text last_error
  }
  processed_events {
    text consumer PK "legacy-classification-importer"
    text event_id PK "CloudEvents id"
    timestamptz processed_at
  }
  schema_migrations {
    bigint version PK
    boolean dirty
  }
```

Source: `internal/adapters/outbound/postgres/migrations/0001_products_outbox.up.sql`,
`internal/adapters/outbound/postgres/migrate.go`, `product_repository.go`,
`outbox_repository.go`, `processed_event_repository.go`.
Omits: the indexes (`idx_products_handling_tags`, a GIN index for the
`handlingTag` filter, and `idx_outbox_events_unpublished`, a partial index on
unpublished rows). There are **no foreign keys**: the three tables are
independent, so no relationship line is drawn. `outbox_events.subject` and
`key` carry a SKU by value, not by reference.

## Table is not aggregate

| Table | Backs | Written by | Notes |
| --- | --- | --- | --- |
| `products` | the whole `Product` aggregate, flattened: classification and both halves of the physical profile are columns, not child tables | `ProductRepo.Save` (insert when the loaded version is 0, else `UPDATE ... WHERE sku = $1 AND version = <loaded>`) | One row per SKU; no history. The effective values, `effectiveSource`, `discrepancy` and volume are derived in Go, never stored. |
| `outbox_events` | nothing in the domain: already-encoded Kafka messages | `OutboxRepo.Insert` in the use case's transaction; `OutboxRepo.Drain` marks rows published | Kept after publication (no cleanup job). |
| `processed_events` | nothing in the domain: idempotency claims of the legacy importer | `ProcessedEventRepo.Claim` (`INSERT ... ON CONFLICT DO NOTHING`) in the import's transaction | A rollback un-claims the id. |

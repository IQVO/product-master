---
id: index
title: Architecture decision records
sidebar_label: ADR index
sidebar_position: 0
slug: /
---

# Architecture decision records

Every ADR of `product-master`, in number order. The Status column is copied
from each ADR's own `## Status` section; the ADR bodies are immutable, so a
change of decision is recorded in a new ADR that supersedes the old one.

| Number | Title | Status |
| --- | --- | --- |
| [0001](./0001-product-master-bounded-context.md) | product-master as the owner of SKU-level product master data | Accepted (2026-10-06) |
| [0002](./0002-physical-profile-declared-vs-measured.md) | Physical profile, declared versus measured | Accepted (2026-10-06) |
| [0003](./0003-migration-from-inventory-storage.md) | Migrating classification ownership from inventory-storage | Accepted (2026-10-06); companion decision inventory-storage ADR 0034 |
| [0004](./0004-cloudevents-envelope-and-type-catalogue.md) | CloudEvents envelope and type catalogue | Accepted (2026-10-06) |
| [0005](./0005-mcp-server-adoption.md) | MCP server adoption (read-only, unauthenticated, additive) | Accepted (2026-10-07) |
| [0006](./0006-analytics-read-side.md) | Analytics read side: an analytics stream, a projector and a master data quality report over a separate analytical database | Accepted (2026-10-07); additive |

## Where each decision lives in the code

| ADR | Main code |
| --- | --- |
| 0001 | `internal/domain/product` (the `Product` aggregate), `apis/openapi.yaml`, `apis/asyncapi.yaml` |
| 0002 | `internal/domain/product/physical.go` (declared, measured, effective, 10% discrepancy) |
| 0003 | `internal/adapters/inbound/kafka/legacy_importer.go`, `internal/application/usecases/import_legacy_classification.go` |
| 0004 | `internal/adapters/kafka/cloudevents/cloudevents.go`, `internal/adapters/outbound/kafka/encoder.go` |
| 0005 | `cmd/mcp`, `internal/adapters/inbound/mcp` |
| 0006 | `cmd/product-projector`, `cmd/product-reports`, `internal/analytics/report`, `internal/adapters/outbound/analyticsstore`, `analytics/migrations` |

Back to the [documentation](/docs/intro).

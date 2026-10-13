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
| [0003](./0003-migration-from-inventory-storage.md) | Migrating classification ownership from inventory-storage | Accepted (2026-10-06); companion decisions: inventory-storage ADR 0034 (hand-over) and one ADR in each of order-management, wes-work-planning and fulfillment-execution (local copy instead of live lookup) |
| [0004](./0004-cloudevents-envelope-and-type-catalogue.md) | CloudEvents envelope and type catalogue | Accepted (2026-10-06); per-key ordering amended by 0007 |
| [0005](./0005-mcp-server-adoption.md) | MCP server adoption (read-only, unauthenticated, additive) | Accepted (2026-10-07) |
| [0006](./0006-analytics-read-side.md) | Analytics read side: an analytics stream, a projector and a master data quality report over a separate analytical database | Accepted (2026-10-07); additive |
| [0007](./0007-outbox-relay-attempt-cap.md) | Outbox relay attempt cap (`OUTBOX_MAX_ATTEMPTS`) | Accepted (2026-10-13) |
| [0008](./0008-projector-telemetry-and-environment.md) | The analytics projector sets up telemetry; every backend Deployment sets ENVIRONMENT | Accepted (2026-10-13); additive |
| [0009](./0009-trace-context-crosses-the-outbox.md) | The W3C trace context crosses the outbox to Kafka | Accepted (2026-10-13); additive |

## Where each decision lives in the code

| ADR | Main code |
| --- | --- |
| 0001 | `internal/domain/product` (the `Product` aggregate), `apis/openapi.yaml`, `apis/asyncapi.yaml` |
| 0002 | `internal/domain/product/physical.go` (declared, measured, effective, 10% discrepancy) |
| 0003 | `internal/adapters/inbound/kafka/legacy_importer.go`, `internal/application/usecases/import_legacy_classification.go` |
| 0004 | `internal/adapters/kafka/cloudevents/cloudevents.go`, `internal/adapters/outbound/kafka/encoder.go` |
| 0005 | `cmd/mcp`, `internal/adapters/inbound/mcp` |
| 0006 | `cmd/product-projector`, `cmd/product-reports`, `internal/analytics/report`, `internal/adapters/outbound/analyticsstore`, `analytics/migrations` |
| 0007 | `internal/adapters/outbound/outbox/relay.go` (`WithMaxAttempts`), `internal/adapters/outbound/postgres/outbox_repository.go` (`claim`), `cmd/api/main.go` (`outboxMaxAttempts`) |
| 0008 | `cmd/product-projector/main.go` (`setupTelemetry`), `charts/product-master/templates/{projector,reports}-deployment.yaml` |
| 0009 | `internal/adapters/outbound/postgres/outbox_repository.go` (`Insert`, `traceHeaders`), `internal/adapters/outbound/kafka/relay_sink.go` |

Back to the [documentation](/docs/intro).

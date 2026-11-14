---
id: index
title: Architecture decision records
sidebar_label: ADR index
sidebar_position: 0
slug: /
---

# Architecture decision records

Every ADR of `product-master`, in number order. The Status column is copied
from each ADR's own `## Status` section (one exception, ADR 0011, explained under
[Status notes](#status-notes)); the ADR bodies are immutable, so a
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
| [0010](./0010-postgres-and-kafka-are-the-only-runtime.md) | Postgres and Kafka are the only runtime (no in-memory mode, no log sink, real `/readyz`) | Accepted (2026-10-19) |
| [0011](./0011-packaging-hierarchy-with-gs1-gtin.md) | Packaging hierarchy with GS1 GTIN; eaches are the ledger unit | Accepted - implemented (the ADR body still reads "Proposed (2026-10-23)"; see [Status notes](#status-notes)); amends 0005 (two MCP write tools) |
| [0012](./0012-a2a-inbound-adapter.md) | A2A inbound adapter (third driving adapter): `product_profile` (read) and `declare_pack_level` (write), delegation only | Accepted (2026-10-29); additive |
| [0013](./0013-adopt-the-fleet-rest-api-guideline.md) | Adopt the fleet REST API guideline: noun route for retirement, `Idempotency-Key` on the one POST (409 reuse, `Idempotency-Replayed`), keyset-paged collections | Accepted (2026-11-01); breaking |
| [0014](./0014-agent-authority-levels-and-complete-mcp-surface.md) | Agent authority levels and complete MCP surface: every tool/skill declares L1 or L3, the handler is stateless (2026-07-28), one resource + one prompt | Accepted (2026-11-02); additive |

## Where each decision lives in the code

| ADR | Main code |
| --- | --- |
| 0001 | `internal/domain/product` (the `Product` aggregate), `apis/openapi.yaml`, `apis/asyncapi.yaml` |
| 0002 | `internal/domain/product/physical.go` (declared, measured, effective, 10% discrepancy) |
| 0003 | importer removed at stage E (2026-10-17); `internal/adapters/inbound/kafka/kafka.go` |
| 0004 | `internal/adapters/kafka/cloudevents/cloudevents.go`, `internal/adapters/outbound/kafka/encoder.go` |
| 0005 | `cmd/mcp`, `internal/adapters/inbound/mcp` |
| 0006 | `cmd/product-projector`, `cmd/product-reports`, `internal/analytics/report`, `internal/adapters/outbound/analyticsstore`, `analytics/migrations` |
| 0007 | `internal/adapters/outbound/outbox/relay.go` (`WithMaxAttempts`), `internal/adapters/outbound/postgres/outbox_repository.go` (`claim`), `cmd/api/config.go` (`outboxMaxAttempts`) |
| 0008 | `cmd/product-projector/main.go` (`setupTelemetry`), `charts/product-master/templates/{projector,reports}-deployment.yaml` |
| 0009 | `internal/adapters/outbound/postgres/outbox_repository.go` (`Insert`, `traceHeaders`), `internal/adapters/outbound/kafka/relay_sink.go` |
| 0010 | `cmd/api/{config,adapters,relay,main}.go`, `cmd/mcp/main.go`, `cmd/product-projector/main.go`, `internal/adapters/inbound/http/readiness.go`, `internal/adapters/outbound/kafka/health.go`, `internal/architecture/runtime_no_memory_fitness_test.go`, `charts/product-master/templates/_helpers.tpl` |
| 0011 | `internal/domain/product/packaging.go` (`PackagingHierarchy`), `internal/adapters/outbound/postgres/migrations/0002_product_pack_levels.up.sql`, `apis/openapi.yaml`, `apis/asyncapi.yaml`, `web/src/screens/PackagingScreen.tsx` |
| 0012 | `cmd/a2a`, `internal/adapters/inbound/a2a`, `charts/product-master/templates/a2a-*.yaml` |
| 0013 | `internal/adapters/inbound/http/{idempotency,paging,server}.go`, `internal/adapters/inbound/http/page`, `internal/adapters/outbound/postgres/idempotency_store.go`, `internal/adapters/outbound/postgres/migrations/0003_idempotency_keys.up.sql`, `.spectral.yaml` |
| 0014 | `internal/adapters/inbound/mcp/{authority.go,resources.go,prompts.go,server.go,tools.go}`, `internal/adapters/inbound/a2a/server.go`, `internal/adapters/inbound/mcp/testdata/tool_registry.golden.json`, `internal/architecture/mcp_authority_fitness_test.go` |

## Status notes

The Status column above is the authority for ADR 0011, because its body is
immutable and was never updated after it was built.

- **0011** (body: "Proposed (2026-10-23). Nothing in this ADR is built"):
  Accepted - implemented, checked against `origin/develop` on 2026-11-13.
  - Design merged in PR #60 (2026-10-24); implementation merged in PR #61
    "feat(packaging): pack levels with GS1 GTINs (ADR 0011)" (2026-10-24, commit `70d8eac`).
  - Schema: `internal/adapters/outbound/postgres/migrations/0002_product_pack_levels.up.sql`.
  - REST (`apis/openapi.yaml`): `GET /products/{sku}/pack-levels` (`listPackLevels`),
    `PUT /products/{sku}/pack-levels/{level}` (`declarePackLevel`),
    `POST /products/{sku}/pack-levels/{level}/retirements` (`retirePackLevel`),
    `PUT /products/{sku}/pack-levels/{level}/measured` (`recordPackLevelMeasurement`),
    `GET /gtins/{gtin}` (`resolveGtin`).
  - MCP write tools `declare_pack_level` and `retire_pack_level`
    (`internal/adapters/inbound/mcp/tools.go`), which amend ADR 0005 as the ADR said.
  - A2A skill `declare_pack_level` (`internal/adapters/inbound/a2a/skills.go`, see ADR 0012).
  - Web Packaging screen: `web/src/screens/PackagingScreen.tsx`.

Back to the [documentation](/docs/intro).

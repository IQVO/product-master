# CLAUDE.md — product-master

`product-master` is a Supporting bounded context (`wms` tier) of the
`warehouse-systems` fleet (GitHub org `IQVO`, Go module
`github.com/claudioed/product-master`). It is the single source of truth for
SKU-level product master data: what a SKU *is* for handling (its
classification: hazmat, fragile, temperature, oversized, high value, DOT
hazard class) and physically (declared and measured unit dimensions and
weight). It owns the `Product` aggregate. It answers no "where" or "how many"
question: stock is inventory-storage's. It ships four binaries (`cmd/api` REST
+ outbox relay, `cmd/mcp` read-only MCP, `cmd/product-projector` and
`cmd/product-reports` for the analytics read side) and the `web/`
micro-frontend remote.

## Hard rules (each one cost an incident or is fitness-tested)

1. **Hexagonal dependency rule.** `internal/domain` depends on nothing internal
   but domain; `internal/application` only on domain and application; inbound
   and outbound adapters never depend on each other; only `cmd/` wires layers.
   No JSON tags, SQL or HTTP types in the domain. `make arch-test` enforces it.
2. **CloudEvents 1.0 is MANDATORY** on every Kafka message produced or
   consumed (structured mode, no flat envelope, no dual-write, no envelope
   toggle). Build/decode ONLY through `internal/adapters/kafka/cloudevents/`;
   `type` is `com.warehouse.wms.product-master.product.<EventName>`; a breaking
   payload change is a new `.v2` type, never a mutation. Catalogue:
   `docs/adr/0004-cloudevents-envelope-and-type-catalogue.md`; payloads:
   `apis/asyncapi.yaml`; rules: `.claude/rules/integration-events.md`.
3. **Events leave through the transactional outbox only**: the product save and
   the encoded CloudEvents commit in one `ports.UnitOfWork`; the relay in
   `cmd/api` publishes. Never write to Kafka from a handler or use case.
4. **`version` is a published contract.** It starts at 1, increases by one per
   accepted change, guards the repository write (409 `concurrent-modification`)
   and rides on every event so downstream local copies can drop stale messages.
   A change that alters nothing bumps no version and raises no event.
5. **No outbound calls to sibling contexts, ever.** This service has no HTTP
   client to another context. Downstream contexts keep LOCAL COPIES from
   `warehouse.product-master.events`; the REST GETs are for operators, the
   console and agents, never a service-to-service lookup (ADR 0001).
6. **Classification invariants are inherited verbatim** from inventory-storage
   ADR 0009/0010 (closed tag set; TemperatureClass iff TemperatureSensitive;
   DOT hazard class 1-9 only with Hazmat). Field names on the wire stay
   identical to inventory-storage's `ProductClassified` v1. Segregation rules
   (which DOT classes may share a bin or package) are NOT owned here.
7. **Physical profile uses whole millimetres and grams** (ADR 0002). Effective
   = measured if present else declared; latest measurement wins by
   `measuredAt`; >10 % volume or weight difference sets `discrepancy` (never a
   rejection).
8. **The legacy importer is a migration artefact** (ADR 0003): it reads
   inventory-storage's `ProductClassified` only while
   `LEGACY_IMPORT_CONSUMER_GROUP` is set and never overwrites a classification
   whose source is `native`. Do not build features on it; it is removed at
   stage E.
9. **No auth layer on REST or MCP** (fleet-wide revert 2026-09-11): never add
   bearer/JWT/API-key middleware (`TestNoAuthMiddlewareReintroduced`). MCP is
   additive (`cmd/mcp`, read tools only).
10. **Every write is an idempotent PUT** (no resource-creating POST, so no
    Idempotency-Key middleware). Do not add a POST without an ADR.
11. **An ADR comes first** for a new cross-context contract, a new attribute
    family (pack hierarchy, lot/expiry, lifecycle) or any auth. ADRs live in
    `docs/adr/`.

## Commands

```bash
make check-fast   # fmt-check + vet + arch-test + tests of changed packages: run before saying "done"
make check        # fmt-check vet build lint test
make check-all    # check + coverage (90% gate on domain+application) + arch-test + bdd
make integration  # real Postgres/Kafka via testcontainers (needs Docker)
make mutation     # gremlins on internal/domain/product (CI job mutation-fast, blocking)
make guide-lint   # these guides: skills load, references resolve, context budget
```

Run locally: `go run ./cmd/api` (REST `:8080`; in-memory adapters without
`DATABASE_URL`, event publishing in `log` mode without a broker);
`go run ./cmd/mcp` (`:8090`, read-only MCP); `cmd/product-projector` (`:8091`)
and `cmd/product-reports` (`:8092`) need `ANALYTICS_DATABASE_URL` (ADR 0006).

## Where things are written down (read before editing the matching area)

- `.claude/rules/domain-model.md`: ubiquitous language, the aggregate, events,
  use cases. `.claude/rules/rest-api.md`: endpoints and error slugs.
  `.claude/rules/integration-events.md`: published/consumed events, outbox,
  local-copy contract for consumers. `.claude/rules/mcp.md`: the MCP adapter.
  `.claude/rules/frontend.md`: the `web/` remote.
- `.claude/rules/fleet/*.md`: fleet-wide rules copied from the harness
  template; never edit them here.
- `docs/adr/`: decisions. `apis/openapi.yaml`, `apis/asyncapi.yaml`: contracts.
  `HARNESS.md`: what each sensor runs and why.

Scaffolded from `warehouse-harness-template`; harness-template v3 is in effect
(hooks, guide-lint).

<!-- harness:scoped-rules:start (generated by tools/migrate_v3.py in warehouse-harness-template; do not hand-edit) -->
## Scoped rules and harness

Claude Code loads each rule below automatically when you touch the matching paths. OpenCode and Codex do NOT: read the rule BEFORE editing matching files.

| When touching | Read |
|---|---|
| `internal/domain/**`, `internal/application/**`, `features/**` ... | `.claude/rules/domain-model.md` |
| `internal/adapters/**/kafka/**`, `internal/adapters/outbound/events/**`, `apis/asyncapi*` | `.claude/rules/integration-events.md` |
| `internal/adapters/inbound/http/**`, `apis/openapi*.yaml`, `apis/openapi/**` | `.claude/rules/rest-api.md` |
| `internal/adapters/inbound/mcp/**`, `cmd/mcp/**` | `.claude/rules/mcp.md` |
| `web/**` | `.claude/rules/frontend.md` |

Hooks (`scripts/harness/hook.py`, wired for Claude Code, Codex and OpenCode) block pushes to develop/main, `--no-verify`, bare `rm -rf`, and edits to generated files, and feed gofmt/vet findings back after each edit. Before saying "done" run `make check-fast`; the full gate is `make check-all`. `HARNESS_OFF=1` disables the hooks when debugging the harness itself.
<!-- harness:scoped-rules:end -->

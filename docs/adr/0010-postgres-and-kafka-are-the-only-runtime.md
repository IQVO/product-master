# ADR 0010: Postgres and Kafka are the only runtime

## Status

Accepted (2026-10-19). Supersedes the in-memory fallback, the log sink and the
optional relay that [ADR 0005](./0005-mcp-server-adoption.md) and the first
composition roots of `cmd/api` and `cmd/mcp` allowed. Fleet decision
"everything must use real database and broker"; product-master is the second
context migrated, after pick-face-replenishment (its ADR 0006). No migration,
no contract change: REST, MCP and the CloudEvents envelope
([ADR 0004](./0004-cloudevents-envelope-and-type-catalogue.md)) are unchanged;
only the readiness of `/readyz` grows.

## Context

Until now the runtime binaries could start in states that cannot serve the
cluster, and did so silently:

- `cmd/api` without `DATABASE_URL` logged `DATABASE_URL not configured; using
  in-memory adapters` and served REST on a repository, an outbox and a
  processed-event store that vanish on restart.
- `cmd/api` without `EVENT_PUBLISHER=kafka` relayed the outbox to a log sink:
  rows were marked published and nothing reached
  `warehouse.product-master.events` or the analytics topic. `KAFKA_BROKERS`
  was optional.
- `cmd/mcp` without `DATABASE_URL` answered `product-not-found` for every SKU
  from its own empty in-memory repository.
- The projector defaulted its consumer group.
- `/readyz` was only a drain gate: it said "ready" while Postgres or Kafka was
  unreachable, so a pod without its dependencies took traffic.

The chart and the troubleshooting guide documented these fallbacks as normal
states ("data disappears on every restart", "events logged, not sent"). A
fallback that works makes a mis-set environment look like a healthy deployment.

## Decision

1. **One runtime.** `cmd/api` and `cmd/mcp` run on real Postgres; `cmd/api`
   and `cmd/product-projector` run on real Kafka. There is no in-memory mode,
   no log sink, no optional relay and no `*_MODE` switch.
2. **Missing configuration is a boot error that names the variable**, raised
   before any listener binds, and never a default:
   `DATABASE_URL is required: this context runs on real Postgres`,
   `KAFKA_BROKERS is required: this context runs on real Kafka`,
   `ANALYTICS_CONSUMER_GROUP is required`. `EVENT_PUBLISHER` stays only because
   the chart and warehouse-infra set it: empty and `kafka` are accepted, `log`
   and anything else stop the boot.
3. **The composition root builds real adapters only.** `cmd/api` is split into
   `config.go` (environment to a validated config), `adapters.go` (Postgres
   repositories, unit of work, outbox), `relay.go` (the outbox relay on the
   Kafka sink, which is refused without a store or brokers) and `main.go`.
   Hexagonal architecture is kept: the `memory` adapters stay as test doubles
   for unit and BDD tests and are never imported by a binary.
4. **`/readyz` tells the truth.** It is `200` only while the drain gate is open
   AND Postgres answers a ping AND a Kafka broker answers. Otherwise `503`
   (`{"status":"not_ready"}` while draining; a `problem+json` of type
   `dependency-unavailable` naming `postgres` or `kafka` when a dependency
   fails). The verdict is cached 3 s and one round is bounded to 2 s, so probe
   traffic cannot hammer the database or the broker; a cancelled caller does
   not poison the cached verdict. `/healthz` stays a pure liveness check.
5. **Readiness checks the broker, not the topics.** The broker auto-creates
   topics on the first write (fleet convention, `warehouse-infra`
   `terraform/kafka.tf`) and this service never creates topics through an
   admin API. A not-ready pod receives no traffic and so could never make the
   first write: requiring the topics would deadlock a cold cluster. The check
   dials a fresh connection per call and asks for the broker list; kafka-go's
   `Client.Metadata` is served from the transport's cached layout after the
   first answer and would not notice a broker going away.
6. **A sensor keeps it true.** `TestRuntimeBinaryHasNoInMemoryAdapters`
   (`internal/architecture`) fails when any non-test file under `cmd/**`
   imports `internal/adapters/outbound/memory`, and requires `cmd/` to hold
   real binaries. `TestNoGoFilesAtRepoRoot` keeps the repository root free of
   Go files, so the godog runner and the analytics end-to-end test live in
   `features/`.
7. **The deployment mirrors it.** The chart fails the render, naming the value,
   without `database.url`/`database.existingSecret` or `kafka.brokers` (no
   default), with `config.eventPublisher` other than `kafka`, with
   `kafka.enabled=false`, with a `*_MODE` entry in `extraEnv`, with a grace
   period under 25 s, and with analytics enabled but no analytical DSN source
   or consumer group. `KAFKA_BROKERS` is always rendered. `kafka.enabled` is
   still accepted (warehouse-infra sets it to `true`) but is no longer a
   switch.

## Consequences

- A mis-set environment fails at boot with a message that names the missing
  variable; it no longer degrades into a pod that is up, empty and silent.
- Local `go run ./cmd/api` needs a Postgres and a Kafka broker (the kind
  cluster, or any containers you start). Fast feedback stays in unit and BDD
  tests over the memory doubles.
- A broker outage now takes the api pods out of the Service (readiness
  `503`), not out of the node: liveness is unaffected, so nothing restarts.
  Writes that were accepted stay in the transactional outbox and are relayed
  when the broker returns ([ADR 0007](./0007-outbox-relay-attempt-cap.md)).
  If every api pod is not-ready at once REST write traffic stops until a
  dependency recovers; the readiness probe tolerates three failures
  (`failureThreshold: 3`, `timeoutSeconds: 3`) so a one-off blip does not flap.
- The in-memory outbox no longer injects the trace context in any binary path
  ([ADR 0009](./0009-trace-context-crosses-the-outbox.md) point 5 is now
  unreachable outside tests).
- Tests: `TestRunOnRealPostgresAndKafka`
  (`cmd/api/e2e_integration_test.go`, testcontainers) boots the real `run()`,
  drives REST, asserts the rows in Postgres, the CloudEvents on both topics,
  replay idempotency, an in-process restart, and `/readyz` following the
  dependencies (green with no topic, `503` naming the dependency when it goes
  away). `TestBrokerCheck_RealKafka_NeedsNoTopicAndCreatesNone` proves the
  broker check needs and creates no topic. Unit tests cover every boot error
  (`bootconfig_test.go`, `main_test.go`, the mcp and projector mains).

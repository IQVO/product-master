---
name: how-to-add-an-integration-event
description: Publish or consume a cross-service Kafka event: CloudEvents 1.0 type naming, AsyncAPI, transactional outbox, consumer-group rules. Use when touching internal/adapters kafka or outbox code, a publisher/consumer, or apis/asyncapi*.yaml.
---

# How to add an integration event (publish and consume)

Use when asked to publish a new cross-context integration event, or
consume one from a sibling bounded context. This fleet's Kafka is ONE
broker platform-wide — every design decision below exists because that
shared-broker reality has already caused a real incident once.

## Publishing a new integration event

### 1. Is it actually cross-service?

Not every domain event belongs on the wire. In product-master every
`product.Event` the aggregate raises IS published: the use case encodes it
with `internal/adapters/outbound/kafka/encoder.go` and inserts it into the
transactional outbox (`internal/adapters/outbound/postgres/outbox_repository.go`)
in the same unit of work as the aggregate save; `internal/adapters/outbound/outbox/relay.go`
then ships it through `internal/adapters/outbound/kafka/relay_sink.go`.
Before adding a new event, confirm a sibling context genuinely needs to
react to it — check `docs/adr/0001-product-master-bounded-context.md` and
`docs/adr/0004-cloudevents-envelope-and-type-catalogue.md` for who is
downstream.

### 2. Envelope: CloudEvents 1.0, structured mode — MANDATORY

Every message is a CloudEvents 1.0 JSON document in structured content
mode (Kafka value = `application/cloudevents+json`), with the Kafka header
`content-type: application/cloudevents+json; charset=UTF-8`. There is no
other envelope in this fleet — no flat `event_id`/`event_type`/`occurred_at`
shape, no dual-write, no `EVENT_ENVELOPE_MODE` toggle (see
`.claude/rules/integration-events.md`; a fitness test enforces it).

```json
{
  "specversion": "1.0",
  "id": "<uuid v4, minted once, persisted with the outbox row>",
  "source": "/warehouse/<repo>",
  "type": "com.warehouse.<subdomain>.<bounded-context>.<entity>.<EventName>",
  "subject": "<aggregate id>",
  "time": "<domain occurred-at, RFC3339 UTC>",
  "datacontenttype": "application/json",
  "dataschema": "urn:warehouse:<repo>:events:<EventName>:v1",
  "data": { "the": "actual payload, business types only" }
}
```

`type` follows the platform-wide reverse-DNS convention
`com.warehouse.<subdomain>.<bounded-context>.<entity>.<EventName>`, all
lowercase except the final PascalCase event name — e.g.
`com.warehouse.wms.product-master.product.ProductClassified`. Take
the subdomain/context segment from the subdomain table on warehouse-docs'
Event Standard page (in the warehouse-docs repo, not this one); don't
guess it. The same `type` is used on the analytics topic; only
`dataschema` changes (`…:analytics:<EventName>:v1`).

### 3. Implementation

Add the event struct to `internal/domain/product/events.go` (it should already
exist as a domain event the aggregate raises — publishing wires an
EXISTING domain event onto Kafka, it doesn't invent a new payload shape at
the adapter layer). In `internal/adapters/outbound/kafka/encoder.go`:

- Add a case to `payloadFor` returning a snake_case payload struct that
  matches `apis/asyncapi.yaml` exactly (optional fields omitted).
- Encode ONLY through `internal/adapters/kafka/cloudevents` — `encodeOne`
  already does it:
  ```go
  value, err := cloudevents.New(cloudevents.Spec{
      ID:        id,                   // minted once by Encoder.NewID; the outbox row stores it
      Entity:    Entity,               // "product"
      EventName: ev.EventName(),
      Subject:   sku,
      Time:      ev.OccurredAt(),
      Stream:    cloudevents.StreamEvents,
      Version:   schemaVersion,
      Data:      data,                 // payloadFor(ev)
  })
  ```
- The key is the SKU, so all events of one product stay ordered on one
  partition; the relay writer keeps the `kafkago.Hash{}` balancer.
- Use `Topic` (`warehouse.product-master.events`) — this service's own
  topic constant, never a sibling's.

### 4. Contract + docs

- Add the message to `apis/asyncapi.yaml` under this service's channel
  (`defaultContentType: application/cloudevents+json`, the shared
  CloudEvents envelope schema with every attribute required), with its
  exact `type` const and `dataschema`, matching the entity-grouping
  convention already there (group by aggregate, not chronologically)
- Add the type to the catalogue in
  `docs/adr/0004-cloudevents-envelope-and-type-catalogue.md` and to
  `.claude/rules/integration-events.md`; `TestEventCatalogueMatchesContract`
  in `internal/architecture/` fails if they drift from the AsyncAPI file.
- Lint the contract: `spectral lint apis/asyncapi.yaml`.

### 5. Test

Add a golden exact-JSON unit test for the new `type` asserting every
CloudEvents attribute, the `type` string and the `content-type` header
(see `internal/adapters/outbound/kafka/encoder_test.go` — never a real broker in
a unit test). If this event
now needs a `_integration_test.go` asserting real delivery, it MUST use
testcontainers (see the fitness test `TestKafkaIntegrationTestsUseTestcontainers`
in `internal/architecture/` — a skip-gated `KAFKA_BROKERS` test or a
hardcoded `localhost:9092` fails CI).

## Consuming an integration event from a sibling context

### 1. Never import the sibling's Go packages

This service knows a sibling's topic name, its exact CloudEvents `type`
strings and payload shape ONLY — never its Go types. See
`internal/adapters/inbound/kafka/legacy_importer.go`: it knows inventory-storage's
`LegacyTopic` and `TypeLegacyProductClassified` and a hand-mirrored
snake_case payload, nothing else. Hand-mirror the payload struct locally; do not add a Go module
dependency on the sibling repo (an architecture fitness test in most
repos in this fleet would catch that anyway for the stricter contexts —
check this repo's own `internal/architecture/` for a
`TestNoSiblingContextOutboundCalls`-style guard before assuming it's
allowed).

### 2. Decode CloudEvents only, dispatch on the full `type`

```go
evt, err := cloudevents.Decode(msg.Value)
if err != nil { // errors.Is(err, cloudevents.ErrNotCloudEvent): deterministic poison
    // existing DLQ path if this consumer has one, else:
    logger.Warn("skipping non-CloudEvents message", "topic", msg.Topic,
        "partition", msg.Partition, "offset", msg.Offset, "err", err)
    return commit(msg) // never crash, never block the partition
}
switch evt.Type() {
case "com.warehouse.wes.fulfillment-execution.task.TaskCompleted": // exact, byte-identical to the producer
    var p taskCompletedData // local mirror of the payload
    if err := evt.DataAs(&p); err != nil { /* poison: skip as above */ }
    // dedupe on evt.ID(); use evt.Time() / evt.Subject() from attributes
default:
    return commit(msg) // unknown types are ignored, not errors
}
```

Never parse a legacy flat shape as a fallback, never dispatch on a short
name or suffix match. Add a test that a legacy flat-envelope message is
rejected (skipped/DLQ'd), not parsed.

### 3. Choose the right consumer-group pattern — this is the part that bites

Two DIFFERENT correct patterns exist. Picking the wrong one for your use
case is THE most common integration-event mistake in this fleet, and it
was learned from a real incident (wes-work-planning#67).

**Pattern A — long-lived, single-instance consumer group (a named
constant).** Use when exactly ONE instance of this consumer ever runs at
a time (e.g. this service's own analytics projector). The group id is a
plain named constant (`AnalyticsConsumerGroup`), reused across restarts —
that's correct because Kafka's committed-offset resume semantics are
EXACTLY what you want: pick up where the single instance left off.

**Pattern B — per-process-unique consumer group (a generated id).** Use
when this consumer rebuilds a complete read model from a topic's FULL
history on every start (an event-sourced local cache, not a work queue) —
see inventory-storage's facility-cache consumer (`consumerGroupPrefix` +
`uniqueConsumerGroup()`); product-master has no such consumer. Its legacy
importer is Pattern A with the group from `LEGACY_IMPORT_CONSUMER_GROUP`. The group id MUST be unique per process instance
(hostname+PID+timestamp), NEVER a fixed shared string. Consumer group
offsets are shared infrastructure state: a brand-new process joining a
group an EARLIER instance already consumed resumes from that instance's
committed offset, so the new process gets marked "ready" with an empty
local cache having replayed nothing — a silent correctness bug, not a
crash.

**Never do this** (the actual incident): a fixed shared consumer group id
on a consumer meant to run as exactly one instance per environment. When
a local dev/test harness process joins the SAME broker's SAME group as a
live in-cluster Deployment, Kafka's rebalance protocol hands the
partition to only ONE of the two group members — the other silently
starves. Fix: make the group id env-configurable
(`KAFKA_CONSUMER_GROUP`/`<SERVICE>_CONSUMER_GROUP`), never hardcode it as
a literal string. This fleet's `internal/architecture/`
`TestKafkaConsumerGroupNeverHardcodedInline` fitness test (where present)
enforces this statically — an inline `GroupID: "literal"` fails CI.

### 4. Readiness gate, if this consumer backs a local cache

If the consumer replays a topic's full history to build a cache other
code depends on, expose a `Ready()` gate the health check consults, and
block readiness (not process startup — a transient Kafka outage
shouldn't be fatal) until the initial replay finishes. A readiness check
that only re-evaluates on a NEW message arriving deadlocks forever on an
ordinary restart where a shared/already-caught-up group never gets a new
message to trigger it — use `OffsetFetch` against the group's committed
offset, or (simpler and less bug-prone) just use the per-process-unique
group pattern above, which sidesteps the whole class of bug.

## Verify before opening the PR

```bash
make check-all    # includes arch-test — will catch a sibling-package import
```

Prove any new fitness-test-adjacent behavior actually matters by running
the specific scenario against a real broker if this repo has
testcontainers-based integration tests for the consumer/publisher touched.

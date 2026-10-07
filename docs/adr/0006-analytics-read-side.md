# ADR 0006: Analytics read side: an analytics stream, a projector and a master data quality report over a separate analytical database

## Status

Accepted (2026-10-07). Additive: the OLTP API, the integration topic
`warehouse.product-master.events` and its payloads are unchanged (the
integration encoder golden tests pass unchanged). No OLTP migration is needed
(see section 3). Adds a second, separate database. Closes the "Not published
(yet)" note of ADR 0004.

## Context

Every OLTP bounded context of the fleet ships an analytics read side: a
separate analytical Postgres fed by a **projector** that consumes the
context's own analytics topic, and a read-only **reports** binary over that
database (warehouse-planning ADR 0005, workforce-management, facility-layout,
network-fulfillment). ADR 0004 said product-master had no analytics topic in
v1 and that one would follow with its own ADR. This is that ADR. It mirrors
warehouse-planning ADR 0005 and records where this service differs.

The question the data product answers is **master data quality**: is the
product master filling in? How many products were registered, classified,
given declared dimensions and measured each day, how many measured products
disagree with their declared dimensions, and what share of the catalogue is
classified and measured right now. Downstream contexts (slotting, cartonization,
hazmat routing) degrade silently when SKUs lack a classification or a physical
profile, so this is the operational KPI of this context.

## Decision

### 1. What is projected

All five published Product events
(`com.warehouse.wms.product-master.product.{ProductRegistered,
ProductDescriptionChanged, ProductClassified, ProductDimensionsDeclared,
ProductMeasured}`) are ALSO written to `warehouse.product-master.analytics`, as
CloudEvents 1.0 with the **same `type` and the same `id` per occurrence** as
the integration message, `subject` and Kafka key = the SKU, and
`dataschema=urn:warehouse:product-master:analytics:<EventName>:v1`.

### 2. Payloads: equal to the integration payloads

The analytics payload of every event is byte-for-byte the integration
payload. Nothing is added: the report needs the SKU, the aggregate `version`,
the presence of `declared` / `measured` and the `discrepancy` flag of the full
physical profile, all already in the integration payload, plus the CloudEvents
`time`. A later analytics-only field would be additive and documented in
`apis/asyncapi.yaml`, as warehouse-planning did with `binding_constraint`.

`ProductDescriptionChanged` is projected only as "this SKU exists" (a product
registered before the analytics stream started becomes known on its next
event); its description is not stored.

### 3. One transaction, two topics, one id

`FanoutEncoder` (what `cmd/api` hands the use cases, including the legacy
importer) turns every domain event into **two outbox rows, the integration row
first and the analytics row second, under one CloudEvents id minted once and
persisted with both**. The use case's single `ports.UnitOfWork` inserts them
with the product row, so they commit or roll back together; a relay retry
republishes each row's persisted bytes, so the id never changes. The
integration `Encoder` and its bytes are untouched.

warehouse-planning needed an OLTP migration for this (`UNIQUE (event_id)` ->
`UNIQUE (event_id, topic)`). product-master's `outbox_events` was created with
`UNIQUE (event_id, topic)` from migration `0001`, so no migration is needed.

### 4. The analytical model

Two tables plus the processed-event set (`analytics/migrations/0001`):

- `product_facts`, one row per SKU: `registered_at`, `first_classified_at`,
  `first_declared_at`, `first_measured_at` (each the CloudEvents `time` of the
  earliest such event seen, kept with `LEAST`, so arrival order does not
  matter), and the **current** physical-profile state (`has_declared`,
  `has_measured`, `discrepancy`) guarded by `profile_version`: a profile event
  overwrites it only when its `version` is greater than the stored one (the
  ADR 0004 consumer rule).
- `profile_states`, one row per `(sku, version)` of a profile event:
  `occurred_at`, `has_declared`, `has_measured`, `discrepancy`. It is the
  history that answers "how many discrepancies were open at the end of day D"
  (the latest state of each SKU at that instant), which the current-state
  table cannot.
- `analytics_processed_events`: the dedupe set and the freshness source.

### 5. The projector (`cmd/product-projector`)

- Consumes the analytics topic under a **fixed consumer group read from env**
  (`ANALYTICS_CONSUMER_GROUP`, default `product-master-analytics`, set by the
  chart). At-least-once, not a full-replay cache: committed offsets are
  honoured, a brand-new group starts at the earliest offset so the model can
  be rebuilt from retained history.
- **Dedupe and effect in one transaction**: `Projection.Apply` inserts the
  CloudEvents `id` into `analytics_processed_events` (`ON CONFLICT DO NOTHING`)
  and, only if it was inserted, upserts `product_facts` and inserts the
  `profile_states` row in ONE analytical-database transaction. The offset is
  committed only after `Apply` returned.
- **Policy** (warehouse-planning's, which differs from the older siblings):
  - not a CloudEvents 1.0 message: skipped and committed past, with a
    **rate-limited WARN** (the first, then at most one per minute with the
    number suppressed);
  - a valid CloudEvent of another type: acknowledged and ignored;
  - a known type with an unusable payload (`sku` missing or not the
    `subject`, `version` < 1, no `time`, a profile event without
    `discrepancy`, a `ProductDimensionsDeclared` without `declared`, a
    `ProductMeasured` without `measured`) or one the store deterministically
    rejects (Postgres SQLSTATE class 22 or 23): **dead-lettered at once** to
    `warehouse.product-master.analytics.dlq` (raw bytes plus
    `x-dlq-source-topic`, `x-dlq-error`, `x-dlq-failed-at` headers), then
    committed past; a failed DLQ write retries the message, so poison is never
    lost;
  - a **transient** failure (database down, timeout): the same message is
    retried with capped exponential backoff and never dead-lettered, so a
    database outage cannot turn into silently missing analytics.
- Boot: the embedded analytical migrations (`analytics/migrations`, compiled
  into the binary like the OLTP ones) and the first ping run under
  `internal/bootretry`; Kafka is dialled lazily by the reader. `/healthz` and
  `/readyz` on `:8091`; graceful shutdown flips readiness, drains, lets the
  in-flight message finish, then closes the pool.
- It writes only to the analytical database (`ANALYTICS_DATABASE_URL`, a
  direct DSN) and never opens the OLTP database.

### 6. The report (`cmd/product-reports`, `:8092`)

Read-only HTTP over the analytical database through a read-only pool
(`default_transaction_read_only=on`); `/healthz`; RFC 7807 errors; no auth
(fleet rule); additive OpenAPI paths in `apis/openapi.yaml` (tag `reports`,
server `http://localhost:8092`).

| endpoint | question | shape |
| --- | --- | --- |
| `GET /reports/master-data-quality` | per UTC day: products registered, newly classified, newly given declared dimensions, newly measured, and the declared-vs-measured discrepancies open at the end of the day; plus the current coverage of the whole catalogue | `from`, `to`, `days[]`: `day`, `registered`, `classified`, `dimensions_declared`, `measured`, `open_discrepancies`; `coverage`: `products`, `classified`, `dimensions_declared`, `measured`, `open_discrepancies`, `classified_ratio`, `dimensions_declared_ratio`, `measured_ratio`, `discrepancy_ratio` |
| `GET /reports/freshness` | how far the projection is behind (fleet analytics charter) | `as_of` (newest applied CloudEvents time), `lag_seconds` (now - as_of, never negative); both `null` until the first event |

Rules:

- `from` / `to` are optional RFC 3339 instants; both omitted = the 30 days
  ending now; at most 366 days; `from` inclusive, `to` exclusive; an empty or
  inverted range is a 400.
- `days[]` is dense: one row per UTC calendar day that intersects
  `[from, to)`, zeros included. A day's counts cover the part of the day
  inside the range. `classified`, `dimensions_declared` and `measured` count
  products whose FIRST such event fell in that window (a reclassification or a
  re-measurement does not count twice), so the daily rows add up to coverage
  growth. `open_discrepancies` counts the SKUs whose latest profile state at
  the end of the window (the day's end, or `to` for the last partial day) has
  `discrepancy=true`.
- `coverage` is the current state of every SKU the projection knows,
  independent of the range: `products` is the number of distinct SKUs seen in
  any event (every product event implies a registered product);
  `classified_ratio = classified / products`, `measured_ratio = measured /
  products`, `dimensions_declared_ratio = dimensions_declared / products`,
  `discrepancy_ratio = open_discrepancies / measured`; a ratio over zero is 0.
- SQL only counts; ratios, the range rules and the freshness lag live in
  `internal/analytics/report` (pure, imports nothing internal, in the coverage
  set). The in-memory store and the Postgres store run the same contract test.

### 7. Why a separate analytical database

The projection is a derived copy that can be dropped and rebuilt from the
topic; keeping it out of the OLTP database means a heavy report cannot take
OLTP connections, the two schemas evolve independently, the reports role can
be read-only on a database that holds nothing transactional, and the
projector has no credentials on OLTP data. The cost is a second role/database
to provision in `warehouse-infra` (`analytics_services` entry; an
already-populated Postgres does not re-run its init script).

### 8. Packaging

The one image builds every `cmd/*`, so `/app/product-projector` and
`/app/product-reports` ship in it (uid 1000); the analytical migrations are
embedded, so nothing extra is copied. The chart gains two components,
`analytics-projector` (Deployment) and `analytics-reports` (Deployment and
Service `<release>-reports:80`), plus a Secret with `ANALYTICS_DATABASE_URL`
and `ANALYTICS_READER_DATABASE_URL`, behind `analytics.enabled` (default
`false`: the default render is unchanged). The chart refuses to render
analytics without a DSN source or without Kafka. External routing to the
reports Service is warehouse-infra's job, not the chart's.

## Consequences

- Two outbox rows per event; the relay publishes both through the same sink.
  Tests that count outbox rows for one write now see two per event.
- The analytics stream is not an integration contract: only
  `product-projector` consumes it. Other contexts keep reading
  `warehouse.product-master.events`.
- Products registered, classified or measured before the analytics stream
  existed are only partly visible (a SKU appears on its next event; its
  first-event days are the days seen on the stream). Replaying from the
  earliest retained offset is the backfill.
- A new published event type must be added to BOTH encoders' switch
  (`payloadFor` is shared, so this is automatic today) and to the projector's
  kind table, or it is silently absent from the report.

## Alternatives considered and rejected

- **Project from the integration topic** (a second consumer group on
  `warehouse.product-master.events`): the fleet convention is a dedicated
  analytics topic owned by the producer, and a future analytics-only field
  could not be added without touching the integration contract.
- **Two ids per occurrence**: breaks the "same type and id on both topics"
  rule that lets a consumer correlate the streams and keeps retries
  idempotent per occurrence.
- **Count every ProductClassified / ProductMeasured per day**: re-measurements
  and reclassifications would inflate the daily rows and they would no longer
  add up to coverage; the first-event semantics answers "is the master filling
  in". Re-measurement volume can be a later report.
- **Open discrepancies from current state only**: cannot answer "open at the
  end of day D" once a discrepancy is resolved; hence `profile_states`.
- **Compute the report from the OLTP `products` table**: current state only,
  no per-day history, and analytical queries on the OLTP database.
- **Dead-letter after N attempts for every failure**: drops analytics for
  events that failed only because the database was briefly down.

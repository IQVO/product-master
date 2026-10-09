---
id: troubleshooting
title: Troubleshooting
sidebar_label: Troubleshooting
---

# Troubleshooting

Symptom, cause, how to check, how to fix. Every row is a failure mode that the
code can actually produce (the error strings are quoted from it). For the
signals referred to here see [Observability](/docs/operations/observability);
for the procedures, the [runbook](/docs/operations/runbook).

## Startup and readiness

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| Pod in CrashLoopBackOff; last log `run migrations (after 5 attempts): ...` or `ping postgres (after 5 attempts): ...` | Postgres unreachable or the DSN is wrong. The boot retry gives up after 5 attempts (1s, 2s, 4s, 8s apart). | `kubectl logs --previous` for the error; the `retrying` WARN lines show each attempt. | Fix the Secret behind `DATABASE_URL`/`MIGRATIONS_DATABASE_URL` or the database itself; the pod recovers on the next restart. |
| Boot hangs or fails at the migration step only when going through PgBouncer | golang-migrate takes a session-scoped advisory lock that PgBouncer transaction pooling cannot hold. | Is `DATABASE_URL` a PgBouncer DSN and `MIGRATIONS_DATABASE_URL` unset? | Set `MIGRATIONS_DATABASE_URL` (chart key `database.migrationsExistingSecretKey`) to a direct Postgres DSN. For the projector `ANALYTICS_DATABASE_URL` itself must be direct. |
| `Dirty database version 1. Fix and force version.` | A migration failed half-way and golang-migrate marked its `schema_migrations` table dirty. | `SELECT * FROM schema_migrations;` in the affected database (OLTP or analytical). | Repair the schema by hand, then reset the row (`dirty = false`, correct `version`), and restart. |
| Process exits with `unknown EVENT_PUBLISHER "..." (want kafka or log)` | Typo in `EVENT_PUBLISHER`. | Pod env. | Use `kafka` or `log` (empty means `log`). |
| Process exits with `EVENT_PUBLISHER=kafka requires KAFKA_BROKERS` or `LEGACY_IMPORT_CONSUMER_GROUP requires KAFKA_BROKERS` | Kafka mode or the importer is on without brokers. The chart refuses to render this, so it happens with hand-made env. | Pod env. | Set `KAFKA_BROKERS` (chart: `kafka.enabled=true`). |
| Projector exits with `ANALYTICS_DATABASE_URL is required ...` or `KAFKA_BROKERS is required ...` | Missing required variable. | Pod env, analytics Secret keys. | Provide both (chart: `analytics.database.*`, `kafka.enabled`). |
| Projector exits with `analytics consumer stopped unexpectedly: ...` | The Kafka reader itself failed (not a handling error, which is retried). | Log line before the exit. | Usually broker connectivity; the restart resumes from committed offsets. |
| Pod never becomes Ready | `/readyz` is served only after the listener opens, and the listener opens only after migrations and the first ping succeeded. A pod stuck in the boot retry is therefore not Ready; once the listener is up `/readyz` is ready until shutdown (it does not check Postgres or Kafka). | Logs for `retrying`; `kubectl describe pod` for probe failures; compare `HTTP_ADDR` with the container port 8080. | Fix the database, or keep `config.httpAddr` and `service.targetPort` aligned. |
| MCP or reports pod Ready although its database is down | Their readiness probes use `/healthz`, which never checks the database. | Tool calls return `internal-error`; reports return `500 report-store-error`. | Fix the database; the probes will not catch this. |
| Data disappears on every restart | `DATABASE_URL` is unset, so `cmd/api` uses in-memory adapters. | First log line `DATABASE_URL not configured; using in-memory adapters`. | Set `DATABASE_URL`. |
| Repeated INFO `traces export: exporter export timeout ... connection refused` | No OpenTelemetry Collector at `OTEL_EXPORTER_OTLP_ENDPOINT`. | Endpoint value. | Harmless; point it at a collector or set `otel.enabled=false` in the chart. |

## Events not reaching consumers

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| Sibling contexts never see new classifications; logs show `event published (log sink)` | `EVENT_PUBLISHER` is `log` (the default). Rows are marked published but nothing is sent. | `outbox relay running` log line shows `"publisher":"log"`. | Set `config.eventPublisher=kafka` with `kafka.enabled=true`. Rows already marked published by the log sink are not re-sent automatically (see [re-publish](/docs/operations/runbook#re-publish-an-event)). |
| Outbox backlog grows, `outbox relay pass failed` ERRORs | Kafka unreachable or rejecting the write. The relay stops at the first failing row each pass, so everything behind it waits. | `SELECT count(*), max(attempts) FROM outbox_events WHERE published_at IS NULL;` and the `last_error` column. | Fix Kafka connectivity; the relay catches up by itself in id order. Nothing is lost. |
| One SKU's later events overtake nothing but everything is stuck behind one row | A single row fails forever (it keeps `attempts` rising, `last_error` set). | Lowest unpublished `id` with a non-null `last_error`. | Fix the cause shown in `last_error`. There is no attempt limit and no outbox dead letter. |
| Consumers receive an event twice | At-least-once delivery: a crash between send and `UPDATE ... published_at` re-sends the row with the same CloudEvents `id`. | Same `id` twice on the topic. | Expected; consumers deduplicate on `id`. |

## Analytics read side

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| `GET /reports/freshness` returns `{"as_of":null,"lag_seconds":null}` | The projector has applied no event yet. | Is `cmd/api` publishing to Kafka (`EVENT_PUBLISHER=kafka`)? Is the projector running (`analytics consumer running`)? | Enable Kafka publishing; the analytics topic is filled by the same relay. |
| Freshness lag keeps growing (consumer lag) | Projector blocked on a transient error (analytical database down, timeouts): the same message is retried forever and the offset is not committed. | ERROR `analytics consumer handling failed; retrying the same message` with rising `attempt`. | Fix the analytical database; the projector continues from the blocked message. |
| Messages on `warehouse.product-master.analytics.dlq` | A known event type with an unusable payload (`sku` different from the CloudEvents `subject`, missing `version`, missing `discrepancy` on a profile event, failed validation) or a store rejection (SQLSTATE class 22 or 23). | ERROR `analytics: dead-lettering ...`; the `x-dlq-error` header of each DLQ message. | Fix the producer or schema, then re-drive (see the [runbook](/docs/operations/runbook#re-drive-the-dlq)). |
| WARN `analytics: skipping message that is not a CloudEvents 1.0 event` | Something wrote a non-CloudEvents message to the analytics topic. | `topic`, `partition`, `offset` fields. | Find the writer; only product-master's relay should write there. |
| `GET /reports/master-data-quality` returns `400 invalid-report-range` | `from`/`to` not RFC 3339, `from` not before `to`, or the range is longer than 366 days. | The `detail` field names the rule. | Send RFC 3339 timestamps, e.g. `2026-10-01T00:00:00Z`. |
| Reports return `500 report-store-error` | The analytical query failed (connection, 15s statement timeout). | `report query failed` ERROR log with the real error. | Fix the database; narrow the range if it times out. |
| `404` for `http://localhost:8000/api/product-master/reports/...` | Known warehouse-infra routing issue: Kong sends the request to the OLTP API, which has no `/reports` route. | Same request through a port-forward works. | `kubectl -n warehouse-systems port-forward svc/product-master-reports 8092:80`. |

## Legacy importer (ADR 0003)

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| Nothing imported, no importer log lines | `LEGACY_IMPORT_CONSUMER_GROUP` is unset, so the importer is not started. | INFO `LEGACY_IMPORT_CONSUMER_GROUP not set; legacy classification importer not started`. | Set the group (chart `config.legacyImportConsumerGroup`). |
| `legacy classification processed` with `"outcome":"unchanged"` | The product already has a `native` classification (never overwritten) or the same classification. | `GET /products/{sku}/classification` shows `classificationSource`. | Expected behaviour. |
| `"outcome":"duplicate"` | The CloudEvents `id` was already claimed in `processed_events`. | `SELECT * FROM processed_events WHERE event_id = '...'`. | Expected on redelivery. |
| WARN `skipping an invalid legacy classification` or `skipping a malformed legacy ProductClassified payload` | Bad SKU, unknown tag or broken invariant in inventory-storage's message. | `error` field. | Fix the source data in inventory-storage; the message is committed past and not retried. |

## REST errors

All errors are RFC 7807 documents with `type` =
`https://errors.product-master.warehouse-systems.dev/` + slug.

| Status and slug | Cause | Fix |
| --- | --- | --- |
| `400 malformed-request` | Body is not valid JSON, has an unknown field, trailing data or exceeds 64 KiB; a dimension field is missing; `measuredAt` missing or `deviceId` longer than 64 characters; list query with `limit` 0 or outside 1..500, a bad `cursor`, an unknown `handlingTag`, or `classified` not `true`/`false`. | Correct the request; field names are camelCase (`lengthMm`, `handlingTags`). |
| `400 invalid-sku` | SKU empty, longer than 64 characters, or containing whitespace, a control character or `/` (also when URL-encoded as `%2F`). | Use a valid SKU. |
| `400 invalid-description` | More than 200 characters or a control character. | Shorten the description. |
| `400 no-handling-tags`, `unknown-handling-tag`, `duplicate-handling-tag` | Empty, unknown (not `Hazmat`, `Fragile`, `TemperatureSensitive`, `Oversized`, `HighValue`) or repeated tag. | Fix the tag list. |
| `400 temperature-class-required`, `temperature-class-not-applicable`, `unknown-temperature-class` | `TemperatureSensitive` without a class, a class without the tag, or a class other than `Ambient`, `Chilled`, `Frozen`. | Send the class only with `TemperatureSensitive`. |
| `400 invalid-dot-hazard-class`, `dot-hazard-class-not-applicable` | `dotHazardClass` outside 1..9 (an explicit `0` included), or present without `Hazmat`. | Omit it or send 1..9 with `Hazmat`. |
| `400 invalid-dimension`, `invalid-weight` | A dimension outside 1..20000 mm or a weight outside 1..2000000 g. | Fix the values. |
| `400 measured-at-in-future` | `measuredAt` is after the service clock. | Check the device clock. |
| `404 product-not-found` | The SKU is not registered. Attributes of an unknown SKU are never an implicit registration. | `PUT /products/{sku}` first. |
| `404 product-classification-not-found` | `GET .../classification` on a registered but unclassified product. | Classify it, or read `GET /products/{sku}`. |
| `409 stale-measurement` | The measurement is older than the one already recorded. | Send only newer readings. |
| `409 concurrent-modification` | Two writes to the same SKU raced; the version guard rejected the loser. | Re-read the product and retry. |
| `500 internal-error` | Unmapped error, logged as `request failed` (the detail is never returned). | Read the log line. |

Not produced by this service: there is no `412` (no `If-Match`/ETag
precondition; the version guard is internal), no `422` (validation errors are
`400`), and no `Idempotency-Key` header. Every `PUT` is idempotent by
itself: repeating the same request changes nothing and returns `200`. There is
no circuit breaker either: the service has no outbound HTTP client, and Kafka
failures are absorbed by the outbox.

## MCP and console

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| Every MCP tool returns `product-not-found` for SKUs that exist in REST | `cmd/mcp` runs without `DATABASE_URL` (its own empty in-memory store) or against another database. | Log `DATABASE_URL not configured; using the in-memory product repository`. | Give it the same `DATABASE_URL` as `cmd/api`. |
| Tool result `isError: true` with `internal-error: an unexpected internal error occurred` | Database error behind the read use case. | ERROR `mcp tool failed with an unexpected error`. | Fix the database. |
| MCP clients lose their session intermittently | More than one `cmd/mcp` replica: sessions live in process memory and the Service has no affinity. | `kubectl get deploy <fullname>-mcp`. | Keep `mcp.replicaCount: 1`. |
| Browser shows a CORS error when the standalone remote (`npm run dev`, `:5191`) calls `localhost:8080` | `CORS_ALLOWED_ORIGINS` defaults to `http://localhost:5173` (the console shell's dev port) only. | Response lacks `Access-Control-Allow-Origin`. | Start `cmd/api` with `CORS_ALLOWED_ORIGINS=http://localhost:5173,http://localhost:5191`. In the cluster Kong handles CORS. |

## Shutdown

| Symptom | Cause | Fix |
| --- | --- | --- |
| WARN `worker did not stop before the shutdown drain deadline` (`worker` = `legacy-importer` or `outbox-relay`) | The importer or relay was mid-retry when SIGTERM came and did not return within 10s. | Harmless for data: unpublished rows stay in the outbox and uncommitted messages are redelivered. Check what it was retrying. |
| Requests fail during rollout | The pod was removed before `/readyz` stopped routing to it. | Keep `SHUTDOWN_DRAIN_DELAY` (5s) above the readiness probe period (5s) and `terminationGracePeriodSeconds` (40) above the sum of the drains. |

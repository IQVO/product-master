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
| `cmd/api` exits with `invalid configuration: DATABASE_URL is required: this context runs on real Postgres` and/or `KAFKA_BROKERS is required: this context runs on real Kafka` | A mandatory variable is missing. There is no in-memory mode and no log sink ([ADR 0010](/docs/adr/0010-postgres-and-kafka-are-the-only-runtime)). The chart refuses to render this, so it happens with hand-made env. | Pod env (the error names every missing variable). | Set `DATABASE_URL` and `KAFKA_BROKERS` (chart: `database.*`, `kafka.brokers`). |
| `cmd/api` exits with `EVENT_PUBLISHER=log is not supported: this context publishes to real Kafka` or `unknown EVENT_PUBLISHER "..." ...` | `EVENT_PUBLISHER` is set to something other than `kafka`. | Pod env; chart `config.eventPublisher`. | Unset it or set `kafka`. |
| `cmd/mcp` exits with `DATABASE_URL is required: this context runs on real Postgres` | No database for the MCP server (it no longer falls back to an empty in-memory repository). | Pod env. | Give it the same `DATABASE_URL` as `cmd/api`. |
| Projector exits with `ANALYTICS_DATABASE_URL is required ...`, `KAFKA_BROKERS is required ...` or `ANALYTICS_CONSUMER_GROUP is required ...` | Missing required variable (the consumer group has no default). | Pod env, analytics Secret keys. | Provide all three (chart: `analytics.database.*`, `kafka.brokers`, `analytics.projector.consumerGroup`). |
| Projector exits with `analytics consumer stopped unexpectedly: ...` | The Kafka reader itself failed (not a handling error, which is retried). | Log line before the exit. | Usually broker connectivity; the restart resumes from committed offsets. |
| Pod never becomes Ready, or `GET /readyz` returns `503` with problem type `dependency-unavailable` | `/readyz` is served only after the listener opens (after migrations and the first ping) and then answers `200` only while Postgres answers a ping and a Kafka broker answers a broker-list request. The detail names the failing dependency (`postgres` or `kafka`). A not-ready pod gets no traffic but is not restarted (`/healthz` does not check dependencies). | `curl <pod>:8080/readyz` for the detail; logs for `retrying`; `kubectl describe pod` for probe failures; compare `HTTP_ADDR` with the container port 8080. | Fix the database or the broker (`KAFKA_BROKERS`), or keep `config.httpAddr` and `service.targetPort` aligned. Missing topics never make it not-ready: the broker auto-creates them on the first write. |
| MCP or reports pod Ready although its database is down | Their readiness probes use `/healthz`, which never checks the database. | Tool calls return `internal-error`; reports return `500 report-store-error`. | Fix the database; the probes will not catch this. |
| Repeated INFO `traces export: exporter export timeout ... connection refused` | No OpenTelemetry Collector at `OTEL_EXPORTER_OTLP_ENDPOINT`. | Endpoint value. | Harmless; point it at a collector or set `otel.enabled=false` in the chart. |

## Events not reaching consumers

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| Sibling contexts never see new classifications although the API accepts writes | The outbox relay is behind (Kafka was unreachable) or `KAFKA_BROKERS` points at the wrong broker. There is no log sink any more: unpublished rows stay in `outbox_events`. Rows marked published by an old log-sink deployment (before [ADR 0010](/docs/adr/0010-postgres-and-kafka-are-the-only-runtime)) are not re-sent automatically (see [re-publish](/docs/operations/runbook#re-publish-an-event)). | `outbox relay running` log line shows the `brokers`; the backlog query in the runbook; `/readyz`. | Fix `KAFKA_BROKERS`/connectivity; the relay catches up by itself. |
| Outbox backlog grows, `outbox relay pass failed` ERRORs | Kafka unreachable or rejecting the write. The relay stops at the first failing row each pass, so everything behind it waits. | `SELECT count(*), max(attempts) FROM outbox_events WHERE published_at IS NULL;` and the `last_error` column. | Fix Kafka connectivity; the relay catches up by itself in id order. Nothing is lost. |
| One SKU's later events overtake nothing but everything is stuck behind one row | A single row keeps failing (`attempts` rising, `last_error` set) and has not reached `OUTBOX_MAX_ATTEMPTS` yet (default 20), or the cap is `0` (unlimited). | Lowest unpublished `id` with a non-null `last_error`. | Fix the cause shown in `last_error`. With the cap the row is excluded after 20 failures and the relay moves on. |
| `outbox relay: row reached the attempt cap ...` ERROR; one event never reaches Kafka | A poison row (too large, rejected by the broker) failed `OUTBOX_MAX_ATTEMPTS` times and is excluded; later rows, same SKU included, overtook it ([ADR 0007](/docs/adr/0007-outbox-relay-attempt-cap)). | `SELECT id, topic, event_type, key, attempts, last_error FROM outbox_events WHERE published_at IS NULL AND attempts >= 20;` | Fix the cause, then `UPDATE outbox_events SET attempts = 0 WHERE id = <id>;` ([runbook](/docs/operations/runbook#re-arm-a-row-excluded-by-the-attempt-cap)). |
| Consumers receive an event twice | At-least-once delivery: a crash between send and `UPDATE ... published_at` re-sends the row with the same CloudEvents `id`. | Same `id` twice on the topic. | Expected; consumers deduplicate on `id`. |

## Analytics read side

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| `GET /reports/freshness` returns `{"as_of":null,"lag_seconds":null}` | The projector has applied no event yet. | Is the `cmd/api` relay publishing (backlog in `outbox_events`)? Is the projector running (`analytics consumer running`)? | Fix the relay or the broker; the analytics topic is filled by the same relay. |
| Freshness lag keeps growing (consumer lag) | Projector blocked on a transient error (analytical database down, timeouts): the same message is retried forever and the offset is not committed. | ERROR `analytics consumer handling failed; retrying the same message` with rising `attempt`. | Fix the analytical database; the projector continues from the blocked message. |
| Messages on `warehouse.product-master.analytics.dlq` | A known event type with an unusable payload (`sku` different from the CloudEvents `subject`, missing `version`, missing `discrepancy` on a profile event, failed validation) or a store rejection (SQLSTATE class 22 or 23). | ERROR `analytics: dead-lettering ...`; the `x-dlq-error` header of each DLQ message. | Fix the producer or schema, then re-drive (see the [runbook](/docs/operations/runbook#re-drive-the-dlq)). |
| WARN `analytics: skipping message that is not a CloudEvents 1.0 event` | Something wrote a non-CloudEvents message to the analytics topic. | `topic`, `partition`, `offset` fields. | Find the writer; only product-master's relay should write there. |
| `GET /reports/master-data-quality` returns `400 invalid-report-range` | `from`/`to` not RFC 3339, `from` not before `to`, or the range is longer than 366 days. | The `detail` field names the rule. | Send RFC 3339 timestamps, e.g. `2026-10-01T00:00:00Z`. |
| Reports return `500 report-store-error` | The analytical query failed (connection, 15s statement timeout). | `report query failed` ERROR log with the real error. | Fix the database; narrow the range if it times out. |
| `404` for `http://localhost:8000/api/product-master/reports/...` | The reports HTTPRoute (`charts/product-master/templates/reports-route.yaml`, path `/api/product-master/reports`) is missing or not accepted, so Kong matches the OLTP API's `/api/product-master` route, which has no `/reports` path. warehouse-infra recorded this on 2026-10-07, before the route moved into this chart (`8c22a05`); it has not been re-tested since. | `kubectl -n warehouse-systems get httproute` lists `product-master-reports`; the same request through a port-forward works. | Check `analytics.enabled` and `analytics.reports.route.enabled`; meanwhile `kubectl -n warehouse-systems port-forward svc/product-master-reports 8092:80`. |

## Classification source `legacy-import`

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| `classificationSource` (REST) or `classification_source` (MCP, events) is `legacy-import` | The classification was imported from inventory-storage by the ADR 0003 importer before stage E (2026-10-17) and nobody has classified the SKU here since. The importer no longer exists, so no new row gets this value. | `GET /products/{sku}/classification`. | Review it and `PUT /products/{sku}/classification`; even the same values make it `native` and raise `ProductClassified`. |
| `LEGACY_IMPORT_CONSUMER_GROUP` set on a pod has no effect | The variable was removed with the importer at stage E; nothing reads it. | `cmd/api` logs no importer line. | Remove it from the environment. |

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
| Every MCP tool returns `product-not-found` for SKUs that exist in REST | `cmd/mcp` points at another database than `cmd/api` (it cannot start without `DATABASE_URL`). | Compare `DATABASE_URL` of both Deployments. | Give it the same `DATABASE_URL` as `cmd/api`. |
| Tool result `isError: true` with `internal-error: an unexpected internal error occurred` | Database error behind the read use case. | ERROR `mcp tool failed with an unexpected error`. | Fix the database. |
| MCP clients lose their session intermittently | More than one `cmd/mcp` replica: sessions live in process memory and the Service has no affinity. | `kubectl get deploy <fullname>-mcp`. | Keep `mcp.replicaCount: 1`. |
| Browser shows a CORS error when the standalone remote (`npm run dev`, `:5191`) calls `localhost:8080` | `CORS_ALLOWED_ORIGINS` defaults to `http://localhost:5173` (the console shell's dev port) only. | Response lacks `Access-Control-Allow-Origin`. | Start `cmd/api` with `CORS_ALLOWED_ORIGINS=http://localhost:5173,http://localhost:5191`. In the cluster Kong handles CORS. |

## Shutdown

| Symptom | Cause | Fix |
| --- | --- | --- |
| WARN `worker did not stop before the shutdown drain deadline` (`worker` = `outbox-relay`) | The relay was mid-pass when SIGTERM came and did not return within 10s. | Harmless for data: unpublished rows stay in the outbox and are sent by the next pod. Check the `last_error` of the pending rows. |
| Requests fail during rollout | The pod was removed before `/readyz` stopped routing to it. | Keep `SHUTDOWN_DRAIN_DELAY` (5s) above the readiness probe period (5s) and `terminationGracePeriodSeconds` (40) above the sum of the drains. |

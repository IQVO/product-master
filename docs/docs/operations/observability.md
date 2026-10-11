---
id: observability
title: Observability
sidebar_label: Observability
---

# Observability

What each binary emits (traces, metrics, logs), where it goes, and what to
alert on. Everything below is wired in
`internal/adapters/outbound/telemetry`, the routers in
`internal/adapters/inbound/http` and `cmd/mcp/router.go`, and the libraries
pinned in `go.mod` (otelchi v0.12.3, OpenTelemetry Go v1.47.0, contrib runtime
v0.72.0, Prometheus client v1.24.1). There are **no** custom business metric
instruments in this repo today.

## Signals per binary

| Binary | `service.name` | OTLP traces and metrics | `GET /metrics` | Logs |
| --- | --- | --- | --- | --- |
| `cmd/api` | `product-master` | yes | yes (Go and process collectors) | JSON on stdout |
| `cmd/mcp` | `product-master-mcp` | yes | no | JSON on stdout |
| `cmd/product-reports` | `product-master-reports` | yes | no | JSON on stdout |
| `cmd/product-projector` | none | **no** (it never calls `telemetry.Setup`) | no | JSON on stdout |

### Export

- OTLP over gRPC, insecure, to `OTEL_EXPORTER_OTLP_ENDPOINT` (default
  `localhost:4317`; the chart sets
  `otel-collector.observability.svc.cluster.local:4317`).
- Traces: batch span processor. Metrics: periodic reader every **30s**.
- Propagation: W3C `traceparent` and `baggage`.
- Resource attributes: `service.name`, `service.version` (`SERVICE_VERSION`,
  default `dev`), `deployment.environment.name` (`ENVIRONMENT`, default
  `local`), plus the SDK defaults.
- Export never blocks startup or requests. Without a collector the SDK logs an
  INFO line such as `traces export: exporter export timeout ... connection
  refused` and drops the data.

## Metric instruments

All of them come from libraries; the repo defines none of its own.

| Instrument | Type | Unit | Attributes | Emitted by | Source |
| --- | --- | --- | --- | --- | --- |
| `http.server.request.duration` | Float64 histogram (buckets 5 ms to 10 s) | `s` | `http.method`, `http.scheme`, `http.route` (the chi pattern, e.g. `/products/{sku}`); scope attribute `service.name` | api, mcp, reports | otelchi `metric.NewServerRequestDuration`, wired in `internal/adapters/inbound/http/server.go`, `reports_handler.go`, `cmd/mcp/router.go` |
| `go.memory.used` | Int64 observable up-down counter | `By` | `go.memory.type` (`stack`, `other`) | api, mcp, reports | contrib `runtime.Start` in `telemetry.Setup` |
| `go.memory.limit` | Int64 observable up-down counter | `By` | | api, mcp, reports | same (only reported when a memory limit is set) |
| `go.memory.allocated` | Int64 observable counter | `By` | | api, mcp, reports | same |
| `go.memory.allocations` | Int64 observable counter | `{allocation}` | | api, mcp, reports | same |
| `go.memory.gc.goal` | Int64 observable up-down counter | `By` | | api, mcp, reports | same |
| `go.goroutine.count` | Int64 observable up-down counter | `{goroutine}` | | api, mcp, reports | same |
| `go.processor.limit` | Int64 observable up-down counter | `{thread}` | | api, mcp, reports | same |
| `go.config.gogc` | Int64 observable up-down counter | `%` | | api, mcp, reports | same |

The otelchi request-duration histogram carries **no status-code attribute**, so
error rates have to come from traces, logs or the Kong gateway metrics, not from
this histogram.

### `GET /metrics` (cmd/api only)

`telemetry.NewMetricsHandler` serves a Prometheus scrape endpoint over a
private registry holding only the Prometheus client's Go collector
(`go_goroutines`, `go_gc_duration_seconds`, `go_memstats_*`, ...) and process
collector (`process_cpu_seconds_total`, `process_resident_memory_bytes`,
`process_open_fds`, ...). It keeps the pod scrapeable without a collector; the
HTTP metrics above travel over OTLP only. The route is not in
`apis/openapi.yaml`.

### Names in Prometheus

The warehouse-infra collector exports OTel metrics to Prometheus with dots
replaced by underscores and the unit appended, which is how the fleet
dashboards query them: `http_server_request_duration_seconds_bucket` and
`_count` (label `service_name`), `go_memory_used_bytes` (label
`go_memory_type`), `go_goroutine_count`.

## Traces

- Every REST, reports and MCP request runs inside a server span created by
  `otelchi.Middleware` with `WithChiRoutes`. The span name is the matched chi
  route pattern without the method, for example `/products/{sku}`,
  `/products/{sku}/dimensions/measured`, `/reports/master-data-quality`; MCP
  requests get the span of the chi mount that serves them (`/mcp` or `/`).
- There are no hand-written spans: the use cases, the Postgres adapters, the
  outbox relay and the Kafka consumers create none, and the Kafka messages
  carry no trace context header. A trace therefore ends at the HTTP boundary;
  follow an event across services by its CloudEvents `id` in the logs instead.

## Logs

All four binaries log JSON with `log/slog` to stdout (`time`, `level`, `msg`
plus fields); `LOG_LEVEL` sets the threshold. Logs carry no trace or span id.
In the kind cluster Alloy ships them to Loki with the label
`app=product-master` (from the `app.kubernetes.io/name` pod label).

| Message (`msg`) | Level | Fields | Binary | Meaning |
| --- | --- | --- | --- | --- |
| `DATABASE_URL not configured; using in-memory adapters` | INFO | | api | No Postgres: data is lost on restart. |
| `postgres adapters configured` | INFO | | api | Migrations and ping succeeded. |
| `retrying` / `succeeded after retry` | WARN / INFO | `op`, `attempt`, `in`, `err` | all four | Boot retry of the migrations or the first database ping (`internal/bootretry`). |
| `outbox relay running` | INFO | `publisher`, `interval`, `topics` | api | Relay started. |
| `outbox relay pass failed` | ERROR | `error` | api | A send failed; the row keeps `last_error` and is retried. |
| `event published (log sink)` | INFO | `topic`, `type`, `subject`, `id` | api | `EVENT_PUBLISHER=log`: the event was **not** sent to Kafka. |
| `legacy classification importer running` | INFO | `topic`, `group_id`, `brokers` | api | Importer started. |
| `legacy classification processed` | INFO | `event_id`, `sku`, `outcome` (`applied`, `unchanged`, `duplicate`) | api | One legacy message handled. |
| `skipping an invalid legacy classification` and the other `skipping ...` lines | WARN | `event_id`, `sku`, `error` | api | Deterministic bad input, committed past. |
| `legacy classification importer handling failed; retrying the same message` (or `analytics consumer ...`, or `... offset commit failed; ...`) | ERROR | `error`, `partition`, `offset`, `attempt`, `retry_in` | api, projector | Transient failure; the partition is blocked until it succeeds. |
| `analytics consumer running` | INFO | `topic`, `group_id`, `brokers` | projector | Consumer started. |
| `analytics: skipping message that is not a CloudEvents 1.0 event ...` | WARN | `topic`, `partition`, `offset`, `suppressed_since_last_warning` | projector | At most one line per minute. |
| `analytics: dead-lettering a message that can never be projected` | ERROR | `error`, `dlq_topic`, `partition`, `offset` | projector | Poison message sent to the DLQ. |
| `request failed` | ERROR | `error`, `method`, `path` | api | Unmapped error answered as `500 internal-error`. |
| `report query failed` | ERROR | `error`, `path` | reports | Analytical query failed (`500 report-store-error`). |
| `mcp tool failed with an unexpected error` | ERROR | `error` | mcp | Tool answered `internal-error`. |
| `shutdown: readiness flipped to not-ready; waiting for traffic to drain` | INFO | `drain_delay` | api | SIGTERM received. |
| `worker did not stop before the shutdown drain deadline` | WARN | `worker` | api | Importer or relay exceeded 10s at shutdown. |

## Health endpoints

| Binary | Liveness | Readiness |
| --- | --- | --- |
| api `:8080` | `GET /healthz` `200 {"status":"ok"}` | `GET /readyz` `200 {"status":"ready"}`, `503 {"status":"not_ready"}` once shutdown starts |
| mcp `:8090` | `GET /healthz` | none (probes use `/healthz`) |
| projector `:8091` | `GET /healthz` | `GET /readyz` (flips on shutdown) |
| reports `:8092` | `GET /healthz` | none |

None of them checks Postgres or Kafka at request time.

## Dashboards

This repo ships no dashboard. The local warehouse-infra checkout
(`terraform/dashboards/`) has fleet-wide `go-runtime.json`,
`kong-gateway-overview.json` and `logs-overview.json`, and generated
per-context dashboards under `dashboards/contexts/`
(`scripts/gen-context-dashboards.py`), but **no** `product-master.json` among
them; product-master appears only in the fleet-wide ones.

## Suggested alerts

None of these exist yet; they are derived from the failure modes in the code.

| Alert | Signal | Why |
| --- | --- | --- |
| Outbox backlog growing | `SELECT count(*) FROM outbox_events WHERE published_at IS NULL` above a few hundred, or `min(created_at)` older than a few minutes | Kafka unreachable or a row failing forever; downstream copies go stale. |
| Outbox row stuck | any unpublished row with `attempts` above 10 | The relay stops at the first failing row, so one row blocks all later events. |
| Relay in log mode in a cluster | log line `event published (log sink)` where Kafka is expected | `EVENT_PUBLISHER` not set to `kafka`: nothing reaches consumers. |
| Analytics stale | `GET /reports/freshness` `lag_seconds` above your tolerance while writes happen | Projector down, blocked on a transient error, or relay not publishing. |
| DLQ not empty | messages on `warehouse.product-master.analytics.dlq`, or the log line `analytics: dead-lettering ...` | A payload the projector can never apply. |
| Consumer retry loop | repeated `handling failed; retrying the same message` | A partition is blocked (database down, timeouts). |
| REST latency | p95 of `http_server_request_duration_seconds` for `service_name="product-master"` | Pool exhaustion (10 connections) or slow Postgres (5s statement timeout). |
| Pod restarts | kube-state-metrics restarts of any component | Boot retry exhausted (database or analytical database unreachable). |

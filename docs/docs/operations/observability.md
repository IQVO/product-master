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
v0.72.0, Prometheus client v1.24.1). There is **one** custom business instrument,
`product_master.products.registered`, listed under Metric instruments.

## Signals per binary

| Binary | `service.name` | OTLP traces and metrics | `GET /metrics` | Logs |
| --- | --- | --- | --- | --- |
| `cmd/api` | `product-master` | yes | yes (Go and process collectors) | JSON on stdout |
| `cmd/mcp` | `product-master-mcp` | yes | no | JSON on stdout |
| `cmd/product-reports` | `product-master-reports` | yes | no | JSON on stdout |
| `cmd/product-projector` | `product-master-projector` | metrics (Go runtime) and the resource identity; it opens no spans ([ADR 0008](/docs/adr/0008-projector-telemetry-and-environment)) | no | JSON on stdout |

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

All of them come from libraries except `product_master.products.registered`, the one business instrument the repo defines (ADR 0013).

| Instrument | Type | Unit | Attributes | Emitted by | Source |
| --- | --- | --- | --- | --- | --- |
| `http.server.request.duration` | Float64 histogram (buckets 5 ms to 10 s) | `s` | `http.method`, `http.scheme`, `http.route` (the chi pattern, e.g. `/products/{sku}`); scope attribute `service.name` | api, mcp, reports | otelchi `metric.NewServerRequestDuration`, wired in `internal/adapters/inbound/http/server.go`, `reports_handler.go`, `cmd/mcp/router.go` |
| `product_master.products.registered` | Int64 counter | `{product}` | `outcome` (`created`, `description_changed`, `unchanged`) | api | `telemetry.ProductMetrics`, created in `cmd/api/main.go` and incremented on every `RegisterProduct` call; OTLP only (`GET /metrics` serves just the Go and process collectors) |
| `go.memory.used` | Int64 observable up-down counter | `By` | `go.memory.type` (`stack`, `other`) | api, mcp, reports, projector | contrib `runtime.Start` in `telemetry.Setup` |
| `go.memory.limit` | Int64 observable up-down counter | `By` | | api, mcp, reports, projector | same (only reported when a memory limit is set) |
| `go.memory.allocated` | Int64 observable counter | `By` | | api, mcp, reports, projector | same |
| `go.memory.allocations` | Int64 observable counter | `{allocation}` | | api, mcp, reports, projector | same |
| `go.memory.gc.goal` | Int64 observable up-down counter | `By` | | api, mcp, reports, projector | same |
| `go.goroutine.count` | Int64 observable up-down counter | `{goroutine}` | | api, mcp, reports, projector | same |
| `go.processor.limit` | Int64 observable up-down counter | `{thread}` | | api, mcp, reports, projector | same |
| `go.config.gogc` | Int64 observable up-down counter | `%` | | api, mcp, reports, projector | same |

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
  outbox relay and the Kafka consumers create none.
- **Trace context crosses the outbox** ([ADR 0009](/docs/adr/0009-trace-context-crosses-the-outbox)):
  `postgres.OutboxRepo.Insert` injects the request span's W3C context
  (`traceparent`, and `tracestate`/`baggage` when present) into each
  `outbox_events.headers` row after `content-type`, and the relay copies the
  stored headers onto the Kafka message. A consumer that calls `Extract` on
  the message headers continues the producing request's trace. Rows written
  without an active span carry no
  `traceparent`; follow those by their CloudEvents `id` in the logs.

## Logs

All four binaries log JSON with `log/slog` to stdout (`time`, `level`, `msg`
plus fields); `LOG_LEVEL` sets the threshold. Logs carry no trace or span id.
In the kind cluster Alloy ships them to Loki with the label
`app=product-master` (from the `app.kubernetes.io/name` pod label).

| Message (`msg`) | Level | Fields | Binary | Meaning |
| --- | --- | --- | --- | --- |
| `postgres adapters configured` | INFO | | api | Migrations and ping succeeded. |
| `retrying` / `succeeded after retry` | WARN / INFO | `op`, `attempt`, `in`, `err` | all four | Boot retry of the migrations or the first database ping (`internal/bootretry`). |
| `kafka topics` | INFO | `brokers`, `produces`, `consumes`, `note` | api | Topics the service writes at boot; it never creates one (the broker does on the first write) and `/readyz` does not wait for them. |
| `outbox relay running` | INFO | `interval`, `max_attempts`, `brokers`, `topics` | api | Relay started (always, on Kafka). |
| `service exited with error` | ERROR | `error` | api | Boot failed, e.g. `invalid configuration: DATABASE_URL is required: this context runs on real Postgres`. Exit status 1, nothing listened. |
| `outbox relay pass failed` | ERROR | `error` (contains `outbox row <id> (<type>)`) | api | A send failed; the row keeps `last_error` and is retried until it reaches the attempt cap. |
| `outbox relay: row reached the attempt cap and is excluded from future claims; re-arm it with attempts = 0 after fixing the cause` | ERROR (once per row) | `outbox_id`, `topic`, `event_type`, `key`, `attempts`, `max_attempts`, `error` | api | The row failed `OUTBOX_MAX_ATTEMPTS` times and is no longer claimed ([ADR 0007](/docs/adr/0007-outbox-relay-attempt-cap)); re-arm it per the runbook. |
| `invalid OUTBOX_MAX_ATTEMPTS; using the default` | WARN | `value`, `default` | api | The env value was negative or not an integer; the cap is 20. |
| `analytics consumer handling failed; retrying the same message` (or `analytics consumer offset commit failed; retrying the same message`) | ERROR | `error`, `partition`, `offset`, `attempt`, `retry_in` | projector | Transient failure; the partition is blocked until it succeeds. |
| `analytics consumer running` | INFO | `topic`, `group_id`, `brokers` | projector | Consumer started. |
| `analytics: skipping message that is not a CloudEvents 1.0 event ...` | WARN | `topic`, `partition`, `offset`, `suppressed_since_last_warning` | projector | At most one line per minute. |
| `analytics: dead-lettering a message that can never be projected` | ERROR | `error`, `dlq_topic`, `partition`, `offset` | projector | Poison message sent to the DLQ. |
| `request failed` | ERROR | `error`, `method`, `path` | api | Unmapped error answered as `500 internal-error`. |
| `report query failed` | ERROR | `error`, `path` | reports | Analytical query failed (`500 report-store-error`). |
| `mcp tool failed with an unexpected error` | ERROR | `error` | mcp | Tool answered `internal-error`. |
| `shutdown: readiness flipped to not-ready; waiting for traffic to drain` | INFO | `drain_delay` | api | SIGTERM received. |
| `worker did not stop before the shutdown drain deadline` | WARN | `worker` (`outbox-relay`, the only worker since the legacy importer was removed) | api | The relay exceeded 10s at shutdown. |

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
| Outbox row stuck | any unpublished row with `attempts` above 10 | The relay stops at the first failing row, so one row blocks all later events until it reaches the attempt cap. |
| Outbox row excluded | any `outbox relay: row reached the attempt cap ...` ERROR line, or `SELECT count(*) FROM outbox_events WHERE published_at IS NULL AND attempts >= 20` above 0 | The row is never delivered until an operator re-arms it (runbook). |
| Pod not ready | `kube_pod_status_ready` false for an api pod while it is Running, or `GET /readyz` returning `503 dependency-unavailable` | Postgres or the Kafka broker is unreachable (the problem detail names which); `/healthz` stays green on purpose, so the pod is not restarted. |
| Analytics stale | `GET /reports/freshness` `lag_seconds` above your tolerance while writes happen | Projector down, blocked on a transient error, or relay not publishing. |
| DLQ not empty | messages on `warehouse.product-master.analytics.dlq`, or the log line `analytics: dead-lettering ...` | A payload the projector can never apply. |
| Consumer retry loop | repeated `handling failed; retrying the same message` | A partition is blocked (database down, timeouts). |
| REST latency | p95 of `http_server_request_duration_seconds` for `service_name="product-master"` | Pool exhaustion (10 connections) or slow Postgres (5s statement timeout). |
| Pod restarts | kube-state-metrics restarts of any component | Boot retry exhausted (database or analytical database unreachable). |

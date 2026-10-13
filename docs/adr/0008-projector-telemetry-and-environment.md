# ADR 0008: The analytics projector sets up telemetry; every backend Deployment sets ENVIRONMENT

## Status

Accepted (2026-10-13, ecosystem gap fix D21). Additive: no contract, table or
topic changes.

## Context

product-master ships four binaries. `cmd/api`, `cmd/mcp` and
`cmd/product-reports` call `telemetry.Setup`
(`internal/adapters/outbound/telemetry`): an OTLP/gRPC TracerProvider and
MeterProvider, the W3C trace-context + baggage propagator and Go runtime
metrics, with the resource attributes `service.name`, `service.version`
(`SERVICE_VERSION`) and `deployment.environment.name` (`ENVIRONMENT`).
`cmd/product-projector` ([ADR 0006](./0006-analytics-read-side.md)) did not:
it exported nothing, so the analytics writer was the one process of the
context invisible in the Collector.

The chart had the matching gap: the reports Deployment did not set
`ENVIRONMENT`, so in-cluster the reports binary reported `local`, and the
projector Deployment set none of the telemetry variables.

The fleet decision (gap-fix wave 2026-10-12, D21) is observability parity:
projectors and reports binaries that lack telemetry get `telemetry.Setup`
like their siblings, and reports Deployments get `ENVIRONMENT`.

## Decision

1. `cmd/product-projector` calls `telemetry.Setup` at boot, after the config
   is validated and before the analytical database is opened, with service
   name `product-master-projector`, `SERVICE_VERSION` (default `dev`) and
   `OTEL_EXPORTER_OTLP_ENDPOINT` (default `localhost:4317`); `ENVIRONMENT` is
   read by the telemetry adapter as in the other binaries. It logs
   `telemetry configured` and flushes the providers (5 s bound) after the
   consumer and the pool are closed. Export stays non-blocking: an
   unreachable Collector only drops telemetry.
2. The projector opens no spans and serves no `/metrics` because of this
   ADR; it gains Go runtime metrics, the resource identity and the
   propagator.
3. The chart sets `ENVIRONMENT` (from `environment`) and `SERVICE_VERSION` on
   every backend Deployment (api, mcp, projector, reports), and
   `OTEL_EXPORTER_OTLP_ENDPOINT` on all four when `otel.enabled`.

## Consequences

- The projector appears in the Collector as `product-master-projector` with
  version and environment, and reports Go runtime metrics every 30 s.
- The reports binary reports its real environment in-cluster.
- One more process exports to the Collector.
- Sensors: `TestSetupTelemetry_InstallsTheSDKProviders`
  (`cmd/product-projector`) and the D21 block of
  `charts/product-master/tests/render_test.sh` (CI job `chart-tests`).

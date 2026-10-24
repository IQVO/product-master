---
id: testing
title: Testing
sidebar_label: Testing
---

# Testing

The test pyramid as it exists in this repo, the `make` targets that run each
layer, and the CI jobs that gate a pull request. Sources: `Makefile`,
`.github/workflows/ci.yml`, `.gremlins.yaml`, `features/` (the godog runner
and the `.feature` files) and `internal/architecture/`.

## Layers

| Layer | What | Where | Run with | Needs |
| --- | --- | --- | --- | --- |
| Unit | Domain invariants, use cases (with fakes), encoders, CloudEvents helper, consumers with fake readers, HTTP handlers via `httptest`, MCP tools in memory, composition-root helpers, the report logic | 56 `*_test.go` files without a build tag, next to the code | `make test` (`go test ./... -race`) | nothing |
| BDD (godog) | Gherkin acceptance scenarios for every use case, run in process against the in-memory test doubles (never a binary's runtime path, ADR 0010) | `features/*.feature`, runner `features/features_test.go` (`TestFeatures`) | `make bdd` | nothing |
| Integration | Real Postgres and Kafka started by testcontainers (`postgres:16-alpine`, `confluentinc/confluent-local:7.6.1`) | 18 files with `//go:build integration` (16 suites and two helpers, `internal/adapters/outbound/postgres/helper_integration_test.go` and `internal/adapters/inbound/kafka/kafka_itest_helpers_test.go`) | `make integration` (`go test -tags=integration ./... -race -count=1 -timeout 300s`) | Docker only |
| Architecture fitness | Hexagonal layering and fleet rules, as Go tests | `internal/architecture/` | `make arch-test` | nothing |
| Mutation | gremlins on the domain | `.gremlins.yaml` | `make mutation`, `make mutation-full` | `gremlins` v0.6.0 |
| Contract | Spectral lint of both specs; the event catalogue fitness test; MCP tool registry golden file | `apis/`, `internal/architecture/catalogue_fitness_test.go`, `internal/adapters/inbound/mcp/testdata/tool_registry.golden.json` | CI `api-lint`; `make test` | Spectral for the lint |
| Frontend | vitest and Testing Library for the `productmaster_mfe` remote | 8 test files under `web/src` (incl. the Packaging screen: hierarchy, declare, retire, GTIN lookup) | `cd web && npm test` | Node 22 and a built sibling `warehouse-ui-kit` |
| Chart | Each Service selects exactly one Deployment | `charts/product-master/tests/test_service_selectors.py` | `python3 charts/product-master/tests/test_service_selectors.py` | Helm, PyYAML |
| Chart render | The api Deployment mounts the chart ConfigMap via `envFrom` (gap fix D14); `OUTBOX_MAX_ATTEMPTS` defaults to 20 and renders `0` ([ADR 0007](/docs/adr/0007-outbox-relay-attempt-cap)); mcp, projector and reports do not mount the ConfigMap; every backend Deployment sets `ENVIRONMENT`, `SERVICE_VERSION` and the OTLP endpoint ([ADR 0008](/docs/adr/0008-projector-telemetry-and-environment)) | `charts/product-master/tests/render_test.sh` | `bash charts/product-master/tests/render_test.sh` | Helm |

There is **no** Schemathesis (property-based HTTP contract) job and **no**
evals job in this repo's CI (`.github/workflows/ci.yml` on `develop`; the
Schemathesis `contract` job that facility-layout and inventory-storage run
against real service containers is not here). `EVALS.md` and `tools/harness_eval*.py` document
harness evaluations that were run against inventory-storage, not tests of this
service.

### BDD features

9 feature files, 66 scenarios (16 of them Scenario Outlines). The two
legacy-import feature files were deleted with the importer at stage E
(product-master #48).

| Feature file | Scenarios (of which outlines) |
| --- | --- |
| `features/register_product.feature` | 6 (1) |
| `features/classify_product.feature` | 6 (1) |
| `features/classification_rules.feature` | 6 (2) |
| `features/physical_profile.feature` | 8 (1) |
| `features/physical_profile_rules.feature` | 8 (4) |
| `features/list_products.feature` | 5 (1) |
| `features/list_products_rules.feature` | 3 (1) |
| `features/product_contract.feature` | 6 (3) |
| `features/packaging.feature` | 18 (2): the pack levels of ADR 0011 (GS1 immutability, GTIN uniqueness, check digit, indicator digit 9, the lower child level) |

### Integration tests

Every integration test boots its own containers; none reads `DATABASE_URL`,
`ANALYTICS_DATABASE_URL` or `KAFKA_BROKERS`, and none skips itself when they
are missing (`TestPostgresIntegrationTestsUseTestcontainers` and
`TestKafkaIntegrationTestsUseTestcontainers` enforce both).

The suites use three container shapes:

- **One Postgres per package, a template database, one clone per test.**
  `TestMain` starts the container, migrates a template database once, and
  each test gets a private database created `WITH TEMPLATE` from it, so tests
  are isolated and order-independent. Used by the REST, MCP Streamable HTTP,
  use-case wiring and `pgtx` suites.
- **One Kafka broker per package.** A `sync.Once` starts the broker on first
  use and `TestMain` terminates it; every test creates its own uniquely named
  topic. Used by the relay, the analytics consumer (its shared broker,
  `TestMain` and topic helpers live in `kafka_itest_helpers_test.go`) and the
  Kafka publisher suites.
- **One container per test.** A helper starts and migrates a fresh Postgres
  and terminates it in `t.Cleanup` (the repository suite's helper notes why:
  its tests assert on whole-table contents). Used by `internal/adapters/outbound/postgres`
  (`helper_integration_test.go`), the analytical store, `cmd/mcp`, the
  Postgres side of the relay and analytics-consumer suites,
  and both containers of the end-to-end suite.

| File | Tests | Covers |
| --- | --- | --- |
| `internal/adapters/outbound/postgres/product_repository_integration_test.go` (+ `helper_integration_test.go`) | `TestProductRepo_VersionGuard`, `TestProductRepo_RoundTripsTheWholeAggregate`, `TestProductRepo_ListPagesInByteOrderWithFilters`, `TestUseCasesCommitProductAndOutboxAtomically`, `TestProcessedEventRepo_ClaimsOnceAndRollsBack` | Version guard (`ErrConcurrentModification`), full aggregate round trip, list paging in byte order with filters, product and outbox committed atomically by the real use cases, processed-event claims rolled back with the transaction |
| `internal/adapters/outbound/postgres/pack_levels_integration_test.go` | `TestPackLevels_Postgres_RoundTrip`, `TestPackLevels_Postgres_VersionGuardCoversThePackLevels`, `TestPackLevels_Postgres_AGTINIsUniqueFleetWide`, `TestPackLevels_Postgres_SchemaRefusesWhatTheDomainRefuses`, `TestPackLevels_Postgres_UseCasesWriteOutboxRowsWithTheFullType` | The packaging hierarchy (ADR 0011) on real Postgres: the aggregate round trip with the version guard, the fleet-wide unique GTIN index (and its rollback), the table's CHECKs and partial unique index, and the outbox rows with the full CloudEvents type on the integration topic only |
| `internal/adapters/outbound/postgres/analytics_outbox_integration_test.go` | `TestFanout_Postgres_EachEventLandsOnBothTopicsUnderOneIDInOneTransaction`, `TestFanout_Postgres_AFailureWritesNeitherTopicsRows` | Fan-out: each event lands on both topics under one id in one transaction; a failure writes neither row |
| `internal/adapters/outbound/postgres/pgtx/pgtx_integration_test.go` | `TestPgtx_CommitsSaveAndOutboxAtomically`, `TestPgtx_ErrorInsideTheScopeRollsEverythingBack` | The transaction carried in the context by `pgtx`: a register inside `postgres.UnitOfWork` commits the product row and its outbox row together; an error inside the scope rolls both back; outside a unit of work the repositories fall back to the pool |
| `internal/application/usecases/wiring_integration_test.go` | `TestWiring_RegisterClassifyMeasureLifecycle`, `TestWiring_NoChangeEventPublishesNothing`, `TestWiring_UnknownSKUWriteIsRejected` | The write use cases wired like `cmd/api` (Postgres repositories, `UnitOfWork`, CloudEvents encoder into the outbox): register, re-register, classify and measure bump the version and enqueue one event each, in order; an identical re-register changes and publishes nothing; classifying an unknown SKU leaves no row and no event |
| `internal/adapters/outbound/kafka/publisher_integration_test.go` | `TestPublisher_RealKafka_CloudEventsEnvelopeAndPayload` | Events raised by the real `Product` aggregate, encoded by the production `Encoder` and sent through the `RelaySink` to a real broker: CloudEvents 1.0 envelope (`type`, `specversion`, `id`, `source`), the pinned payload, the SKU as message key, no duplicate ids |
| `internal/adapters/outbound/outbox/relay_integration_test.go` | `TestRelay_RealPostgresAndKafka_PublishesCloudEventsKeyedBySKU` | Relay draining Postgres into a real Kafka |
| `internal/adapters/outbound/outbox/relay_cap_integration_test.go` | `TestOutboxRelay_MaxAttempts_PoisonRowExcludedThenGoodRowPublishes`, `TestOutboxRelay_MaxAttempts_ReArmedRowIsClaimedAgain`, `TestOutboxRelay_NoCap_PoisonRowBlocksLaterRows` | Relay attempt cap over real Postgres: a poison row is excluded after N failures and logged once at ERROR, and the good row behind it (same key) publishes; the runbook's re-arm SQL; `0` = unlimited ([ADR 0007](/docs/adr/0007-outbox-relay-attempt-cap)) |
| `internal/adapters/outbound/outbox/outbox_trace_integration_test.go` | `TestOutboxTrace_TraceparentCrossesTheOutboxToKafka`, `TestOutboxTrace_RowWithoutTraceHeadersRelaysUnchanged` | Trace continuity over real Postgres and Kafka: the request span's `traceparent` is stored in `outbox_events.headers`, relayed by a pass on a background context and extracts to the request's trace and span; a row without stored headers relays unchanged with no headers ([ADR 0009](/docs/adr/0009-trace-context-crosses-the-outbox)) |
| `internal/adapters/inbound/kafka/analytics_consumer_integration_test.go` | `TestAnalyticsConsumer_RealKafkaAndPostgres` | Projector consumer against the analytical store |
| `internal/adapters/outbound/analyticsstore/postgres_integration_test.go` | `TestPostgres_Contract`, `TestPostgres_DedupeMarkAndEffectAreAtomic`, `TestPostgres_ReadOnlyPoolCannotWrite`, `TestPostgres_DayWindowsMatchTheGoDefinition` | Analytical migrations, projection and report queries (the same contract test runs against the in-memory store) |
| `internal/adapters/inbound/http/rest_integration_test.go` | `TestREST_ProductLifecycleEndToEnd`, `TestREST_ValidationAndDomainRejectionsAreProblemDocuments` | The real chi router over the real Postgres-backed use cases, through `httptest`: the PUT lifecycle (`201` then `200`), GET round trips including the list, and RFC 7807 problems (`product-not-found`, `malformed-request`, `invalid-description`, `no-handling-tags`, `temperature-class-not-applicable`, `stale-measurement`) that leave the aggregate unchanged |
| `internal/adapters/inbound/mcp/streamable_http_integration_test.go` | `TestStreamableHTTP_ListToolsExposesTheReadOnlyContract` (six read-only tools, two annotated writes), `TestStreamableHTTP_CallToolReadsWhatPostgresPersisted`, `TestStreamableHTTP_RejectionsAreToolErrorsNotTransportErrors` | The MCP handler on a real Streamable HTTP server, driven by the SDK client over data seeded through the write use cases: `tools/list` exposes the four tools, each with `readOnlyHint`; every tool reads what Postgres holds; `product-not-found`, `invalid-sku`, `product-classification-not-found` and `malformed-request` come back as tool errors, not transport errors |
| `cmd/mcp/main_integration_test.go` | `TestMCPAgainstPostgres_ReadsWhatTheAPIWroteAndNeverWrites`, `TestMCPWriteToolsAgainstPostgres` | `cmd/mcp` against a real database: reads what the API wrote and its read tools never write; the two pack level write tools enqueue exactly their events (integration topic only) and a duplicate GTIN is refused |
| `features/analytics_e2e_integration_test.go` | `TestAnalyticsReadSide_EndToEnd` | Write through the use cases, relay to Kafka, project, read the report |
| `cmd/api/e2e_integration_test.go` | `TestRunOnRealPostgresAndKafka` | The real `run()` booted in process on testcontainers Postgres and Kafka, driven only through REST (including the pack level routes of ADR 0011, whose events must reach the integration topic only): rows in Postgres, CloudEvents on `warehouse.product-master.events` and `.analytics`, replay idempotency, an in-process restart, and `/readyz` going green with no topic and `503` naming the dependency when Postgres or Kafka goes away ([ADR 0010](/docs/adr/0010-postgres-and-kafka-are-the-only-runtime)) |
| `internal/adapters/outbound/kafka/publisher_integration_test.go` | `TestBrokerCheck_RealKafka_NeedsNoTopicAndCreatesNone`, `TestBrokerCheck_ClosedPortIsNotReady` | The `/readyz` broker check against a real broker: passes with no topic and creates none; a closed port is not ready |

The CI `integration` job runs the same command as `make integration`
(`./...`), so every suite above runs on every push and pull request.

### Architecture fitness tests

| Test | Rule |
| --- | --- |
| `TestHexagonalArchitecture` | arch-go layering: domain imports nothing internal, adapters never import each other, only `cmd/*` wires them |
| `TestMCPAdapterDependencyRule` | the MCP adapter depends only on the application layer and the domain, and nothing depends on it |
| `TestNoAuthMiddlewareReintroduced` | no auth middleware on any router (REST and MCP are unauthenticated fleet-wide) |
| `TestRuntimeBinaryHasNoInMemoryAdapters` (+ `TestRuntimeBinarySensorBites`) | no non-test file under `cmd/**` imports `internal/adapters/outbound/memory`: the shipped binaries run on real Postgres and Kafka only ([ADR 0010](/docs/adr/0010-postgres-and-kafka-are-the-only-runtime)). The twin proves the detector flags a probe file. |
| `TestNoGoFilesAtRepoRoot` (+ `TestNoGoFilesAtRepoRootSensorBites`, `TestNoGoFilesAtRepoRootUnreadableRootFails`) | the repository root holds no Go file (the godog runner lives in `features/`) |
| `TestKafkaConsumerGroupNeverHardcodedInline` | a consumer `GroupID` never comes from an inline string literal |
| `TestKafkaIntegrationTestsUseTestcontainers`, `TestPostgresIntegrationTestsUseTestcontainers`, `TestPostgresIntegrationSensorFailsOnBadFixtures` | integration tests start their own containers and never gate on env vars |
| `TestNoEventEnvelopeToggleOrFlatEnvelope`, `TestCloudEventsOnly` | CloudEvents 1.0 built only through `internal/adapters/kafka/cloudevents`; no flat envelope, no toggle |
| `TestReplayConsumersSetCommitInterval` | consumer reader configuration rule for replay-style (unique group) consumers |
| `TestEventCatalogueMatchesContract`, `TestEventCatalogueDetector` | every type `apis/asyncapi.yaml` declares is listed in the CloudEvents ADR (0004) |
| `TestMCPOptionalToolInputsCarryOmitempty`, `TestMCPOptionalToolInputsSensorFailsOnBadFixtures` | every MCP tool input field whose `jsonschema` description calls it optional ("optional" or "omitted") carries `omitempty`, so the SDK does not mark it required (gap fix D16) |
| `TestOutboxRelaysPassAttemptCap`, `TestOutboxRelayCapSensorFailsOnBadFixtures` | every non-test file under `cmd/` that builds an outbox relay (`NewRelay`) passes `WithMaxAttempts(...)` ([ADR 0007](/docs/adr/0007-outbox-relay-attempt-cap)) |
| `TestKafkaWritersSetRequiredAcks`, `TestKafkaWriterAcksSensorBites` | Every kafka-go `Writer` composite literal in a non-test file under `internal/` or `cmd/` sets `RequiredAcks` (the import alias is resolved per file). kafka-go defaults it to `RequireNone` (acks=0): the broker never confirms the write, so a broker blip silently drops the message while `WriteMessages` returns nil. The twin proves the sensor flags a bad fixture under any alias. |
| `TestDeadLetterCallsGuardCancellation` (+ `TestDeadLetterShutdownSensorBites`, `kafka_dlq_shutdown_fitness_test.go`) | Every function in a non-test file under `internal/adapters/inbound/kafka` that sends to the dead-letter topic must reference `ctx.Err()` (any context-named value) or `context.Canceled` in its own body, or carry a `//dlq:deterministic <reason>` comment on the call line or the line above it. A handler that fails because the process is stopping must not dead-letter a good message and commit past it (network-fulfillment #89); a guard in the caller does not count. The sensor test proves the detector flags the pre-fix shape. |
| `TestKafkaWriterTopicNotSetTwice` (+ `TestKafkaWriterTopicSensorBites`, `kafka_writer_topic_fitness_test.go`) | kafka-go rejects every write whose `Message.Topic` is set when the `Writer` also has a `Topic`. Per Go package under `internal/` and `cmd/`: once any kafka-go `Message` literal sets `Topic`, every `Writer` literal in that package must leave `Topic` empty, unless it carries `//kafka:fixed-topic-writer <reason>` and no Topic-setting Message is provably written through it. Writers configured after construction and `WriterConfig` are not covered. (Escape: warehouse-planning's domain DLQ writer, where no dead letter ever landed.) |

### Mutation testing

`.gremlins.yaml` sets 1 worker, timeout coefficient 30, and thresholds
**efficacy 95%** and **mutant coverage 95%** (gremlins fails at or below the
threshold). The last measurement recorded there (2026-10-06, on
`internal/domain/product`): 100% efficacy, 100% mutant coverage, 66 killed, 0
lived.

- `make mutation` and the `mutation-fast` CI job: `./internal/domain/product`,
  on every push and pull request (blocking).
- `make mutation-full` and the `mutation` CI job: `./internal/domain`,
  scheduled weekly (Monday 06:00 UTC) and on manual dispatch.

### Coverage gate

`make coverage` and the `test` CI job measure coverage over
`./internal/domain/...`, `./internal/application/...` and
`./internal/analytics/...` and fail below **90%**.

The Makefile is the single source of the coverage scope and threshold
(gap 16d): the CI `test` job runs `make coverage` instead of its own
`go test -coverpkg=...` command, so the local and the PR gate cannot drift;
harness `repo_lint` rule R11 (coverage-scope) forbids a workflow passing
`-coverpkg` itself. Change the scope or the threshold in the Makefile only.

## Make targets

| Target | Runs |
| --- | --- |
| `make build` | `go build ./...` |
| `make vet` | `go vet ./...` |
| `make fmt` / `make fmt-check` | `gofmt -w .` / fail when `gofmt -l .` is non-empty |
| `make lint` | `golangci-lint run ./...` (v2.14.0 in CI) |
| `make test` | `go test ./... -race` |
| `make coverage` | test with `-coverprofile` and the 90% gate |
| `make integration` | `go test -tags=integration ./... -race -count=1` |
| `make bdd` | `go test ./... -run TestFeatures -v` |
| `make arch-test` | `go test ./internal/architecture/... -v` |
| `make mutation` / `make mutation-full` | gremlins on `./internal/domain/product` / `./internal/domain` |
| `make vuln` | `govulncheck ./...` |
| `make check` | `fmt-check vet build lint test` (also the lefthook pre-push hook) |
| `make check-all` | `check` + `coverage arch-test bdd` |
| `make check-fast` | `fmt-check vet arch-test` + tests of the Go packages changed vs `HEAD` (agent Stop hook) |
| `make guide-lint`, `make harness-test` | agent-guide lint and hook unit tests (Python) |

lefthook runs `fmt-check`, `vet` and `lint` before each commit and `make check`
before each push.

## CI jobs (`.github/workflows/ci.yml`)

| Job | Runs | When |
| --- | --- | --- |
| `lint` | golangci-lint v2.14.0 | every push and PR |
| `guide-lint` | `scripts/harness/guide_lint.py`, `scripts/harness/test_hook.py` (advisory, `continue-on-error`) | every push and PR |
| `complexity` | golangci-lint with only gocyclo, gocognit, cyclop, funlen, nestif; plus an informational gocyclo report | every push and PR |
| `test` | build, vet, then `make coverage` (unit tests with the Makefile's coverage scope and 90% gate) | every push and PR |
| `bdd` | `go test ./... -run TestFeatures -v` | every push and PR |
| `integration` | `go test -tags=integration ./... -race -count=1 -timeout 300s` | every push and PR |
| `mutation-fast` | gremlins on `./internal/domain/product` | every push and PR |
| `mutation` | gremlins on `./internal/domain`; opens or closes a `harness:red` issue | schedule and dispatch only |
| `drift` | deadcode, `go mod tidy -diff`, knip on `web/`, coverage-quality report (advisory) | schedule and dispatch only |
| `api-lint` | Spectral on `apis/openapi.yaml` and `apis/asyncapi.yaml` (`--fail-severity=warn`) | every push and PR |
| `vuln` | govulncheck | every push and PR |
| `docs-api-drift` | regenerate `docs/docs/api-reference/rest` from the OpenAPI spec and fail on any diff | every push and PR |
| `helm-lint` | `ct lint` on the chart and the Service selector test | pull requests |
| `chart-tests` | `charts/product-master/tests/render_test.sh` and the Service selector test | every push and PR |
| `web` | oxlint, `tsc -b`, vitest, production build of `web/` (with `warehouse-ui-kit` built first) | every push and PR |
| `arch-test` | `go test ./internal/architecture/... -v` | every push and PR |
| `trivy-scan` | build the image and scan it (SARIF upload; blocks on fixable CRITICAL/HIGH) | pull requests |
| `docker-publish` | build, push, cosign-sign and SBOM-attest the image | push to `main` only |
| `release` | tag, Helm package and push, GitHub release | push to `main` only, after `docker-publish` |

Other workflows: `docs.yml` builds the Docusaurus site on pull requests that
touch `docs/` or `apis/` and deploys GitHub Pages on push to `main`;
`selftest.yml` tests the harness scripts and fitness tests; `ai-review.yml`
posts an advisory architecture review on PRs into `develop` that touch the
domain, application layer, specs or chart (only when the API key secret is
configured).

## Writing a new test

- Domain rule: a table test in `internal/domain/product`, then check
  `make mutation` still kills every mutant.
- Use case: a test in `internal/application/usecases` with the fakes in
  `fakes_test.go`, and a scenario in the matching `features/*.feature`.
- Adapter touching Postgres or Kafka: an `_integration_test.go` file with
  `//go:build integration` that starts its own container through
  testcontainers (never `os.Getenv("DATABASE_URL")` plus `t.Skip`). For
  Postgres, the cheapest isolated shape is the `TestMain` plus template
  database of `internal/application/usecases/wiring_integration_test.go`.
- New event type: declare it in `apis/asyncapi.yaml` and list it in the
  CloudEvents type catalogue ADR (a `*cloudevents*.md` file under `docs/adr`),
  or `TestEventCatalogueMatchesContract` fails.

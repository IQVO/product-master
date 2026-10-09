---
id: testing
title: Testing
sidebar_label: Testing
---

# Testing

The test pyramid as it exists in this repo, the `make` targets that run each
layer, and the CI jobs that gate a pull request. Sources: `Makefile`,
`.github/workflows/ci.yml`, `.gremlins.yaml`, `features/`, `features_test.go`
and `internal/architecture/`.

## Layers

| Layer | What | Where | Run with | Needs |
| --- | --- | --- | --- | --- |
| Unit | Domain invariants, use cases (with fakes), encoders, CloudEvents helper, consumers with fake readers, HTTP handlers via `httptest`, MCP tools in memory, composition-root helpers, the report logic | 40 `*_test.go` files without a build tag, next to the code | `make test` (`go test ./... -race`) | nothing |
| BDD (godog) | Gherkin acceptance scenarios for every use case, run in process against the in-memory adapters | `features/*.feature`, runner `features_test.go` (`TestFeatures`) | `make bdd` | nothing |
| Integration | Real Postgres and Kafka started by testcontainers (`postgres:16-alpine`, `confluentinc/confluent-local:7.6.1`) | 9 files with `//go:build integration` | `make integration` (`go test -tags=integration ./... -race -count=1`) | Docker only |
| Architecture fitness | Hexagonal layering and fleet rules, as Go tests | `internal/architecture/` | `make arch-test` | nothing |
| Mutation | gremlins on the domain | `.gremlins.yaml` | `make mutation`, `make mutation-full` | `gremlins` v0.6.0 |
| Contract | Spectral lint of both specs; the event catalogue fitness test; MCP tool registry golden file | `apis/`, `internal/architecture/catalogue_fitness_test.go`, `internal/adapters/inbound/mcp/testdata/tool_registry.golden.json` | CI `api-lint`; `make test` | Spectral for the lint |
| Frontend | vitest and Testing Library for the `productmaster_mfe` remote | 7 test files under `web/src` | `cd web && npm test` | Node 22 and a built sibling `warehouse-ui-kit` |
| Chart | Each Service selects exactly one Deployment | `charts/product-master/tests/test_service_selectors.py` | `python3 charts/product-master/tests/test_service_selectors.py` | Helm, PyYAML |

There is **no** Schemathesis (property-based HTTP contract) job and **no**
evals job in this repo's CI. `EVALS.md` and `tools/harness_eval*.py` document
harness evaluations that were run against inventory-storage, not tests of this
service.

### BDD features

5 feature files, 31 scenarios (4 of them Scenario Outlines):

| Feature file | Scenarios |
| --- | --- |
| `features/register_product.feature` | 6 |
| `features/classify_product.feature` | 6 |
| `features/physical_profile.feature` | 8 |
| `features/list_products.feature` | 5 |
| `features/legacy_import.feature` | 6 |

### Integration tests

Every integration test boots its own containers; none reads `DATABASE_URL`,
`ANALYTICS_DATABASE_URL` or `KAFKA_BROKERS`, and none skips itself when they
are missing (`TestPostgresIntegrationTestsUseTestcontainers` and
`TestKafkaIntegrationTestsUseTestcontainers` enforce both).

| File | Covers |
| --- | --- |
| `internal/adapters/outbound/postgres/product_repository_integration_test.go` (+ `helper_integration_test.go`) | Version guard (`ErrConcurrentModification`), full aggregate round trip, list paging in byte order with filters, product and outbox committed atomically by the real use cases, processed-event claims rolled back with the transaction |
| `internal/adapters/outbound/postgres/analytics_outbox_integration_test.go` | Fan-out: each event lands on both topics under one id in one transaction; a failure writes neither row |
| `internal/adapters/outbound/outbox/relay_integration_test.go` | Relay draining Postgres into a real Kafka |
| `internal/adapters/inbound/kafka/legacy_importer_integration_test.go` | Legacy importer end to end (Kafka and Postgres) |
| `internal/adapters/inbound/kafka/analytics_consumer_integration_test.go` | Projector consumer against the analytical store |
| `internal/adapters/outbound/analyticsstore/postgres_integration_test.go` | Analytical migrations, projection and report queries (the same contract test runs against the in-memory store) |
| `cmd/mcp/main_integration_test.go` | `cmd/mcp` against a real database: reads what the API wrote and never writes |
| `analytics_e2e_integration_test.go` | Write through the use cases, relay to Kafka, project, read the report |

### Architecture fitness tests

| Test | Rule |
| --- | --- |
| `TestHexagonalArchitecture` | arch-go layering: domain imports nothing internal, adapters never import each other, only `cmd/*` wires them |
| `TestMCPAdapterDependencyRule` | the MCP adapter depends only on the application layer and the domain, and nothing depends on it |
| `TestNoAuthMiddlewareReintroduced` | no auth middleware on any router (REST and MCP are unauthenticated fleet-wide) |
| `TestKafkaConsumerGroupNeverHardcodedInline` | a consumer `GroupID` never comes from an inline string literal |
| `TestKafkaIntegrationTestsUseTestcontainers`, `TestPostgresIntegrationTestsUseTestcontainers`, `TestPostgresIntegrationSensorFailsOnBadFixtures` | integration tests start their own containers and never gate on env vars |
| `TestNoEventEnvelopeToggleOrFlatEnvelope`, `TestCloudEventsOnly` | CloudEvents 1.0 built only through `internal/adapters/kafka/cloudevents`; no flat envelope, no toggle |
| `TestReplayConsumersSetCommitInterval` | consumer reader configuration rule for replay-style (unique group) consumers |
| `TestEventCatalogueMatchesContract`, `TestEventCatalogueDetector` | every type `apis/asyncapi.yaml` declares is listed in the CloudEvents ADR (0004) |

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
| `test` | build, vet, unit tests with coverage, 90% gate | every push and PR |
| `bdd` | `go test ./... -run TestFeatures -v` | every push and PR |
| `integration` | `go test -tags=integration ./... -race -count=1` | every push and PR |
| `mutation-fast` | gremlins on `./internal/domain/product` | every push and PR |
| `mutation` | gremlins on `./internal/domain`; opens or closes a `harness:red` issue | schedule and dispatch only |
| `drift` | deadcode, `go mod tidy -diff`, knip on `web/`, coverage-quality report (advisory) | schedule and dispatch only |
| `api-lint` | Spectral on `apis/openapi.yaml` and `apis/asyncapi.yaml` (`--fail-severity=warn`) | every push and PR |
| `vuln` | govulncheck | every push and PR |
| `docs-api-drift` | regenerate `docs/docs/api-reference/rest` from the OpenAPI spec and fail on any diff | every push and PR |
| `helm-lint` | `ct lint` on the chart and the Service selector test | pull requests |
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
  testcontainers (never `os.Getenv("DATABASE_URL")` plus `t.Skip`).
- New event type: declare it in `apis/asyncapi.yaml` and list it in the
  CloudEvents type catalogue ADR (a `*cloudevents*.md` file under `docs/adr`),
  or `TestEventCatalogueMatchesContract` fails.

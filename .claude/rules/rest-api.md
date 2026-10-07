---
paths:
  - "internal/adapters/inbound/http/**"
  - "apis/openapi*.yaml"
  - "apis/openapi/**"
---

# REST API (inbound adapter)

Source of truth: `apis/openapi.yaml`. Keep this list in sync with it.

- `GET  /products?limit=&cursor=&handlingTag=&classified=` -> `ListProducts`
- `PUT  /products/{sku}`                      -> `RegisterProduct` (201 new / 200 updated or unchanged)
- `GET  /products/{sku}`                      -> `GetProduct`
- `PUT  /products/{sku}/classification`       -> `ClassifyProduct` (201 first / 200 replaced or unchanged)
- `GET  /products/{sku}/classification`       -> `GetProduct` (classification part)
- `PUT  /products/{sku}/dimensions/declared`  -> `DeclareDimensions` (200)
- `PUT  /products/{sku}/dimensions/measured`  -> `RecordMeasurement` (200; 409 `stale-measurement`)
- `GET  /products/{sku}/physical-profile`     -> `GetProduct` (physical part)
- `GET  /healthz`, `GET /readyz` (503 while draining), `GET /metrics`

## Conventions

- Errors: RFC 7807 `application/problem+json`,
  `type = https://errors.product-master.warehouse-systems.dev/<slug>`. One
  lookup table maps typed errors to (status, slug, title). Slugs:
  `malformed-request`, `invalid-sku`, `invalid-description`,
  `no-handling-tags`, `unknown-handling-tag`, `duplicate-handling-tag`,
  `temperature-class-required`, `temperature-class-not-applicable`,
  `unknown-temperature-class`, `invalid-dot-hazard-class`,
  `dot-hazard-class-not-applicable`, `invalid-dimension`, `invalid-weight`,
  `measured-at-in-future`, `product-not-found`,
  `product-classification-not-found`, `stale-measurement`,
  `concurrent-modification`, `internal-error`.
- Request bodies reject unknown fields (`DisallowUnknownFields`).
- Every write is an idempotent PUT; there is no resource-creating POST, so the
  fleet Idempotency-Key middleware does not apply. Do not add a POST without an ADR.
- Auth: none (fleet-wide revert 2026-09-11; `TestNoAuthMiddlewareReintroduced`).
- The classification request/response shapes match inventory-storage's former
  endpoint (plus `classificationSource` and `version`); keep them compatible.

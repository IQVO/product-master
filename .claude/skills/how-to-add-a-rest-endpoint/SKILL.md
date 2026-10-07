---
name: how-to-add-a-rest-endpoint
description: Add or change a REST endpoint in this service in the fleet's hexagonal order (domain invariant, use case, port, HTTP adapter, apis/openapi.yaml, generated docs, godog scenario). Use when touching internal/adapters/inbound/http, apis/openapi.yaml, or exposing a use case over HTTP.
---

# How to add a REST endpoint

Use when asked to add a new REST use case/endpoint to this service. Follow
this order — domain first, adapter last — never the reverse; writing the
HTTP handler before the domain invariant it enforces produces handlers
that validate nothing and use cases that get bypassed.

This walks the exact path `PUT /products/{sku}/classification` took
(`internal/application/usecases/classify_product.go` +
`internal/adapters/inbound/http/handlers.go`'s `handleClassifyProduct`) as the
concrete worked example — read those two files alongside this guide.

## 1. Domain first: does an invariant already exist, or do you need one?

Check `internal/domain/product/` for the rule this endpoint enforces.
A REST endpoint should almost never contain business logic itself — it
decodes a request, calls a use case, encodes the result. If the operation
needs a new domain rule (e.g. "a temperature class only with TemperatureSensitive"),
add it to the aggregate/value-object in `internal/domain/`, with its own
table-driven unit test, BEFORE touching the application or adapter layers.

## 2. Application: define the use case

Add a new file in `internal/application/usecases/` (one file per use
case, this repo's convention — not one giant `usecases.go`). Shape:

```go
package usecases

// <Verb><Noun> — one sentence: what business capability this represents,
// and the domain rule it enforces (mirror ClassifyProduct's doc comment).
type <Verb><Noun> struct {
    Writer // Products, Outbox, Encoder, UoW, Clock — driven ports only
}

func (uc *<Verb><Noun>) Handle(ctx context.Context, /* domain-typed args */) (<Verb><Noun>Result, error) {
    // Inside ONE uc.UoW.Do (writer.go's change/persist helpers do this):
    // 1. Products.Get the aggregate
    // 2. call the aggregate's own method (never inline the invariant here —
    //    it belongs in internal/domain/product)
    // 3. no events -> return success, save nothing, bump nothing
    // 4. Products.Save(ctx, p, loadedVersion) — version-guarded
    // 5. Encoder.Encode each event and Outbox.Insert it (transactional outbox)
}
```

Add the port to `internal/application/ports/` if it doesn't exist yet —
ports are interfaces ONLY (`TestPortsAreCustomerOwned`/
`TestApplicationPortsContainOnlyInterfaces`-style fitness tests in
`internal/architecture/` enforce this; a struct or function in a ports
package fails CI).

Write the use case's unit test against the fakes in
`internal/application/usecases/fakes_test.go` (or the in-memory adapters in
`internal/adapters/outbound/memory/`) — never a real Postgres/HTTP call
in a unit test. Cover the success path AND the domain-rule failure path.

## 3. Adapter: wire the HTTP handler

In `internal/adapters/inbound/http/`:

1. `internal/adapters/inbound/http/dto.go` — add the request/response DTO structs (JSON tags, this repo's
   naming convention: `<noun>Request`/`<noun>Response`, e.g. `classificationRequest`).
   DTOs live ONLY in the adapter layer — domain types never carry JSON
   tags.
2. `internal/adapters/inbound/http/server.go` — add the route (`r.Put("/products/{sku}/...", s.handle<Name>)`
   in `NewRouter`), and the handler in `internal/adapters/inbound/http/handlers.go`:
   - decode the request strictly (`decodeJSON`: DisallowUnknownFields,
     64 KB cap), parse the SKU with `skuParam` — a bad value fails here as
     an RFC 7807 problem, never reaches the use case
   - call the use case's `Handle`
   - map use-case errors to HTTP status via `writeError` (check
     `problemFor` in `internal/adapters/inbound/http/errors.go` and the slug
     list in `.claude/rules/rest-api.md` before adding a new problem type)
   - encode the domain result back to the response DTO and `writeJSON`
3. Add the new use case field to the `Server` struct and wire it in
   the composition root (`cmd/api/main.go`).

Write at least one httptest per endpoint: one success path, one error
path (validation failure AND/OR the domain-rule failure, whichever this
endpoint can produce).

## 4. Contract: update OpenAPI, then regenerate docs

Add the path to `apis/openapi.yaml` (request/response schemas, the RFC
7807 problem-detail response for each error case — see the existing
`/products/{sku}/classification` entry for the shape), then lint it:

```bash
spectral lint apis/openapi.yaml
```

This repo has no Docusaurus site yet, so there is no generated REST
reference to refresh.

## 5. Behaviour: add a godog scenario

If this endpoint is user-facing behaviour (not purely internal
plumbing), add a `.feature` file under `features/` exercising it
end-to-end against the real HTTP server — see `features/classify_product.feature`
and the step definitions in `features_test.go` for the exact shape this
repo's `bdd` CI job expects (Given/When/Then over real HTTP, not mocked).

## 6. Verify before opening the PR

```bash
make check       # fmt-check vet build lint test
make check-all    # + coverage (90% gate) + arch-test + bdd
```

`make coverage` gates `./internal/domain/...,./internal/application/...`
at 90% — a new use case with no test on its failure path is the most
common way to miss this gate.

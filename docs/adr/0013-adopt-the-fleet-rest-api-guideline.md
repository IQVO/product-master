# ADR 0013: Adopt the fleet REST API guideline

## Status

Accepted (2026-11-01); breaking by design. Part of the fleet-wide REST
cutover set (nouns not verbs, every POST/PATCH idempotent, 409 key reuse,
`Idempotency-Replayed`, keyset paging), enforced by `.spectral.yaml` plus
`.spectral/functions/restGuideline.js` in the `api-lint` job. Amends
[ADR 0011](./0011-packaging-hierarchy-with-gs1-gtin.md) (the retire route)
and supersedes its statement that this service has no `Idempotency-Key`.

## Context

The fleet standard (rules R1-R8) forbids `:custom-method` URLs and verb path
segments, requires an `Idempotency-Key` on every POST and PATCH (400 when
missing or malformed, 409 `idempotency-key-reused` when reused with another
request, replay marked `Idempotency-Replayed: true`), and requires every
collection GET to be keyset-paged as `{items,nextCursor}`. Hard rule 10 of this
repo (every write is an idempotent PUT, no key middleware) stays true for
every PUT; only the retirement is a POST.

## Decision

1. **Route.** `POST /products/{sku}/pack-levels/{level}:retire` becomes
   `POST /products/{sku}/pack-levels/{level}/retirements` (transition noun,
   operationId `retirePackLevel` unchanged). Status codes and bodies do not
   change; the old route is gone (no alias; `TestRetiredRoutesAreGone`).
2. **Idempotency.** The route sits behind a route-scoped middleware
   (`internal/adapters/inbound/http/idempotency.go`). Fingerprint = method +
   path + SHA-256(body). The key is global (looked up by key alone). The key
   row, the aggregate change and the outbox rows commit in ONE Postgres
   transaction (`postgres.IdempotencyStore` hands the use case a ctx carrying
   the transaction, so `UnitOfWork.Do` joins it). 2xx and 4xx answers are
   stored and replayed; a 5xx or a panic rolls back and is never stored.
   Concurrent same-key requests serialise on the primary key; the loser
   replays. Rows older than 7 days are pruned on the next fresh key
   (retention is at least 24 h). The port is `ports.IdempotencyStore`; the
   in-memory double (`memory.IdempotencyStore`) serves unit and BDD tests only
   (ADR 0010). The header is required in every runtime mode.
3. **Pagination.** `GET /products` keeps its SKU order but now speaks the
   fleet cursor (`page` package: base64url `{s,i}`), `limit` 1-500 and the
   problems `invalid-limit`, `invalid-cursor`, `unknown-query-parameter` (the
   retired `after`/`offset`/`nextToken` fail loudly). `GET
   /products/{sku}/pack-levels` is now paged too, ordered (level rank,
   `effectiveFrom`, GTIN); the GTIN is unique across every definition ever
   (never reassigned), so rows sharing a level and instant never skip or
   repeat. The pack levels live inside the `Product` aggregate, so that page is
   cut in memory from the loaded aggregate; there is no SQL keyset for it.
4. **Exemption.** `GET /products/{sku}/classification` is
   `x-pagination-exempt: view`: it returns ONE classification record whose
   `handlingTags` is a closed set of at most eight tags, not a collection.
5. **MCP and A2A** keep their own tool schemas and call the use cases
   directly; they are unaffected (`list_products` keeps its use-case cursor).

## Consumers changed

The web remote (`web/src/api.ts`, `RetirePackLevelPanel.tsx`): new route,
a fresh `Idempotency-Key` per user submit (reused on retry), `nextCursor`
followed for pack levels. No sibling context calls this REST API (ADR 0001).

## Consequences

Breaking: deploy together with the REST cutover set. A client that sent no key
to the retire route, called `:retire`, or passed `after` now gets 400/404/405.
The OpenAPI `IdempotencyKey` parameter is `required: true`.

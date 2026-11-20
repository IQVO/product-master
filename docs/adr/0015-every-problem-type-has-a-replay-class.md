---
id: 0015
title: Every problem type has a replay class; a RETRYABLE refusal is not stored
status: Accepted (2026-11-20)
---

# ADR 0015: Every problem type has a replay class; a RETRYABLE refusal is not stored

Date: 2026-11-20 · Fleet decision (owner, 2026-11-18). Standard:
`warehouse-harness-template` `.claude/rules/fleet/idempotency-and-outbox.md`
and `docs/idempotency-replay-classes.md`. Amends item 2 of ADR 0013 (a dated
pointer is added there; its Decision text is unchanged).

## Context

ADR 0013 put the one `POST` (`/products/{sku}/pack-levels/{level}/retirements`)
behind the `Idempotency-Key` middleware and stored the first answer below 500 —
a 4xx too — for 7 days. Most refusals of this service do not depend on the
request alone: the product of the path is registered by another request, the
pack level may be declared a moment later by the `declare_pack_level` tool, and
a lost optimistic-lock race is a coincidence of timing. A caller that did the
right thing and retried the same key got the stale refusal replayed for 7 days,
exactly the case the key exists for.

## Decision

Each problem type the service can answer on an idempotent route declares exactly
one replay class on its catalogue entry (`problemCatalogue`,
`internal/adapters/inbound/http/errors.go`), next to the status and the slug:

- **FINAL** — the answer is a function of the request alone (or of state that
  only ever moves one way). Stored and replayed.
- **OUTCOME** — the command committed state and then refused; the writes commit
  with the stored answer. **No problem type of this service is one**: every
  write use case refuses before it persists (`usecases/writer.go`: `change`
  returns `apply`'s error before any `Save`, and a command that changes nothing
  raises no event and saves nothing). The store could hold one — a 4xx whose
  handler wrote successfully releases its savepoint and commits the writes with
  the key row (`postgres/idempotency_store.go`) — no current refusal does it.
- **RETRYABLE** — nothing committed and the answer depends on state that can
  change. NOT stored: the run func answers `idempotency.ErrNotStored`, the
  store's rollback drops the key row and the handler's writes (the same
  rollback the 5xx branch always did), the key is released and a retry
  executes again. A different body under a released key is a NEW request, not
  `idempotency-key-reused`.

A 2xx is stored; a 5xx or a panic never is, whatever the class. The middleware
knows no other status range and holds no slug list: `writeError` marks the
capturing writer with the class (`markReplay`), and the capture's
`releasesKey()` decides from the status family (2xx stored, 5xx never) and the
class alone. A runtime refusal with no class (an unmapped error is the
internal-error 500; a handler that wrote a bare status) is released like
RETRYABLE — the fail-safe direction — and
`TestEveryProblemTypeDeclaresAReplayClass` fails the build on an unclassified
catalogue entry. A replay carries `Idempotency-Replayed: true`; the 7-day
retention (rows pruned on the next fresh key) is unchanged.

What a concurrent duplicate sees when the first request ends RETRYABLE: it
blocks on the key's unique index until the first transaction ends; the rollback
means the row never existed, so the blocked claim inserts it and runs the
handler itself (it never replays the released refusal and never runs alongside
the first); copies queued behind it replay that run's answer.

Outside the replay decision (never stored under a key, classified nowhere):
`idempotency-key-required` / `idempotency-key-invalid` (answered before the
store is entered) and `idempotency-key-reused` (a property of the replay
comparison, not an answer a key stores). `internal-error` is a 5xx and never
stored. No writer bypasses `writeError` for a typed problem: the handlers write
through `writeError`/`writeProblem` only, and `problem+json` appears in no
other handler.

## Class table

| Problem type | Class | Status | Reason |
| --- | --- | --- | --- |
| `malformed-request` | FINAL | 400 | the request body alone (`decodeJSON`, `response.go`; the list query, `queries.go`; the measurement body, `physical.go` `NewMeasurement`) |
| `invalid-limit` | FINAL | 400 | GET only (a collection query parameter); classified for completeness |
| `invalid-cursor` | FINAL | 400 | GET only |
| `unknown-query-parameter` | FINAL | 400 | GET only |
| `invalid-sku` | FINAL | 400 | the SKU of the path alone (`usecases` `parseSKU`) |
| `invalid-description` | FINAL | 400 | the body alone (`RegisterProduct`) |
| `no-handling-tags` | FINAL | 400 | the classification body alone |
| `unknown-handling-tag` | FINAL | 400 | the classification body alone |
| `duplicate-handling-tag` | FINAL | 400 | the classification body alone |
| `temperature-class-required` | FINAL | 400 | the classification body alone |
| `temperature-class-not-applicable` | FINAL | 400 | the classification body alone |
| `unknown-temperature-class` | FINAL | 400 | the classification body alone |
| `invalid-dot-hazard-class` | FINAL | 400 | the classification body alone |
| `dot-hazard-class-not-applicable` | FINAL | 400 | the classification body alone |
| `invalid-dimension` | FINAL | 400 | the dimensions of the body alone |
| `invalid-weight` | FINAL | 400 | the weight of the body alone |
| `measured-at-in-future` | RETRYABLE | 400 | `measuredAt` is compared with the service clock (`physical.go:88`), which moves: a reading dated seconds in the future is valid on retry |
| `invalid-pack-level` | FINAL | 400 | the level of the path alone (`ParsePackLevel`) |
| `variable-measure-unsupported` | FINAL | 400 | the GTIN of the body alone (its indicator digit) |
| `invalid-gtin` | FINAL | 400 | the GTIN of the body alone (its check digit) |
| `invalid-pack-quantity` | FINAL | 400 | the pack quantity fields of the body alone |
| `invalid-reason` | FINAL | 400 | the reason of the retirement body alone |
| `product-not-found` | RETRYABLE | 404 | the product of the path is registered by another request (`RegisterProduct`), so it can exist a moment later |
| `gtin-not-found` | RETRYABLE | 404 | the GTIN's pack level is declared by another request or the `declare_pack_level` tool |
| `pack-level-not-found` | RETRYABLE | 404 | the level is declared by another request or the `declare_pack_level` tool (the BDD scenario of the keyed retirement POST proves the retry under the same key) |
| `product-classification-not-found` | RETRYABLE | 404 | the classification arrives by another request (`ClassifyProduct`) or event |
| `stale-measurement` | FINAL | 409 | one-way state: the current reading's `measuredAt` only moves forward (`RecordMeasurement` refuses older, corrections keep `measuredAt`; `product.go:130-137`), so the same request can never stop being stale |
| `gtin-already-assigned` | FINAL | 409 | one-way state: a GTIN is never freed — retirement keeps the definition resolvable by its GTIN (`usecases/pack_levels.go:150-152`) and redeclaring a retired definition is refused as assigned too (`packaging_declare.go:63-70`); the cross-SKU unique index says the same (`pack_level_repository.go:254`) |
| `pack-quantity-immutable` | FINAL | 409 | one-way state: the child/quantity of an active definition never changes (that is this very guard, `packaging_declare.go:71-72`); retiring the level turns a redeclare into `gtin-already-assigned`, never into success |
| `pack-level-order` | RETRYABLE | 409 | mixed slug: a missing `childGtin` is the request alone (`packaging_declare.go:59-60`) but a child level not active yet is state another request clears (104-105); splitting is a recorded follow-up |
| `pack-dimensions-below-contents` | RETRYABLE | 409 | the pack weight and the child's weight both move (declared dimensions, newer measurements; `packaging_declare.go:159`) |
| `concurrent-modification` | RETRYABLE | 409 | the optimistic-lock guard of the version-guarded `Save` (`writer.go` `persist`) |

## Judgement calls

1. **`stale-measurement` is FINAL although it reads state**: the current
   reading's `measuredAt` is provably monotonic (`RecordMeasurement` refuses
   anything older; a correction replaces a reading of the SAME `measuredAt`),
   so a retry of the same body can never pass the guard and replaying the
   refusal is safe — a re-execution would answer the same refusal anyway.
2. **`gtin-already-assigned` is FINAL although it reads state**: the fleet
   default says RETRYABLE when unsure, but here the one-way direction is
   provable from three sides (retirement keeps the definition resolvable,
   redeclaring a retired definition is refused as assigned, and the unique
   index never frees a GTIN). There is no unassign.
3. **`pack-quantity-immutable` is FINAL although it reads state**: the guarded
   values (child, quantity) are immutable while the definition is active (this
   error is the guard), and the only state change available (retirement)
   redirects the same request to `gtin-already-assigned`, never to success.
4. **`measured-at-in-future` is RETRYABLE although it looks like validation**:
   the comparison is against the service clock, not the request alone, and a
   reading dated seconds in the future becomes valid as the clock moves; unsure
   defaults to RETRYABLE.
5. **`pack-level-order` is RETRYABLE although one of its two causes is
   request-only** (a missing `childGtin`): one class per slug, and the other
   cause (a child level not active yet) is cleared by another request's
   declare; splitting the slug is a recorded follow-up, not part of this PR.
6. **No OUTCOME exists here while the store could hold one**: a 4xx whose
   handler wrote successfully commits its writes with the key row (the
   savepoint is released, not rolled back), but every write use case refuses
   before persisting, so no refusal provably commits; the class stays on the
   books and the middleware honours it (proven with a synthetic entry in
   `TestAnOutcomeMarkedRefusalIsStoredAndReplayed`).

## Consequences

- A caller that retries a RETRYABLE refusal under the same key gets a fresh
  execution, not the stale refusal; nothing is added client-side (automation
  retries; people-facing surfaces must surface the refusal — a separate PR).
- The BDD scenario that asserted "a refused request is stored too" now asserts
  the release and the successful retry under the same key.
- `TestTheADRDeclaresTheClassOfEveryProblem` holds this table equal to the
  catalogue; a new problem type without a class fails
  `TestEveryProblemTypeDeclaresAReplayClass` and must extend both.
- Follow-ups recorded: split `pack-level-order` into a request-only
  (`child-gtin-required`) and a state-dependent (`child-level-not-active`)
  problem type; nothing else in this PR.

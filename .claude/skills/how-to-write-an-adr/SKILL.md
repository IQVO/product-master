---
name: how-to-write-an-adr
description: Write an Architecture Decision Record in this repo's numbering and format, including companion ADRs for cross-repo changes. Use when a design decision should be recorded or a change contradicts an existing ADR.
---

# How to write an ADR

Use when a change is architecturally significant — a new bounded-context
integration, a reversal of a prior decision, a cross-repo contract change,
or anything a future reader would otherwise have to reverse-engineer from
the diff. Not every change needs one: a bug fix or a routine feature
addition inside an already-decided architecture doesn't.

## Numbering and location

`docs/adr/NNNN-kebab-case-title.md`, four-digit zero-padded, sequential —
check the highest existing number
(`git ls-tree --name-only origin/develop -- docs/adr/`) and pick the next
integer, never reuse or guess. This repo has no Docusaurus site yet, so an
ADR is a plain Markdown file with no frontmatter.

## Format: Michael Nygard's template, as this repo writes it

```markdown
# ADR NNNN: Title (a short noun phrase)

## Status

Accepted (YYYY-MM-DD). | Proposed | Superseded by ADR XXXX

## Context
The forces at play — technical, business, constraints — that make this
decision necessary. Write in the past tense, as if explaining to someone
who wasn't there. State the alternatives seriously considered, not just
the one chosen.

## Decision
What was actually decided, stated as an active, present-tense
declaration ("we will...", not "we might..."). Be specific about the
mechanism, not just the intent — this section should let a reader
implement the same decision from scratch without asking follow-up
questions.

## Consequences
What becomes easier, what becomes harder, and what future work this
creates or forecloses. Be honest about the downsides — an ADR that only
lists benefits reads as marketing, not a decision record.
```

The `## Decision` section is the part worth the most editing effort: see
`docs/adr/0003-migration-from-inventory-storage.md` for a model example — it
states the exact mechanism (a staged strangler migration over event-carried
state transfer), names the consumer group env var, the dedupe and the
"never overwrite a native classification" rule, and is specific enough that
`internal/adapters/inbound/kafka/legacy_importer.go` was built straight
from it.

## Superseding an earlier ADR

Don't edit the old ADR's Decision section. Change the OLD ADR's
`## Status` to `Superseded by ADR XXXX` (a one-line patch), and open the
new ADR stating in its own Status section which ADR it supersedes.

## Cross-repo decisions: use a companion ADR, not one repo's private opinion

When a decision genuinely spans bounded-context repos, write ONE ADR per
repo, each referencing the others explicitly as companion ADRs with a
one-line description of the split of responsibility — see the Status
section of `docs/adr/0003-migration-from-inventory-storage.md`, which names
inventory-storage's hand-over ADR and the consumer-side ADRs in
order-management, wes-work-planning and fulfillment-execution. Don't write
the decision once in one repo and expect the other repo's readers to find
it; each bounded context's docs are read independently.

## After writing

If the ADR changes the published or consumed event types, update the
catalogue in `docs/adr/0004-cloudevents-envelope-and-type-catalogue.md`
together with `apis/asyncapi.yaml`: `TestEventCatalogueMatchesContract`
(`make arch-test`) fails if they drift. Run `make guide-lint` if the ADR
renames anything a rule or skill cites.

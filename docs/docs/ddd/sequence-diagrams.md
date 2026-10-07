---
id: sequence-diagrams
title: Sequence diagrams
sidebar_label: Sequence diagrams
sidebar_position: 20
---

# Sequence diagrams

UML sequence diagrams of every command use case, derived from the use-case
bodies in `internal/application/usecases`, the inbound adapters and the
Postgres adapters. Every write runs in **one** unit of work (one Postgres
transaction): load, aggregate command, version-guarded save, encode, outbox
insert (`writer.go`). There is no `Idempotency-Key` middleware: every write is
an idempotent `PUT`.

## 1. Register a product (REST)

```mermaid
sequenceDiagram
  autonumber
  actor Client
  participant H as HTTP handler
  participant UC as RegisterProduct
  participant U as UnitOfWork
  participant R as ProductRepository
  participant P as Product
  participant E as EventEncoder
  participant O as OutboxRepository
  Client->>H: PUT /products/SKU-1 with description
  H->>H: unescape sku, decode JSON, unknown fields rejected
  H->>UC: Handle(sku, description)
  UC->>UC: NewSKU
  alt invalid SKU
    UC-->>H: ErrInvalidSKU
    H-->>Client: 400 invalid-sku
  end
  UC->>U: Do
  U->>R: Get(sku)
  alt not found
    UC->>P: Register(sku, description, now)
    Note over P: version 1, ProductRegistered
  else found at version v
    UC->>P: ChangeDescription(description, now)
    Note over P: same text means no event, version stays v
  end
  alt invalid description
    P-->>UC: ErrInvalidDescription, rollback
    H-->>Client: 400 invalid-description
  end
  opt an event was raised
    UC->>R: Save(product, loaded version)
    alt another writer won or the SKU was inserted concurrently
      R-->>UC: ErrConcurrentModification, rollback
      H-->>Client: 409 concurrent-modification
    end
    UC->>E: Encode(events), mint CloudEvents id
    UC->>O: Insert(messages)
  end
  U-->>UC: commit
  H-->>Client: 201 when registered, 200 otherwise, with the product
```

Source: `internal/application/usecases/register_product.go`, `writer.go`,
`internal/adapters/inbound/http/handlers.go` (`handleRegisterProduct`),
`internal/adapters/outbound/postgres/product_repository.go` (`Save`).
Omits: `400 malformed-request` for a bad body and the telemetry middleware.

## 2. Classify a product (REST)

```mermaid
sequenceDiagram
  autonumber
  actor Client
  participant H as HTTP handler
  participant UC as ClassifyProduct
  participant U as UnitOfWork
  participant R as ProductRepository
  participant P as Product
  participant O as Encoder and Outbox
  Client->>H: PUT /products/SKU-1/classification handlingTags, temperatureClass, dotHazardClass
  alt dotHazardClass sent as 0
    H-->>Client: 400 invalid-dot-hazard-class
  end
  H->>UC: Handle(command)
  UC->>UC: NewSKU, NewClassification before any load
  alt broken taxonomy rule
    UC-->>H: ErrNoHandlingTags, ErrUnknownHandlingTag, ErrDuplicateHandlingTag, temperature or DOT error
    H-->>Client: 400 with the rule's slug
  end
  UC->>U: Do
  U->>R: Get(sku)
  alt not registered
    R-->>UC: ErrProductNotFound
    H-->>Client: 404 product-not-found
  end
  UC->>P: Classify(c, now)
  Note over P: same classification already native means no event
  opt ProductClassified raised, version plus 1
    UC->>R: Save(product, loaded version)
    UC->>O: Encode and Insert
  end
  U-->>UC: commit
  H-->>Client: 201 if it had no classification before, else 200, sku, classification, version
```

Source: `internal/application/usecases/classify_product.go`, `writer.go`
(`change`), `internal/adapters/inbound/http/handlers.go`
(`handleClassifyProduct`), `internal/domain/product/classification.go`,
`product.go` (`setClassification`).
Omits: the `409 concurrent-modification` branch on `Save` (same as diagram 1).

## 3. Declare dimensions and record a measurement (REST)

```mermaid
sequenceDiagram
  autonumber
  actor Client
  participant H as HTTP handler
  participant UC as DeclareDimensions or RecordMeasurement
  participant C as Clock
  participant U as UnitOfWork
  participant R as ProductRepository
  participant P as Product
  participant O as Encoder and Outbox
  Client->>H: PUT /products/SKU-1/dimensions/declared or /measured
  alt lengthMm, widthMm, heightMm or weightG missing
    H-->>Client: 400 malformed-request
  end
  H->>UC: Handle(command)
  UC->>UC: NewSKU, NewUnitDimensions
  alt out of bounds
    H-->>Client: 400 invalid-dimension or invalid-weight
  end
  opt RecordMeasurement
    UC->>C: Now()
    UC->>UC: NewMeasurement(dimensions, measuredAt, deviceId, now)
    alt measuredAt missing, in the future, or bad deviceId
      H-->>Client: 400 malformed-request or measured-at-in-future
    end
  end
  UC->>U: Do
  U->>R: Get(sku)
  alt not registered
    H-->>Client: 404 product-not-found
  end
  alt declared
    UC->>P: DeclareDimensions(d, now)
    Note over P: same values means no event
  else measured
    UC->>P: RecordMeasurement(m, now)
    alt measuredAt before the current measurement
      P-->>UC: ErrStaleMeasurement, rollback
      H-->>Client: 409 stale-measurement
    end
    Note over P: the identical reading means no event
  end
  opt ProductDimensionsDeclared or ProductMeasured raised
    UC->>R: Save(product, loaded version)
    UC->>O: Encode the full profile and Insert
  end
  U-->>UC: commit
  H-->>Client: 200 physical profile with effective, effectiveSource, discrepancy, version
```

Source: `internal/application/usecases/physical_profile.go`, `writer.go`,
`internal/adapters/inbound/http/handlers.go` (`dimensions`,
`handleDeclareDimensions`, `handleRecordMeasurement`),
`internal/domain/product/physical.go`, `product.go`.
Omits: the `409 concurrent-modification` branch on `Save`.

## 4. Legacy import from inventory-storage (Kafka, migration only)

```mermaid
sequenceDiagram
  autonumber
  participant K as Kafka warehouse.inventory.events
  participant L as LegacyImporter
  participant UC as ImportLegacyClassification
  participant U as UnitOfWork
  participant PE as ProcessedEvents
  participant R as ProductRepository
  participant P as Product
  participant O as Encoder and Outbox
  K->>L: FetchMessage
  L->>L: cloudevents.Decode, match the full type, DataAs
  alt not a CloudEvent or malformed payload
    L->>K: CommitMessages, skipped with a WARN
  else another type on the topic
    L->>K: CommitMessages, ignored
  else legacy ProductClassified
    L->>UC: Handle(event id, sku, tags, temperature class, DOT class)
    UC->>UC: NewSKU, NewClassification
    alt invalid
      UC-->>L: ErrInvalidLegacyImport
      L->>K: CommitMessages, skipped with a WARN
    end
    UC->>U: Do
    U->>PE: Claim(legacy-classification-importer, event id)
    alt already claimed
      Note over UC: outcome duplicate, nothing else happens
    else first time
      U->>R: Get(sku)
      opt not found
        UC->>P: Register(sku, empty description, now)
      end
      UC->>P: ImportLegacyClassification(c, now)
      Note over P: native classification or identical legacy one means no event
      opt events raised
        UC->>R: Save(product, loaded version or 0)
        UC->>O: Encode and Insert ProductRegistered and or ProductClassified
      end
    end
    U-->>UC: commit
    L->>K: CommitMessages
  end
  opt transient failure such as claim, get, save or commit
    Note over L: rollback, retry the same message, backoff 200ms doubling to 5s, until it succeeds
  end
```

Source: `internal/adapters/inbound/kafka/legacy_importer.go` (`HandleMessage`),
`kafka.go` (`consumeLoop`, `retryUntilOK`),
`internal/application/usecases/import_legacy_classification.go`,
`internal/adapters/outbound/postgres/processed_event_repository.go`.
Omits: the offset-commit retry loop (a failed commit is retried the same way)
and logging of the outcome.

## 5. Outbox relay to Kafka

```mermaid
sequenceDiagram
  autonumber
  participant Rel as outbox.Relay in cmd/api
  participant DB as OutboxRepo, outbox_events
  participant S as RelaySink or LogSink
  participant K as Kafka warehouse.product-master.events
  loop every OUTBOX_RELAY_INTERVAL, default 1s, or at once after a full batch
    Rel->>DB: Drain(limit 100)
    DB->>DB: BEGIN, SELECT unpublished ORDER BY id LIMIT 100 FOR UPDATE SKIP LOCKED
    loop each row in id order
      DB->>S: Send(message)
      alt EVENT_PUBLISHER kafka
        S->>K: WriteMessages key SKU, value CloudEvent, content-type header, RequireAll acks
      else log
        Note over S: logs topic, type, subject, id
      end
      alt send failed
        DB->>DB: attempts plus 1, last_error, COMMIT what was already sent, stop the pass
        Note over Rel: error logged, retried after the interval with the same CloudEvents id
      else sent
        DB->>DB: published_at now, attempts plus 1
      end
    end
    DB->>DB: COMMIT
  end
```

Source: `internal/adapters/outbound/outbox/relay.go` (`Run`, `RelayOnce`,
`LogSink`), `internal/adapters/outbound/postgres/outbox_repository.go`
(`Drain`, `claim`), `internal/adapters/outbound/kafka/relay_sink.go`,
`cmd/api/main.go` (`startOutboxRelay`).
Omits: the in-memory `OutboxRepo.Drain` used without `DATABASE_URL`, and the
shutdown order (the relay stops last).

---
id: class-diagram
title: Class diagrams
sidebar_label: Class diagrams
sidebar_position: 18
---

# Class diagrams

UML class diagrams of `internal/domain/product`, then the hexagonal ports and
the adapters that implement them. Field and method names are the real Go
identifiers (unexported fields keep their lower-case names); only
state-changing methods and the key queries are shown.

Stereotypes: `<<AggregateRoot>>`, `<<ValueObject>>`, `<<Enumeration>>`,
`<<DomainEvent>>` and `<<Repository>>` (an outbound port).

## Package `product`

```mermaid
classDiagram
  direction LR
  class Product {
    <<AggregateRoot>>
    -sku SKU
    -description string
    -classification Classification
    -source ClassificationSource
    -profile PhysicalProfile
    -version int64
    +Register(sku, description, now)$ Product, Event[], error
    +Rehydrate(sku, description, classification, source, profile, version)$ Product
    +ChangeDescription(description, now) Event[], error
    +Classify(c, now) Event[], error
    +ImportLegacyClassification(c, now) Event[]
    +DeclareDimensions(d, now) Event[]
    +RecordMeasurement(m, now) Event[], error
    +Classification() Classification, ClassificationSource, error
    +IsClassified() bool
    +Version() int64
  }
  class Classification {
    <<ValueObject>>
    -tags set of HandlingTag
    -temperatureClass TemperatureClass
    -dotHazardClass DOTHazardClass
    +NewClassification(tags, temperatureClass, dotHazardClass)$ Classification, error
    +Tags() HandlingTag[]
    +HasTag(tag) bool
    +Equal(other) bool
  }
  class PhysicalProfile {
    <<ValueObject>>
    -declared UnitDimensions
    -measured Measurement
    +Declared() UnitDimensions, bool
    +Measured() Measurement, bool
    +Effective() UnitDimensions, bool
    +EffectiveSource() EffectiveSource
    +Discrepancy() bool
  }
  class UnitDimensions {
    <<ValueObject>>
    -lengthMm int64
    -widthMm int64
    -heightMm int64
    -weightG int64
    +NewUnitDimensions(l, w, h, g)$ UnitDimensions, error
    +VolumeMm3() int64
  }
  class Measurement {
    <<ValueObject>>
    -dimensions UnitDimensions
    -measuredAt Time
    -deviceID string
    +NewMeasurement(dimensions, measuredAt, deviceID, now)$ Measurement, error
  }
  class HandlingTag {
    <<Enumeration>>
    Hazmat
    Fragile
    TemperatureSensitive
    Oversized
    HighValue
  }
  class TemperatureClass {
    <<Enumeration>>
    Ambient
    Chilled
    Frozen
  }
  class ClassificationSource {
    <<Enumeration>>
    native
    legacy-import
  }
  class EffectiveSource {
    <<Enumeration>>
    measured
    declared
    none
  }
  class DOTHazardClass {
    <<ValueObject>>
    int 1..9, 0 = not recorded
  }
  class SKU {
    <<ValueObject>>
    string 1..64, no whitespace or slash
    +NewSKU(value)$ SKU, error
  }

  Product *-- SKU
  Product *-- "0..1" Classification
  Product --> ClassificationSource
  Product *-- PhysicalProfile
  Classification --> "1..5" HandlingTag
  Classification --> TemperatureClass
  Classification --> DOTHazardClass
  PhysicalProfile *-- "0..1" UnitDimensions : declared
  PhysicalProfile *-- "0..1" Measurement : latest
  PhysicalProfile ..> EffectiveSource
  Measurement *-- UnitDimensions
```

Source: `internal/domain/product/product.go`, `classification.go`,
`physical.go`, `sku.go`.
Omits: the getters (`SKU()`, `Description()`, `LengthMm()` and so on), the
`Rehydrate*` helpers for persisted value objects, the parse functions
(`ParseHandlingTag`, `ParseTemperatureClass`, `ParseClassificationSource`) and
the sentinel errors (listed on the
[aggregate design canvas](/docs/ddd/aggregate-design-canvas)).

## Domain events

```mermaid
classDiagram
  direction LR
  class Event {
    <<interface>>
    +EventName() string
    +ProductSKU() SKU
    +ProductVersion() int64
    +OccurredAt() Time
  }
  class Header {
    <<ValueObject>>
    +SKU SKU
    +Version int64
    +At Time
  }
  class ProductRegistered {
    <<DomainEvent>>
    +Description string
  }
  class ProductDescriptionChanged {
    <<DomainEvent>>
    +Description string
  }
  class ProductClassified {
    <<DomainEvent>>
    +Classification Classification
    +Source ClassificationSource
  }
  class ProductDimensionsDeclared {
    <<DomainEvent>>
    +Profile PhysicalProfile
  }
  class ProductMeasured {
    <<DomainEvent>>
    +Profile PhysicalProfile
  }
  Event <|.. ProductRegistered
  Event <|.. ProductDescriptionChanged
  Event <|.. ProductClassified
  Event <|.. ProductDimensionsDeclared
  Event <|.. ProductMeasured
  ProductRegistered *-- Header
  ProductDescriptionChanged *-- Header
  ProductClassified *-- Header
  ProductDimensionsDeclared *-- Header
  ProductMeasured *-- Header
```

Source: `internal/domain/product/events.go`.
Omits: nothing; every event embeds `Header`, which supplies `ProductSKU`,
`ProductVersion` and `OccurredAt`.

## Ports and adapters

```mermaid
classDiagram
  direction LR
  class ProductRepository {
    <<Repository>>
    +Get(ctx, sku) Product, error
    +Save(ctx, product, loadedVersion) error
    +List(ctx, filter, afterSKU, limit) Product[], error
  }
  class OutboxRepository {
    <<interface>>
    +Insert(ctx, msgs) error
  }
  class EventEncoder {
    <<interface>>
    +Encode(events) Message[], error
  }
  class ProcessedEvents {
    <<interface>>
    +Claim(ctx, consumer, eventID) bool, error
  }
  class UnitOfWork {
    <<interface>>
    +Do(ctx, fn) error
  }
  class Clock {
    <<interface>>
    +Now() Time
  }
  class Writer {
    +Products ProductRepository
    +Outbox OutboxRepository
    +Encoder EventEncoder
    +UoW UnitOfWork
    +Clock Clock
  }
  class PgProductRepo["postgres.ProductRepo"]
  class PgOutboxRepo["postgres.OutboxRepo"]
  class PgProcessed["postgres.ProcessedEventRepo"]
  class PgUoW["postgres.UnitOfWork"]
  class MemProductRepo["memory.ProductRepo"]
  class KafkaEncoder["kafka.Encoder"]
  class SystemClock["clock.System"]
  class Relay["outbox.Relay"]
  class RelaySink["kafka.RelaySink"]
  class LogSink["outbox.LogSink"]

  Writer --> ProductRepository
  Writer --> OutboxRepository
  Writer --> EventEncoder
  Writer --> UnitOfWork
  Writer --> Clock
  ProductRepository <|.. PgProductRepo
  ProductRepository <|.. MemProductRepo
  OutboxRepository <|.. PgOutboxRepo
  ProcessedEvents <|.. PgProcessed
  UnitOfWork <|.. PgUoW
  EventEncoder <|.. KafkaEncoder
  Clock <|.. SystemClock
  Relay --> PgOutboxRepo : Drain
  Relay --> RelaySink : EVENT_PUBLISHER kafka
  Relay --> LogSink : EVENT_PUBLISHER log
```

Source: `internal/application/ports/*.go`, `internal/application/usecases/writer.go`,
`internal/adapters/outbound/postgres/*.go`, `internal/adapters/outbound/memory/*.go`,
`internal/adapters/outbound/kafka/encoder.go`, `relay_sink.go`,
`internal/adapters/outbound/outbox/relay.go`, `internal/adapters/outbound/clock/clock.go`.
Omits: the in-memory `OutboxRepo`, `ProcessedEventRepo` and `UnitOfWork`
(same ports as their Postgres twins, used when `DATABASE_URL` is unset), and
the relay's `Store` / `Sink` interfaces, which `OutboxRepo` and the two sinks
implement.

## Hexagonal view

```mermaid
flowchart LR
  subgraph inbound[Inbound adapters]
    HTTP[http.Server, chi router]
    LEG[kafka.LegacyImporter]
  end
  subgraph app[Application]
    UC[usecases: RegisterProduct, ClassifyProduct, DeclareDimensions, RecordMeasurement, ImportLegacyClassification, GetProduct, ListProducts]
    PORTS[ports: ProductRepository, OutboxRepository, EventEncoder, ProcessedEvents, UnitOfWork, Clock]
  end
  subgraph domain[Domain]
    PROD[product.Product]
  end
  subgraph outbound[Outbound adapters]
    PG[postgres]
    MEM[memory]
    ENC[kafka.Encoder]
    REL[outbox.Relay with kafka.RelaySink or LogSink]
  end
  HTTP --> UC
  LEG --> UC
  UC --> PROD
  UC --> PORTS
  PG -. implements .-> PORTS
  MEM -. implements .-> PORTS
  ENC -. implements .-> PORTS
  REL --> PG
```

Source: `cmd/api/main.go` (`buildAdapters`, `buildServer`,
`startLegacyImporter`, `startOutboxRelay`), `internal/architecture/architecture_test.go`.
Omits: telemetry, readiness and the CloudEvents helper package.

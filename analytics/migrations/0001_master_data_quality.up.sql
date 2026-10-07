-- product-master analytics read model (ADR 0006).
--
-- This is the ANALYTICAL database (product_master_analytics), separate from
-- the OLTP database. It is written only by cmd/product-projector and read
-- (read-only) by cmd/product-reports. Everything here is a projection derived
-- from warehouse.product-master.analytics, not a source of truth: it can be
-- dropped and rebuilt by replaying the topic under a new consumer group.

-- Idempotency: every applied CloudEvents id is recorded here exactly once, in
-- the SAME transaction as its effect. occurred_at is the event's CloudEvents
-- `time`; max(occurred_at) is the freshness of the projection.
CREATE TABLE analytics_processed_events (
    event_id    TEXT        PRIMARY KEY,
    event_type  TEXT        NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    applied_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_analytics_processed_events_occurred_at ON analytics_processed_events (occurred_at);

-- product_facts: one row per SKU the projection has seen (any event).
--
--   registered_at, first_classified_at, first_declared_at, first_measured_at
--       the CloudEvents time of the EARLIEST ProductRegistered /
--       ProductClassified / ProductDimensionsDeclared / ProductMeasured seen
--       for the SKU (kept with LEAST, so arrival order does not matter); NULL
--       until one is seen. The per-day counts of the report read these.
--   profile_version, has_declared, has_measured, discrepancy
--       the CURRENT physical-profile state, from the profile event with the
--       highest aggregate version (overwritten only by a greater version:
--       the ADR 0004 consumer rule). profile_version 0 = no profile event yet.
CREATE TABLE product_facts (
    sku                 TEXT COLLATE "C" PRIMARY KEY,
    registered_at       TIMESTAMPTZ,
    first_classified_at TIMESTAMPTZ,
    first_declared_at   TIMESTAMPTZ,
    first_measured_at   TIMESTAMPTZ,
    profile_version     BIGINT      NOT NULL DEFAULT 0 CHECK (profile_version >= 0),
    has_declared        BOOLEAN     NOT NULL DEFAULT false,
    has_measured        BOOLEAN     NOT NULL DEFAULT false,
    discrepancy         BOOLEAN     NOT NULL DEFAULT false,
    CHECK (NOT discrepancy OR (has_declared AND has_measured))
);

CREATE INDEX idx_product_facts_registered_at       ON product_facts (registered_at)       WHERE registered_at IS NOT NULL;
CREATE INDEX idx_product_facts_first_classified_at ON product_facts (first_classified_at) WHERE first_classified_at IS NOT NULL;
CREATE INDEX idx_product_facts_first_declared_at   ON product_facts (first_declared_at)   WHERE first_declared_at IS NOT NULL;
CREATE INDEX idx_product_facts_first_measured_at   ON product_facts (first_measured_at)   WHERE first_measured_at IS NOT NULL;

-- profile_states: the physical-profile history, one row per profile event
-- (sku, aggregate version). "Discrepancies open at the end of day D" is the
-- latest state (highest version) of each SKU among the rows with
-- occurred_at < the end of D; product_facts alone cannot answer it once a
-- discrepancy has been resolved.
CREATE TABLE profile_states (
    sku          TEXT COLLATE "C" NOT NULL,
    version      BIGINT      NOT NULL CHECK (version >= 1),
    occurred_at  TIMESTAMPTZ NOT NULL,
    has_declared BOOLEAN     NOT NULL,
    has_measured BOOLEAN     NOT NULL,
    discrepancy  BOOLEAN     NOT NULL,
    PRIMARY KEY (sku, version),
    CHECK (NOT discrepancy OR (has_declared AND has_measured))
);

CREATE INDEX idx_profile_states_occurred_at ON profile_states (occurred_at);
